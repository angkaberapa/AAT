package audit

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

const (
	defaultLimit = 100
	maxLimit     = 1000
	cursorKind   = "audit-events"
)

type Reader interface {
	AuditEvents(context.Context, store.AuditFilter) (store.AuditPage, error)
	AuditChecksum(context.Context, domain.Source) (store.ChecksumResult, error)
}

type Locator interface {
	Locate(context.Context, string) (*Location, error)
}

func Register(mux *http.ServeMux, logger *slog.Logger, reader Reader, locator Locator, token string) {
	authenticated := func(handler http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !validBearerToken(r.Header.Get("Authorization"), token) {
				w.Header().Set("WWW-Authenticate", "Bearer")
				writeError(w, http.StatusUnauthorized, "unauthorized", "token tidak valid")
				return
			}
			handler(w, r)
		}
	}

	mux.HandleFunc("GET /internal/audit/events", authenticated(func(w http.ResponseWriter, r *http.Request) {
		filter, problem := parseEventsQuery(r)
		if problem != "" {
			writeError(w, http.StatusBadRequest, "bad_request", problem)
			return
		}
		page, err := reader.AuditEvents(r.Context(), filter)
		if err != nil {
			logger.ErrorContext(r.Context(), "audit events failed", "error", err.Error())
			writeError(w, http.StatusServiceUnavailable, "unavailable", "audit storage is temporarily unavailable")
			return
		}
		core.WriteJSON(w, http.StatusOK, eventsResponse{
			Items:            page.Items,
			NextCursor:       encodeEventsCursor(page.Next, filter),
			Partial:          false,
			UnavailableUnits: []string{},
		})
	}))

	mux.HandleFunc("GET /internal/audit/checksum", authenticated(func(w http.ResponseWriter, r *http.Request) {
		source := domain.Source(r.URL.Query().Get("source"))
		if source == "" || !source.Valid() {
			writeError(w, http.StatusBadRequest, "bad_request", "source is required and must be a known HazardEvent source")
			return
		}
		result, err := reader.AuditChecksum(r.Context(), source)
		if err != nil {
			logger.ErrorContext(r.Context(), "audit checksum failed", "source", source, "error", err.Error())
			writeError(w, http.StatusServiceUnavailable, "unavailable", "audit storage is temporarily unavailable")
			return
		}
		core.WriteJSON(w, http.StatusOK, checksumResponse{
			Source:           source,
			Count:            result.Count,
			Checksum:         result.Checksum,
			Algorithm:        "sha256",
			AsOf:             result.AsOf.UTC(),
			Partial:          false,
			UnavailableUnits: []string{},
		})
	}))

	mux.HandleFunc("GET /internal/audit/location", authenticated(func(w http.ResponseWriter, r *http.Request) {
		hazardID := strings.TrimSpace(r.URL.Query().Get("hazard_id"))
		if hazardID == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "hazard_id is required")
			return
		}
		location, err := locator.Locate(r.Context(), hazardID)
		if err != nil {
			logger.ErrorContext(r.Context(), "audit location failed", "hazard_id", hazardID, "error", err.Error())
			writeError(w, http.StatusServiceUnavailable, "unavailable", "audit location is temporarily unavailable")
			return
		}
		if location == nil {
			writeError(w, http.StatusNotFound, "not_found", "hazard_id tidak ditemukan")
			return
		}
		core.WriteJSON(w, http.StatusOK, location)
	}))
}

type eventsResponse struct {
	Items            []domain.HazardEvent `json:"items"`
	NextCursor       *string              `json:"next_cursor"`
	Partial          bool                 `json:"partial"`
	UnavailableUnits []string             `json:"unavailable_units"`
}

type checksumResponse struct {
	Source           domain.Source `json:"source"`
	Count            int64         `json:"count"`
	Checksum         string        `json:"checksum"`
	Algorithm        string        `json:"algorithm"`
	AsOf             time.Time     `json:"as_of"`
	Partial          bool          `json:"partial"`
	UnavailableUnits []string      `json:"unavailable_units"`
}

type eventsCursorPayload struct {
	Version    int           `json:"v"`
	Kind       string        `json:"kind"`
	Source     domain.Source `json:"source,omitempty"`
	Since      *time.Time    `json:"since,omitempty"`
	IngestedAt time.Time     `json:"ingested_at"`
	HazardID   string        `json:"hazard_id"`
}

func parseEventsQuery(r *http.Request) (store.AuditFilter, string) {
	query := r.URL.Query()
	filter := store.AuditFilter{Limit: defaultLimit}

	if raw := query.Get("source"); raw != "" {
		filter.Source = domain.Source(raw)
		if !filter.Source.Valid() {
			return filter, "invalid source"
		}
	}
	if raw := query.Get("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || !strings.HasSuffix(raw, "Z") {
			return filter, "invalid since, expected ISO 8601 UTC"
		}
		parsed = parsed.UTC()
		filter.Since = &parsed
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxLimit {
			return filter, "invalid limit, expected an integer between 1 and 1000"
		}
		filter.Limit = limit
	}
	if raw := query.Get("cursor"); raw != "" {
		payload, err := decodeEventsCursor(raw)
		if err != nil {
			return filter, "invalid cursor"
		}
		if filter.Source != "" && filter.Source != payload.Source {
			return filter, "cursor does not match source"
		}
		if filter.Since != nil && !sameOptionalTime(filter.Since, payload.Since) {
			return filter, "cursor does not match since"
		}
		filter.Source = payload.Source
		filter.Since = payload.Since
		filter.Cursor = &store.AuditCursor{IngestedAt: payload.IngestedAt, HazardID: payload.HazardID}
	}
	return filter, ""
}

func encodeEventsCursor(cursor *store.AuditCursor, filter store.AuditFilter) *string {
	if cursor == nil {
		return nil
	}
	payload, err := json.Marshal(eventsCursorPayload{
		Version:    1,
		Kind:       cursorKind,
		Source:     filter.Source,
		Since:      filter.Since,
		IngestedAt: cursor.IngestedAt.UTC(),
		HazardID:   cursor.HazardID,
	})
	if err != nil {
		return nil
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return &encoded
}

func decodeEventsCursor(raw string) (eventsCursorPayload, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return eventsCursorPayload{}, err
	}
	var payload eventsCursorPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return eventsCursorPayload{}, err
	}
	if payload.Version != 1 || payload.Kind != cursorKind || payload.IngestedAt.IsZero() || payload.HazardID == "" {
		return eventsCursorPayload{}, errors.New("unsupported or incomplete cursor")
	}
	if payload.Source != "" && !payload.Source.Valid() {
		return eventsCursorPayload{}, errors.New("cursor contains an invalid source")
	}
	payload.IngestedAt = payload.IngestedAt.UTC()
	if payload.Since != nil {
		normalized := payload.Since.UTC()
		payload.Since = &normalized
	}
	return payload, nil
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func validBearerToken(header, expected string) bool {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(parts[1]), []byte(expected)) == 1
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	core.WriteJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
