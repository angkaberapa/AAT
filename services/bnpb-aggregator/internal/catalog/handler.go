package catalog

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/mapping"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

const (
	maxBatchEvents = 500
	maxBodyBytes   = 16 << 20
)

type seismicMapper interface {
	MapSeismicEvent(mapping.SeismicEvent, *mapping.TsunamiWarning) (domain.HazardEvent, error)
}

type importer interface {
	ImportBatch(context.Context, []domain.HazardEvent) (map[store.SaveResult]int, error)
}

type importRequest struct {
	Events []mapping.SeismicEvent `json:"events"`
}

type importResponse struct {
	Received  int `json:"received"`
	Inserted  int `json:"inserted"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
}

func Register(mux *http.ServeMux, logger *slog.Logger, mapper seismicMapper, destination importer, token string) {
	mux.HandleFunc("POST /internal/catalog/seismic-events", func(w http.ResponseWriter, r *http.Request) {
		if !validToken(r.Header.Get("Authorization"), token) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "unauthorized", "token tidak valid")
			return
		}
		var request importRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid catalogue JSON: "+err.Error())
			return
		}
		if len(request.Events) == 0 || len(request.Events) > maxBatchEvents {
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", "events must contain between 1 and 500 records")
			return
		}

		events := make([]domain.HazardEvent, 0, len(request.Events))
		for index, raw := range request.Events {
			event, err := mapper.MapSeismicEvent(raw, nil)
			if err != nil {
				writeError(w, http.StatusUnprocessableEntity, "validation_failed",
					"events["+jsonNumber(index)+"] is invalid: "+err.Error())
				return
			}
			events = append(events, event)
		}
		counts, err := destination.ImportBatch(r.Context(), events)
		if err != nil {
			logger.ErrorContext(r.Context(), "historical catalogue import failed", "events", len(events), "error", err.Error())
			writeError(w, http.StatusServiceUnavailable, "unavailable", "canonical storage is temporarily unavailable")
			return
		}
		core.WriteJSON(w, http.StatusOK, importResponse{
			Received: len(events), Inserted: counts[store.Inserted], Updated: counts[store.Updated], Unchanged: counts[store.Unchanged],
		})
	})
}

func validToken(header, expected string) bool {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(parts[1]), []byte(expected)) == 1
}

func jsonNumber(value int) string {
	return strconv.Itoa(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	core.WriteJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
