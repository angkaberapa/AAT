package fieldreports

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

const maxFieldReportBytes = 1 << 20

type fieldReportStore interface {
	AcceptFieldReport(context.Context, store.FieldReport) (store.FieldReport, bool, error)
	ClaimFieldReport(context.Context, string, time.Duration) (*store.FieldReport, error)
	CompleteFieldReport(context.Context, string, string) error
	GetFieldReport(context.Context, string) (*store.FieldReport, error)
}

type fieldReportRequest struct {
	OccurredAt  time.Time      `json:"occurred_at"`
	AreaName    string         `json:"area_name"`
	Latitude    float64        `json:"latitude"`
	Longitude   float64        `json:"longitude"`
	Severity    string         `json:"severity"`
	Description string         `json:"description"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

func Register(mux *http.ServeMux, logger *slog.Logger, destination fieldReportStore, ingestToken, auditToken string) {
	mux.HandleFunc("POST /internal/field-reports", func(w http.ResponseWriter, r *http.Request) {
		if !validBearer(r.Header.Get("Authorization"), ingestToken) {
			writeProblem(w, http.StatusUnauthorized, "unauthorized", "token tidak valid")
			return
		}
		idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		clientID := strings.TrimSpace(r.Header.Get("X-Client-ID"))
		if idempotencyKey == "" || len(idempotencyKey) > 200 || clientID == "" {
			writeProblem(w, http.StatusBadRequest, "bad_request", "Idempotency-Key and X-Client-ID are required")
			return
		}
		var payload fieldReportRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFieldReportBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			writeProblem(w, http.StatusBadRequest, "bad_request", "invalid field report: "+err.Error())
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			writeProblem(w, http.StatusBadRequest, "bad_request", "field report must contain one JSON document")
			return
		}
		if problem := validateFieldReport(payload); problem != "" {
			writeProblem(w, http.StatusUnprocessableEntity, "validation_failed", problem)
			return
		}
		report, inserted, err := destination.AcceptFieldReport(r.Context(), store.FieldReport{
			ReportID: fieldReportID(clientID, idempotencyKey), IdempotencyKey: idempotencyKey, ClientID: clientID,
			OccurredAt: payload.OccurredAt, AreaName: strings.TrimSpace(payload.AreaName), Latitude: payload.Latitude,
			Longitude: payload.Longitude, Severity: payload.Severity, Description: strings.TrimSpace(payload.Description),
			Attributes: payload.Attributes,
		})
		if errors.Is(err, store.ErrIdempotencyConflict) {
			writeProblem(w, http.StatusConflict, "idempotency_conflict", err.Error())
			return
		}
		if err != nil {
			logger.ErrorContext(r.Context(), "accept field report failed", "error", err.Error())
			writeProblem(w, http.StatusServiceUnavailable, "unavailable", "field report storage is temporarily unavailable")
			return
		}
		core.WriteJSON(w, http.StatusAccepted, map[string]any{
			"report_id": report.ReportID, "status": report.Status, "accepted": true, "inserted": inserted,
		})
	})

	mux.HandleFunc("GET /internal/audit/field-reports/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !validBearer(r.Header.Get("Authorization"), auditToken) {
			writeProblem(w, http.StatusUnauthorized, "unauthorized", "token tidak valid")
			return
		}
		report, err := destination.GetFieldReport(r.Context(), r.PathValue("id"))
		if err != nil {
			writeProblem(w, http.StatusServiceUnavailable, "unavailable", "field report storage is temporarily unavailable")
			return
		}
		if report == nil {
			writeProblem(w, http.StatusNotFound, "not_found", "field report tidak ditemukan")
			return
		}
		core.WriteJSON(w, http.StatusOK, report)
	})
}

func RunWorker(ctx context.Context, logger *slog.Logger, destination fieldReportStore, owner string, interval, lease, processingDelay time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		for {
			report, err := destination.ClaimFieldReport(ctx, owner, lease)
			if err != nil {
				if ctx.Err() == nil {
					logger.WarnContext(ctx, "claim field report failed", "error", err.Error())
				}
				break
			}
			if report == nil {
				break
			}
			if processingDelay > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(processingDelay):
				}
			}
			if err := destination.CompleteFieldReport(ctx, report.ReportID, owner); err != nil {
				logger.WarnContext(ctx, "complete field report failed", "report_id", report.ReportID, "error", err.Error())
				break
			}
			logger.InfoContext(ctx, "field report processed", "report_id", report.ReportID, "attempt", report.Attempts)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func validateFieldReport(payload fieldReportRequest) string {
	if payload.OccurredAt.IsZero() || strings.TrimSpace(payload.AreaName) == "" || strings.TrimSpace(payload.Description) == "" {
		return "occurred_at, area_name, and description are required"
	}
	if math.IsNaN(payload.Latitude) || math.IsInf(payload.Latitude, 0) || payload.Latitude < -90 || payload.Latitude > 90 ||
		math.IsNaN(payload.Longitude) || math.IsInf(payload.Longitude, 0) || payload.Longitude < -180 || payload.Longitude > 180 {
		return "coordinates are outside valid ranges"
	}
	switch payload.Severity {
	case "NORMAL", "WASPADA", "SIAGA", "AWAS":
	default:
		return "severity must be NORMAL, WASPADA, SIAGA, or AWAS"
	}
	return ""
}

func fieldReportID(clientID, key string) string {
	digest := sha256.Sum256([]byte(clientID + ":" + key))
	return "FR-" + hex.EncodeToString(digest[:16])
}

func validBearer(header, expected string) bool {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(parts[1]), []byte(expected)) == 1
}

func writeProblem(w http.ResponseWriter, status int, code, message string) {
	core.WriteJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
