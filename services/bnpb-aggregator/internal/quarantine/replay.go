package quarantine

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/mapping"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/source/pvmbg"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

type replayStore interface {
	ClaimUnprocessed(context.Context, string, string, time.Duration, ...string) (*store.UnprocessedMessage, error)
	ClaimUnprocessedByID(context.Context, string, string, string, time.Duration, ...string) (*store.UnprocessedMessage, error)
	ResolveUnprocessed(context.Context, string, string) error
	ReleaseUnprocessed(context.Context, string, string, string) error
	ImportBatch(context.Context, []domain.HazardEvent) (map[store.SaveResult]int, error)
}

var replayMu sync.Mutex

func RegisterReplay(mux *http.ServeMux, logger *slog.Logger, destination replayStore, mapper *mapping.Mapper, token, owner string, compatibilityVersion int) {
	mux.HandleFunc("POST /internal/admin/unprocessed/replay", func(w http.ResponseWriter, r *http.Request) {
		if !validBearer(r.Header.Get("Authorization"), token) {
			writeProblem(w, http.StatusUnauthorized, "unauthorized", "token tidak valid")
			return
		}
		limit := 100
		messageID := strings.TrimSpace(r.URL.Query().Get("message_id"))
		if len(messageID) > 256 {
			writeProblem(w, http.StatusBadRequest, "bad_request", "message_id is too long")
			return
		}
		if value := r.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > 1000 {
				writeProblem(w, http.StatusBadRequest, "bad_request", "limit must be between 1 and 1000")
				return
			}
			limit = parsed
		}
		if messageID != "" {
			limit = 1
		}
		if !replayMu.TryLock() {
			writeProblem(w, http.StatusConflict, "replay_in_progress", "a replay is already running on this instance")
			return
		}
		defer replayMu.Unlock()

		recovered := 0
		for recovered < limit {
			var message *store.UnprocessedMessage
			var err error
			if messageID != "" {
				message, err = destination.ClaimUnprocessedByID(r.Context(), string(domain.SourcePVMBG), messageID, owner, 30*time.Second, store.UnprocessedParked)
			} else {
				message, err = destination.ClaimUnprocessed(r.Context(), string(domain.SourcePVMBG), owner, 30*time.Second, store.UnprocessedParked)
			}
			if err != nil {
				writeProblem(w, http.StatusServiceUnavailable, "unavailable", err.Error())
				return
			}
			if message == nil {
				if messageID != "" {
					writeProblem(w, http.StatusNotFound, "not_found", "message_id was not found in parked PVMBG messages")
					return
				}
				break
			}
			report, err := pvmbg.DecodeReport(message.RawPayload, compatibilityVersion)
			if err == nil {
				var event domain.HazardEvent
				event, err = mapper.MapVolcanicReport(report)
				if err == nil {
					_, err = destination.ImportBatch(r.Context(), []domain.HazardEvent{event})
				}
			}
			if err != nil {
				_ = destination.ReleaseUnprocessed(r.Context(), message.MessageID, owner, "replay_failed: "+err.Error())
				logger.WarnContext(r.Context(), "parked message replay failed", "message_id", message.MessageID, "error", err.Error())
				core.WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{"recovered": recovered, "failed_message_id": message.MessageID, "error": err.Error()})
				return
			}
			if err := destination.ResolveUnprocessed(r.Context(), message.MessageID, owner); err != nil {
				writeProblem(w, http.StatusServiceUnavailable, "unavailable", err.Error())
				return
			}
			recovered++
		}
		response := map[string]any{"source": domain.SourcePVMBG, "recovered": recovered, "limit": limit}
		if messageID != "" {
			response["message_id"] = messageID
		}
		core.WriteJSON(w, http.StatusOK, response)
	})
}
