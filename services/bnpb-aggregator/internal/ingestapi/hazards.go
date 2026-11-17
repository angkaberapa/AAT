// Package ingestapi accepts canonical HazardEvent changes from the satellite
// and coastal producers.
package ingestapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

const (
	maxRequestBytes = 2 << 20
	operationUpsert = "UPSERT"
	operationDelete = "DELETE"
)

type Writer interface {
	IngestHazard(ctx context.Context, event domain.HazardEvent) (store.SaveResult, error)
	DeleteHazard(ctx context.Context, hazardID string) (bool, error)
}

type hazardRequest struct {
	Operation       string               `json:"operation"`
	Producer        string               `json:"producer"`
	ProducerEventID string               `json:"producer_event_id"`
	SourcePosition  store.SourcePosition `json:"source_position"`
	Event           *domain.HazardEvent  `json:"event"`
	HazardID        string               `json:"hazard_id"`
	Source          domain.Source        `json:"source"`
	SourceRefID     string               `json:"source_ref_id"`
}

type response struct {
	HazardID        string `json:"hazard_id"`
	Result          string `json:"result"`
	ProducerEventID string `json:"producer_event_id"`
}

// RegisterHazards wires POST /internal/ingest/hazards. Both producer tokens are
// accepted so coastal and satellite can stay separate.
func RegisterHazards(mux *http.ServeMux, logger *slog.Logger, writer Writer, coastalToken, satelliteToken string) {
	mux.HandleFunc("POST /internal/ingest/hazards", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r.Header.Get("Authorization"), coastalToken, satelliteToken) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeProblem(w, http.StatusUnauthorized, "unauthorized", "token tidak valid")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "bad_request", "body tidak dapat dibaca")
			return
		}
		if len(body) > maxRequestBytes {
			writeProblem(w, http.StatusRequestEntityTooLarge, "payload_too_large", "body melebihi batas")
			return
		}
		var request hazardRequest
		if err := json.Unmarshal(body, &request); err != nil {
			writeProblem(w, http.StatusBadRequest, "bad_request", "JSON tidak valid")
			return
		}
		if request.Producer == "" || request.ProducerEventID == "" {
			writeProblem(w, http.StatusBadRequest, "bad_request", "producer dan producer_event_id wajib")
			return
		}
		if key := r.Header.Get("Idempotency-Key"); key != request.ProducerEventID {
			writeProblem(w, http.StatusBadRequest, "bad_request", "Idempotency-Key harus sama dengan producer_event_id")
			return
		}

		switch strings.ToUpper(request.Operation) {
		case operationUpsert:
			event := request.Event
			if event == nil {
				writeProblem(w, http.StatusBadRequest, "bad_request", "event wajib untuk UPSERT")
				return
			}
			if event.Source == "" || event.SourceRefID == "" {
				writeProblem(w, http.StatusUnprocessableEntity, "unprocessable", "source dan source_ref_id wajib")
				return
			}
			if event.HazardID == "" {
				event.HazardID = domain.NewHazardID(event.Source, event.SourceRefID)
			}
			event.IngestedAt = time.Now().UTC()
			result, err := writer.IngestHazard(r.Context(), *event)
			if err != nil {
				logger.ErrorContext(r.Context(), "ingest hazard failed", "hazard_id", event.HazardID, "error", err.Error())
				writeProblem(w, http.StatusServiceUnavailable, "unavailable", "penyimpanan sedang tidak tersedia")
				return
			}
			writeJSON(w, http.StatusOK, response{
				HazardID:        event.HazardID,
				Result:          string(result),
				ProducerEventID: request.ProducerEventID,
			})
		case operationDelete:
			hazardID := request.HazardID
			if hazardID == "" && request.Source != "" && request.SourceRefID != "" {
				hazardID = domain.NewHazardID(request.Source, request.SourceRefID)
			}
			if hazardID == "" {
				writeProblem(w, http.StatusBadRequest, "bad_request", "hazard_id atau source+source_ref_id wajib")
				return
			}
			deleted, err := writer.DeleteHazard(r.Context(), hazardID)
			if err != nil {
				logger.ErrorContext(r.Context(), "delete hazard failed", "hazard_id", hazardID, "error", err.Error())
				writeProblem(w, http.StatusServiceUnavailable, "unavailable", "penyimpanan sedang tidak tersedia")
				return
			}
			result := "deleted"
			if !deleted {
				result = "not_found"
			}
			writeJSON(w, http.StatusOK, response{
				HazardID:        hazardID,
				Result:          result,
				ProducerEventID: request.ProducerEventID,
			})
		default:
			writeProblem(w, http.StatusBadRequest, "bad_request", "operation harus UPSERT atau DELETE")
		}
	})
}

func authorized(header string, tokens ...string) bool {
	value := strings.TrimSpace(header)
	if !strings.HasPrefix(value, "Bearer ") {
		return false
	}
	presented := strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
	if presented == "" {
		return false
	}
	for _, token := range tokens {
		if token == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(presented), []byte(token)) == 1 {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeProblem(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
