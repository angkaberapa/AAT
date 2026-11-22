// Package dashboard keeps the latest state of every hazard, built only from the
// hazard topic, and serves it at GET /dashboard (P5 consumer #1).
package dashboard

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-consumer-dashboard/internal/consumer"
	"github.com/angkaberapa/AAT/services/bnpb-consumer-dashboard/internal/core"
)

type entry struct {
	Hazard     consumer.Hazard `json:"hazard"`
	Version    int             `json:"version"`
	ReceivedAt time.Time       `json:"received_at"`
}

// M2 (ditulis dengan bantuan LLM, Claude): state M1 menyimpan HazardEvent lengkap untuk setiap
// hazard dan menulis ulang semuanya tiap batch; dengan ratusan ribu event Sensor Pantai pod ini
// OOMKilled (limit 256Mi). Sekarang tiap hazard hanya disimpan ringkasannya (cukup untuk dedupe
// dan hitungan), dan event lengkap hanya untuk keepLatest event terbaru.
const keepLatest = 50

type summary struct {
	Version    int    `json:"version"`
	Severity   string `json:"severity"`
	HazardType string `json:"hazard_type"`
}

type state struct {
	Hazards    map[string]summary `json:"hazards"`
	Latest     []entry            `json:"latest"` // terbaru di depan, maksimal keepLatest
	Processed  int                `json:"processed"`
	Duplicates int                `json:"duplicates_ignored"`
}

type Dashboard struct {
	mu     sync.RWMutex
	state  state
	file   consumer.FileState
	logger *slog.Logger
	now    func() time.Time
}

// New restores the persisted state, so a restart does not empty the dashboard.
func New(file consumer.FileState, logger *slog.Logger) (*Dashboard, error) {
	st, err := loadState(file.Path())
	if err != nil {
		return nil, err
	}
	return &Dashboard{state: st, file: file, logger: logger, now: time.Now}, nil
}

// loadState membaca file state secara streaming (satu hazard sekali decode), sehingga file
// besar format M1 ({"hazards": {id: {"hazard": {...}, "version", "received_at"}}}) bisa
// dimigrasikan tanpa memuat seluruh isinya ke memori.
func loadState(path string) (state, error) {
	st := state{Hazards: map[string]summary{}}
	if path == "" {
		return st, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, fmt.Errorf("read state %s: %w", path, err)
	}
	defer f.Close()
	fail := func(err error) (state, error) { return st, fmt.Errorf("decode state %s: %w", path, err) }

	dec := json.NewDecoder(bufio.NewReader(f))
	if _, err := dec.Token(); err != nil { // '{'
		return fail(err)
	}
	var legacyLatest []entry
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return fail(err)
		}
		switch tok {
		case "hazards":
			if _, err := dec.Token(); err != nil { // '{'
				return fail(err)
			}
			for dec.More() {
				idTok, err := dec.Token()
				if err != nil {
					return fail(err)
				}
				id, _ := idTok.(string)
				var rec struct {
					summary
					Hazard     *consumer.Hazard `json:"hazard"` // format M1
					ReceivedAt time.Time        `json:"received_at"`
				}
				if err := dec.Decode(&rec); err != nil {
					return fail(err)
				}
				sum := rec.summary
				if rec.Hazard != nil {
					sum.Severity, sum.HazardType = rec.Hazard.Severity, rec.Hazard.HazardType
					legacyLatest = pushLatest(legacyLatest, entry{Hazard: *rec.Hazard, Version: rec.Version, ReceivedAt: rec.ReceivedAt})
				}
				st.Hazards[id] = sum
			}
			if _, err := dec.Token(); err != nil { // '}'
				return fail(err)
			}
		case "latest":
			if err := dec.Decode(&st.Latest); err != nil {
				return fail(err)
			}
		case "processed":
			if err := dec.Decode(&st.Processed); err != nil {
				return fail(err)
			}
		case "duplicates_ignored":
			if err := dec.Decode(&st.Duplicates); err != nil {
				return fail(err)
			}
		default:
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return fail(err)
			}
		}
	}
	if len(st.Latest) == 0 {
		st.Latest = legacyLatest
	}
	return st, nil
}

// pushLatest menyisipkan e ke daftar terurut (terbaru di depan), menggantikan versi lama hazard
// yang sama, dan memotong daftar menjadi keepLatest.
func pushLatest(list []entry, e entry) []entry {
	out := make([]entry, 0, min(len(list)+1, keepLatest))
	inserted := false
	for _, cur := range list {
		if cur.Hazard.HazardID == e.Hazard.HazardID {
			continue
		}
		if !inserted && !e.ReceivedAt.Before(cur.ReceivedAt) {
			out = append(out, e)
			inserted = true
		}
		out = append(out, cur)
	}
	if !inserted {
		out = append(out, e)
	}
	if len(out) > keepLatest {
		out = out[:keepLatest]
	}
	return out
}

// Handle is idempotent: a version that is not newer than the stored one (a redelivery
// or a duplicate publish) changes nothing.
func (d *Dashboard) Handle(ctx context.Context, msg consumer.Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if prev, ok := d.state.Hazards[msg.Hazard.HazardID]; ok && msg.Version <= prev.Version {
		d.state.Duplicates++
		d.logger.DebugContext(ctx, "duplicate ignored", "event_id", msg.EventID, "stored_version", prev.Version)
		return nil
	}
	d.state.Hazards[msg.Hazard.HazardID] = summary{
		Version: msg.Version, Severity: msg.Hazard.Severity, HazardType: msg.Hazard.HazardType,
	}
	d.state.Latest = pushLatest(d.state.Latest, entry{Hazard: msg.Hazard, Version: msg.Version, ReceivedAt: d.now().UTC()})
	d.state.Processed++
	d.logger.InfoContext(ctx, "dashboard updated",
		"event_id", msg.EventID, "type", msg.Type,
		"hazard_type", msg.Hazard.HazardType, "severity", msg.Hazard.Severity, "area", msg.Hazard.AreaName)
	return nil
}

func (d *Dashboard) Flush(context.Context) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.file.Save(d.state)
}

type view struct {
	Total      int            `json:"total"`
	BySeverity map[string]int `json:"by_severity"`
	ByType     map[string]int `json:"by_type"`
	Latest     []entry        `json:"latest"`
	Stats      struct {
		Processed     int        `json:"processed"`
		Duplicates    int        `json:"duplicates_ignored"`
		LastReceiveAt *time.Time `json:"last_received_at,omitempty"`
	} `json:"stats"`
}

func (d *Dashboard) View(latest int) view {
	d.mu.RLock()
	defer d.mu.RUnlock()
	v := view{BySeverity: map[string]int{}, ByType: map[string]int{}}
	for _, h := range d.state.Hazards {
		v.Total++
		v.BySeverity[h.Severity]++
		v.ByType[h.HazardType]++
	}
	entries := d.state.Latest
	if len(entries) > latest {
		entries = entries[:latest]
	}
	v.Latest = append([]entry(nil), entries...)
	v.Stats.Processed = d.state.Processed
	v.Stats.Duplicates = d.state.Duplicates
	if len(entries) > 0 {
		t := entries[0].ReceivedAt
		v.Stats.LastReceiveAt = &t
	}
	return v
}

func (d *Dashboard) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /dashboard", func(w http.ResponseWriter, _ *http.Request) {
		core.WriteJSON(w, http.StatusOK, d.View(10))
	})
}
