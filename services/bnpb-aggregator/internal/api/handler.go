package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/ingest"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

// Batas jumlah event per snapshot (P2: cegah respons membengkak seiring data bertambah).
// Gak berlaku untuk query dengan `since` (lihat store.Filter.Limit).
const (
	defaultLimit = 200
	maxLimit     = 1000
)

// Reader = yang dibutuhin api dari store. sengaja interface kecil, biar gampang dimock waktu test handler.
type Reader interface {
	List(ctx context.Context, f store.Filter) ([]domain.HazardEvent, error)
	Get(ctx context.Context, hazardID string) (*domain.HazardEvent, error)
}

// StatusProvider = status kebaruan data per sumber (diisi poller).
type StatusProvider interface {
	Status(source domain.Source) ingest.SourceStatus
	Snapshot() []ingest.SourceStatus
}

// Register daftarin route read yang dipakai bnpb-api. bnpb-api nerusin path + query apa adanya (kecuali `fields`), jadi di sini tinggal terima.
func Register(mux *http.ServeMux, logger *slog.Logger, reader Reader, statuses StatusProvider) {
	mux.HandleFunc("GET /hazards", func(w http.ResponseWriter, r *http.Request) {
		filter, problem := parseFilter(r)
		if problem != "" {
			writeError(w, http.StatusBadRequest, problem)
			return
		}

		events, err := reader.List(r.Context(), filter)
		if err != nil {
			logger.ErrorContext(r.Context(), "list hazard events failed", "error", err.Error())
			writeError(w, http.StatusInternalServerError, "failed to read hazard events")
			return
		}

		//data dari store tetap dilayani walau sumbernya lagi mati; meta.sources ngasih tau seberapa basi datanya.
		meta := freshnessMeta(statuses, relevantSources(filter))
		w.Header().Set(HeaderDataStale, strconv.FormatBool(meta["stale"].(bool)))
		core.WriteJSON(w, http.StatusOK, map[string]any{"data": events, "meta": meta})
	})

	//status lengkap per sumber (termasuk pesan error terakhir), buat operator / debugging.
	mux.HandleFunc("GET /sources", func(w http.ResponseWriter, r *http.Request) {
		core.WriteJSON(w, http.StatusOK, map[string]any{"data": statuses.Snapshot()})
	})

	mux.HandleFunc("GET /hazards/{id}", func(w http.ResponseWriter, r *http.Request) {
		event, err := reader.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			logger.ErrorContext(r.Context(), "get hazard event failed", "error", err.Error())
			writeError(w, http.StatusInternalServerError, "failed to read hazard event")
			return
		}
		if event == nil {
			writeError(w, http.StatusNotFound, "hazard event not found")
			return
		}

		//respons event tunggal = objek event apa adanya, jadi penanda kebaruannya lewat header.
		setFreshnessHeaders(w, statuses.Status(event.Source))
		core.WriteJSON(w, http.StatusOK, event)
	})
}

// parseFilter balikin (filter, pesanError).
// todo: validasi enum-nya masih hardcoded di sini. kalau nambah Source atau HazardType baru update di sini + di domain.
func parseFilter(r *http.Request) (store.Filter, string) {
	var f store.Filter
	q := r.URL.Query()

	if raw := q.Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if !strings.HasSuffix(raw, "Z") || err != nil {
			return f, "invalid 'since', expected ISO 8601 UTC e.g. 2026-09-25T10:00:00Z"
		}
		f.Since = &t
	}

	if raw := q.Get("source"); raw != "" {
		f.Source = domain.Source(raw)
		if f.Source != domain.SourceBMKG && f.Source != domain.SourcePVMBG {
			return f, "invalid 'source', expected BMKG or PVMBG"
		}
	}

	if raw := q.Get("hazard_type"); raw != "" {
		f.HazardType = domain.HazardType(raw)
		if f.HazardType != domain.HazardTypeSeismic && f.HazardType != domain.HazardTypeVolcanic {
			return f, "invalid 'hazard_type', expected SEISMIC or VOLCANIC"
		}
	}

	if raw := q.Get("severity"); raw != "" {
		f.Severity = domain.Severity(raw)
		switch f.Severity {
		case domain.SeverityNormal, domain.SeverityWaspada,
			domain.SeveritySiaga, domain.SeverityAwas:
		default:
			return f, "invalid 'severity', expected NORMAL, WASPADA, SIAGA or AWAS"
		}
	}

	f.Limit = defaultLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxLimit {
			return f, "invalid 'limit', expected an integer between 1 and " + strconv.Itoa(maxLimit)
		}
		f.Limit = n
	}

	return f, ""
}

// Header penanda kebaruan data (P2). Dipasang di semua respons baca; bnpb-api meneruskannya ke client.
const (
	HeaderDataStale    = "X-Data-Stale"    // "true" kalau ada sumber relevan yang gak "ok"
	HeaderSourceStatus = "X-Source-Status" // event tunggal: status sumbernya
	HeaderStaleSince   = "X-Stale-Since"   // event tunggal: sejak kapan sumbernya gak diperbarui
)

func setFreshnessHeaders(w http.ResponseWriter, st ingest.SourceStatus) {
	w.Header().Set(HeaderDataStale, strconv.FormatBool(st.Status != ingest.StatusOK))
	w.Header().Set(HeaderSourceStatus, st.Status)
	if st.StaleSince != nil {
		w.Header().Set(HeaderStaleSince, st.StaleSince.UTC().Format(time.RFC3339Nano))
	}
}

// relevantSources = sumber yang datanya bisa muncul di hasil query ini.
func relevantSources(f store.Filter) []domain.Source {
	switch {
	case f.Source != "":
		return []domain.Source{f.Source}
	case f.HazardType == domain.HazardTypeSeismic:
		return []domain.Source{domain.SourceBMKG}
	case f.HazardType == domain.HazardTypeVolcanic:
		return []domain.Source{domain.SourcePVMBG}
	default:
		return []domain.Source{domain.SourceBMKG, domain.SourcePVMBG}
	}
}

// publicStatus = SourceStatus tanpa detail error internal. meta ikut diteruskan ke semua client
// (termasuk Media), jadi pesan error mentah dari sumber gak ikut dibocorin.
type publicStatus struct {
	Status        string     `json:"status"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	StaleSince    *time.Time `json:"stale_since,omitempty"`
}

func freshnessMeta(statuses StatusProvider, sources []domain.Source) map[string]any {
	perSource := make(map[string]publicStatus, len(sources))
	stale := false
	for _, src := range sources {
		st := statuses.Status(src)
		perSource[string(src)] = publicStatus{
			Status:        st.Status,
			LastSuccessAt: st.LastSuccessAt,
			StaleSince:    st.StaleSince,
		}
		if st.Status != ingest.StatusOK {
			stale = true
		}
	}
	return map[string]any{"stale": stale, "sources": perSource}
}

func writeError(w http.ResponseWriter, status int, message string) {
	core.WriteJSON(w, status, map[string]string{"error": message})
}