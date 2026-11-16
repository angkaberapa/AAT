package quarantine

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

const maxUnprocessedRequestBytes = 48 << 20

type unprocessedWriter interface {
	StoreUnprocessed(ctx context.Context, message store.UnprocessedMessage) (bool, error)
}

type unprocessedRequest struct {
	MessageID        string               `json:"message_id"`
	Source           string               `json:"source"`
	SourceRefID      string               `json:"source_ref_id,omitempty"`
	SourcePosition   store.SourcePosition `json:"source_position"`
	ReceivedAt       time.Time            `json:"received_at"`
	Reason           string               `json:"reason"`
	Retryable        bool                 `json:"retryable"`
	Status           string               `json:"status"`
	RawPayloadBase64 string               `json:"raw_payload_base64"`
	ContentType      string               `json:"content_type"`
}

func RegisterUnprocessedIngest(mux *http.ServeMux, logger *slog.Logger, writer unprocessedWriter, token string) {
	mux.HandleFunc("POST /internal/ingest/unprocessed", func(w http.ResponseWriter, r *http.Request) {
		if !validBearer(r.Header.Get("Authorization"), token) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeProblem(w, http.StatusUnauthorized, "unauthorized", "token tidak valid")
			return
		}
		var request unprocessedRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUnprocessedRequestBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeProblem(w, http.StatusBadRequest, "bad_request", "invalid unprocessed message: "+err.Error())
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			writeProblem(w, http.StatusBadRequest, "bad_request", "request must contain one JSON document")
			return
		}
		if request.Source == "" || !domain.Source(request.Source).Valid() {
			writeProblem(w, http.StatusUnprocessableEntity, "validation_failed", "source is unknown")
			return
		}
		if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" && key != request.MessageID {
			writeProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key must equal message_id")
			return
		}
		raw, err := base64.StdEncoding.DecodeString(request.RawPayloadBase64)
		if err != nil {
			writeProblem(w, http.StatusUnprocessableEntity, "validation_failed", "raw_payload_base64 is invalid")
			return
		}
		inserted, err := writer.StoreUnprocessed(r.Context(), store.UnprocessedMessage{
			MessageID: request.MessageID, Source: request.Source, SourceRefID: request.SourceRefID,
			SourcePosition: request.SourcePosition, ReceivedAt: request.ReceivedAt, Reason: request.Reason,
			Retryable: request.Retryable, Status: request.Status, RawPayload: raw, ContentType: request.ContentType,
		})
		if err != nil {
			logger.ErrorContext(r.Context(), "store unprocessed failed", "message_id", request.MessageID, "error", err.Error())
			writeProblem(w, http.StatusServiceUnavailable, "unavailable", "unprocessed storage is temporarily unavailable")
			return
		}
		status := http.StatusOK
		if inserted {
			status = http.StatusCreated
		}
		core.WriteJSON(w, status, map[string]any{"message_id": request.MessageID, "stored": true, "inserted": inserted})
	})
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
