package ingestapi

// POST /internal/ingest/source-status: producer internal (coastal, satellite) melaporkan kesehatan
// sumbernya, mengikuti docs/KONTRAK_INTERNAL_INGEST.md bagian 4. Ditulis dengan bantuan LLM (Claude).
//
// Laporan disimpan di koleksi source_status yang sama dengan status poll BMKG/PVMBG, sehingga
// meta.sources / stale_since di API dan panel Grafana ikut menampilkannya. Waktu acuan
// kebaruan adalah waktu terima di server (bukan jam pengirim).

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
)

type StatusReporter interface {
	ReportSourceStatus(ctx context.Context, source domain.Source, healthy bool, reason string, at time.Time) error
}

type sourceStatusRequest struct {
	Source      domain.Source  `json:"source"`
	Status      string         `json:"status"`
	LastEventAt *time.Time     `json:"last_event_at,omitempty"`
	LagSeconds  *float64       `json:"lag_seconds,omitempty"`
	ReasonCode  string         `json:"reason_code,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

// RegisterSourceStatus: token coastal hanya boleh melaporkan SENSOR_PANTAI dan token satellite
// hanya SATELIT (izin minimum, kontrak bagian 6).
func RegisterSourceStatus(mux *http.ServeMux, logger *slog.Logger, reporter StatusReporter, coastalToken, satelliteToken string) {
	mux.HandleFunc("POST /internal/ingest/source-status", func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !authorized(header, coastalToken, satelliteToken) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeProblem(w, http.StatusUnauthorized, "unauthorized", "token tidak valid")
			return
		}
		var req sourceStatusRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
			writeProblem(w, http.StatusBadRequest, "bad_request", "JSON tidak valid")
			return
		}
		var allowed bool
		switch req.Source {
		case domain.SourceSensorPantai:
			allowed = authorized(header, coastalToken)
		case domain.SourceSatelit:
			allowed = authorized(header, satelliteToken)
		default:
			writeProblem(w, http.StatusBadRequest, "bad_request", "source harus SENSOR_PANTAI atau SATELIT")
			return
		}
		if !allowed {
			writeProblem(w, http.StatusForbidden, "forbidden", "token tidak boleh melaporkan source ini")
			return
		}
		var healthy bool
		switch req.Status {
		case "ok":
			healthy = true
		case "degraded", "unavailable":
		default:
			writeProblem(w, http.StatusBadRequest, "bad_request", "status harus ok, degraded, atau unavailable")
			return
		}
		reason := req.ReasonCode
		if reason == "" && !healthy {
			reason = req.Status
		}
		if err := reporter.ReportSourceStatus(r.Context(), req.Source, healthy, reason, time.Now().UTC()); err != nil {
			logger.ErrorContext(r.Context(), "store source status failed", "source", req.Source, "error", err.Error())
			writeProblem(w, http.StatusServiceUnavailable, "unavailable", "status sumber belum dapat disimpan")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
