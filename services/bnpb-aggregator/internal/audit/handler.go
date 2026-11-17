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
	Analytics(context.Context, store.AnalyticsFilter) (store.AnalyticsResult, error)
	AuditUnprocessed(context.Context, store.UnprocessedFilter) (store.UnprocessedPage, error)
	GetUnprocessed(context.Context, string) (*store.UnprocessedMessage, error)
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
			Partial:          page.Partial,
			UnavailableUnits: nonNilStrings(page.UnavailableUnits),
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
			Partial:          result.Partial,
			UnavailableUnits: nonNilStrings(result.UnavailableUnits),
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

	mux.HandleFunc("GET /internal/analytics/data", authenticated(func(w http.ResponseWriter, r *http.Request) {
		filter, problem := parseAnalyticsQuery(r)
		if problem != "" {
			writeError(w, http.StatusBadRequest, "bad_request", problem)
			return
		}
		result, err := reader.Analytics(r.Context(), filter)
		if err != nil {
			logger.ErrorContext(r.Context(), "canonical analytics failed", "error", err.Error())
			writeError(w, http.StatusServiceUnavailable, "unavailable", "analytics storage is temporarily unavailable")
			return
		}
		core.WriteJSON(w, http.StatusOK, analyticsResponse{
			From:             filter.From,
			To:               filter.To,
			Bucket:           filter.Bucket,
			Total:            result.Total,
			TimeSeries:       result.TimeSeries,
			BySeverity:       result.BySeverity,
			ByArea:           result.ByArea,
			AsOf:             time.Now().UTC(),
			Partial:          result.Partial,
			Degraded:         result.Degraded,
			UnavailableUnits: nonNilStrings(result.UnavailableUnits),
		})
	}))

	mux.HandleFunc("GET /internal/audit/unprocessed", authenticated(func(w http.ResponseWriter, r *http.Request) {
		filter, problem := parseUnprocessedQuery(r)
		if problem != "" {
			writeError(w, http.StatusBadRequest, "bad_request", problem)
			return
		}
		page, err := reader.AuditUnprocessed(r.Context(), filter)
		if err != nil {
			logger.ErrorContext(r.Context(), "audit unprocessed failed", "error", err.Error())
			writeError(w, http.StatusServiceUnavailable, "unavailable", "unprocessed storage is temporarily unavailable")
			return
		}
		items := make([]unprocessedItem, 0, len(page.Items))
		for _, item := range page.Items {
			items = append(items, unprocessedItem{
				MessageID: item.MessageID, Source: item.Source, ReceivedAt: item.ReceivedAt.UTC(),
				Reason: item.Reason, Retryable: item.Retryable, Attempts: item.Attempts, Status: item.Status,
			})
		}
		core.WriteJSON(w, http.StatusOK, unprocessedResponse{Items: items, NextCursor: encodeUnprocessedCursor(page.Next, filter)})
	}))

	mux.HandleFunc("GET /internal/audit/unprocessed/{message_id}", authenticated(func(w http.ResponseWriter, r *http.Request) {
		message, err := reader.GetUnprocessed(r.Context(), r.PathValue("message_id"))
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "unavailable", "unprocessed storage is temporarily unavailable")
			return
		}
		if message == nil {
			writeError(w, http.StatusNotFound, "not_found", "message_id tidak ditemukan")
			return
		}
		core.WriteJSON(w, http.StatusOK, map[string]any{
			"message_id": message.MessageID, "source": message.Source, "source_ref_id": message.SourceRefID,
			"received_at": message.ReceivedAt.UTC(), "reason": message.Reason, "retryable": message.Retryable,
			"attempts": message.Attempts, "status": message.Status, "content_type": message.ContentType,
			"raw_payload_base64": base64.StdEncoding.EncodeToString(message.RawPayload),
		})
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

type analyticsResponse struct {
	From             *time.Time         `json:"from"`
	To               *time.Time         `json:"to"`
	Bucket           string             `json:"bucket"`
	Total            int64              `json:"total"`
	TimeSeries       []store.TimeCount  `json:"time_series"`
	BySeverity       []store.NamedCount `json:"by_severity"`
	ByArea           []store.NamedCount `json:"by_area"`
	AsOf             time.Time          `json:"as_of"`
	Partial          bool               `json:"partial"`
	Degraded         bool               `json:"degraded"`
	UnavailableUnits []string           `json:"unavailable_units"`
}

type unprocessedItem struct {
	MessageID  string    `json:"message_id"`
	Source     string    `json:"source"`
	ReceivedAt time.Time `json:"received_at"`
	Reason     string    `json:"reason"`
	Retryable  bool      `json:"retryable"`
	Attempts   int       `json:"attempts"`
	Status     string    `json:"status"`
}

type unprocessedResponse struct {
	Items      []unprocessedItem `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

type unprocessedCursorPayload struct {
	Version    int       `json:"v"`
	Kind       string    `json:"kind"`
	Source     string    `json:"source,omitempty"`
	ReceivedAt time.Time `json:"received_at"`
	MessageID  string    `json:"message_id"`
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

func parseAnalyticsQuery(r *http.Request) (store.AnalyticsFilter, string) {
	query := r.URL.Query()
	filter := store.AnalyticsFilter{Bucket: query.Get("bucket")}
	if filter.Bucket == "" {
		filter.Bucket = "day"
	}
	if filter.Bucket != "hour" && filter.Bucket != "day" {
		return filter, "invalid bucket, expected hour or day"
	}
	for key, target := range map[string]**time.Time{"from": &filter.From, "to": &filter.To} {
		raw := query.Get(key)
		if raw == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || !strings.HasSuffix(raw, "Z") {
			return filter, "invalid " + key + ", expected ISO 8601 UTC"
		}
		normalized := parsed.UTC()
		*target = &normalized
	}
	if filter.From != nil && filter.To != nil && filter.From.After(*filter.To) {
		return filter, "from must not be later than to"
	}
	return filter, ""
}

func parseUnprocessedQuery(r *http.Request) (store.UnprocessedFilter, string) {
	query := r.URL.Query()
	filter := store.UnprocessedFilter{Limit: defaultLimit, Source: query.Get("source")}
	if filter.Source != "" && !domain.Source(filter.Source).Valid() {
		return filter, "invalid source"
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxLimit {
			return filter, "invalid limit"
		}
		filter.Limit = limit
	}
	if raw := query.Get("cursor"); raw != "" {
		payload, err := decodeUnprocessedCursor(raw)
		if err != nil || payload.Source != filter.Source {
			return filter, "invalid cursor or cursor filters do not match"
		}
		filter.Cursor = &store.UnprocessedCursor{ReceivedAt: payload.ReceivedAt, MessageID: payload.MessageID}
	}
	return filter, ""
}

func encodeUnprocessedCursor(cursor *store.UnprocessedCursor, filter store.UnprocessedFilter) *string {
	if cursor == nil {
		return nil
	}
	raw, err := json.Marshal(unprocessedCursorPayload{
		Version: 1, Kind: "audit-unprocessed", Source: filter.Source,
		ReceivedAt: cursor.ReceivedAt.UTC(), MessageID: cursor.MessageID,
	})
	if err != nil {
		return nil
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return &encoded
}

func decodeUnprocessedCursor(raw string) (unprocessedCursorPayload, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return unprocessedCursorPayload{}, err
	}
	var payload unprocessedCursorPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return payload, err
	}
	if payload.Version != 1 || payload.Kind != "audit-unprocessed" || payload.ReceivedAt.IsZero() || payload.MessageID == "" {
		return payload, errors.New("unsupported or incomplete cursor")
	}
	payload.ReceivedAt = payload.ReceivedAt.UTC()
	return payload, nil
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

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	core.WriteJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
