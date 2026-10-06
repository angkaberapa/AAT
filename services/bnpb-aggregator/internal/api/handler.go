package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

//Reader = yang dibutuhin api dari store. sengaja interface kecil, biar gampang dimock waktu test handler.
type Reader interface {
	List(ctx context.Context, f store.Filter) ([]domain.HazardEvent, error)
	Get(ctx context.Context, hazardID string) (*domain.HazardEvent, error)
}

//Register daftarin route read yang dipakai bnpb-api. bnpb-api nerusin path + query apa adanya (kecuali `fields`), jadi di sini tinggal terima.
func Register(mux *http.ServeMux, logger *slog.Logger, reader Reader) {
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

		core.WriteJSON(w, http.StatusOK, map[string]any{"data": events})
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

		core.WriteJSON(w, http.StatusOK, event)
	})
}

//parseFilter balikin (filter, pesanError).
//todo: validasi enum-nya masih hardcoded di sini. kalau nambah Source atau HazardType baru update di sini + di domain.
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

	return f, ""
}

func writeError(w http.ResponseWriter, status int, message string) {
	core.WriteJSON(w, status, map[string]string{"error": message})
}