// Package dashboard keeps the latest state of every hazard, built only from the
// hazard topic, and serves it at GET /dashboard (P5 consumer #1).
package dashboard

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
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

type state struct {
	Hazards    map[string]entry `json:"hazards"`
	Processed  int              `json:"processed"`
	Duplicates int              `json:"duplicates_ignored"`
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
	d := &Dashboard{
		state:  state{Hazards: map[string]entry{}},
		file:   file,
		logger: logger,
		now:    time.Now,
	}
	if err := file.Load(&d.state); err != nil {
		return nil, err
	}
	if d.state.Hazards == nil {
		d.state.Hazards = map[string]entry{}
	}
	return d, nil
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
	d.state.Hazards[msg.Hazard.HazardID] = entry{Hazard: msg.Hazard, Version: msg.Version, ReceivedAt: d.now().UTC()}
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
	entries := make([]entry, 0, len(d.state.Hazards))
	for _, e := range d.state.Hazards {
		v.Total++
		v.BySeverity[e.Hazard.Severity]++
		v.ByType[e.Hazard.HazardType]++
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ReceivedAt.After(entries[j].ReceivedAt) })
	if len(entries) > latest {
		entries = entries[:latest]
	}
	v.Latest = entries
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
