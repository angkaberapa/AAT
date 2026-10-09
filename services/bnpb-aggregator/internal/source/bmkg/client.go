package bmkg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/ingest"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/mapping"
)

const maxResponseBytes = 16 << 20

type HTTPClient interface {
	Do(req *http.Request, target string) (*http.Response, error)
}

type Client struct {
	baseURL    *url.URL
	apiKey     string
	httpClient HTTPClient
	mapper     *mapping.Mapper
	logger     *slog.Logger
	eventSince          *time.Time
	warningSince        *time.Time
	pendingEventSince   *time.Time
	pendingWarningSince *time.Time
	initialized         bool
	events              map[string]mapping.SeismicEvent
	warnings            map[string]mapping.TsunamiWarning
	dirty               map[string]struct{}
	pendingDirty        []string
}

var _ ingest.SourceClient = (*Client)(nil)

func NewClient(
	baseURL, apiKey string,
	httpClient HTTPClient,
	mapper *mapping.Mapper,
	loggers ...*slog.Logger,
) (*Client, error) {
	parsedBaseURL, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsedBaseURL.Scheme == "" || parsedBaseURL.Host == "" {
		return nil, fmt.Errorf("invalid BMKG base URL %q", baseURL)
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("BMKG API key is required")
	}
	if httpClient == nil {
		return nil, errors.New("BMKG HTTP client is required")
	}
	if mapper == nil {
		return nil, errors.New("BMKG mapper is required")
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return &Client{
		baseURL:    parsedBaseURL,
		apiKey:     apiKey,
		httpClient: httpClient,
		mapper:     mapper,
		logger:     logger,
		events:     make(map[string]mapping.SeismicEvent),
		warnings:   make(map[string]mapping.TsunamiWarning),
		dirty:      make(map[string]struct{}),
	}, nil
}

func (client *Client) Source() domain.Source {
	return domain.SourceBMKG
}

func (client *Client) Fetch(
	ctx context.Context,
	since *time.Time,
) ([]domain.HazardEvent, error) {
	eventSince := cloneTime(client.eventSince)
	if !client.initialized {
		eventSince = cloneTime(since)
	}
	warningSince := cloneTime(client.warningSince)
	if !client.initialized {
		warningSince = cloneTime(since)
	}

	rawEvents, err := client.fetchRaw(ctx, "/seismic-events", eventSince)
	if err != nil {
		return nil, err
	}
	rawWarnings, err := client.fetchRaw(ctx, "/tsunami-warnings", warningSince)
	if err != nil {
		return nil, err
	}

	latestEvent := cloneTime(eventSince)
	for index, raw := range rawEvents {
		latestEvent = laterCheckpoint(latestEvent, timestampField(raw, "occurred_at"))

		var event mapping.SeismicEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			client.logSkipped(ctx, "seismic_event", index, err)
			continue
		}
		if _, err := client.mapper.MapSeismicEvent(event, nil); err != nil {
			client.logSkipped(ctx, "seismic_event", index, err)
			continue
		}
		client.events[event.EventID] = event
		client.dirty[event.EventID] = struct{}{}
	}

	latestWarning := cloneTime(warningSince)
	for index, raw := range rawWarnings {
		latestWarning = laterCheckpoint(latestWarning, timestampField(raw, "issued_at"))

		var warning mapping.TsunamiWarning
		if err := json.Unmarshal(raw, &warning); err != nil {
			client.logSkipped(ctx, "tsunami_warning", index, err)
			continue
		}
		if _, ok := warningPriority(warning.ThreatLevel); !ok {
			client.logSkipped(ctx, "tsunami_warning", index,
				fmt.Errorf("unknown tsunami threat_level %q", warning.ThreatLevel))
			continue
		}
		current, exists := client.warnings[warning.RelatedEventID]
		if !exists || strongerWarning(warning, current) {
			client.warnings[warning.RelatedEventID] = warning
			client.dirty[warning.RelatedEventID] = struct{}{}
		}
	}

	ids := make([]string, 0, len(client.dirty))
	for eventID := range client.dirty {
		if _, exists := client.events[eventID]; exists {
			ids = append(ids, eventID)
		}
	}
	sort.Strings(ids)

	mapped := make([]domain.HazardEvent, 0, len(ids))
	for _, eventID := range ids {
		event := client.events[eventID]
		var warning *mapping.TsunamiWarning
		if related, ok := client.warnings[eventID]; ok {
			warningCopy := related
			warning = &warningCopy
		}
		hazard, err := client.mapper.MapSeismicEvent(event, warning)
		if err != nil {
			client.logger.WarnContext(ctx, "skipping invalid cached BMKG event",
				"source", domain.SourceBMKG,
				"event_id", eventID,
				"error", err.Error())
			delete(client.dirty, eventID)
			continue
		}
		mapped = append(mapped, hazard)
	}

	client.pendingEventSince = latestEvent
	client.pendingWarningSince = latestWarning
	client.pendingDirty = ids
	return mapped, nil
}

func (client *Client) CommitFetch() {
	client.eventSince = cloneTime(client.pendingEventSince)
	client.warningSince = cloneTime(client.pendingWarningSince)
	client.initialized = true
	for _, eventID := range client.pendingDirty {
		delete(client.dirty, eventID)
	}
	client.pendingEventSince = nil
	client.pendingWarningSince = nil
	client.pendingDirty = nil
}

type rawEnvelope struct {
	Data []json.RawMessage `json:"data"`
}

func (client *Client) fetchRaw(
	ctx context.Context,
	path string,
	since *time.Time,
) ([]json.RawMessage, error) {
	endpoint := *client.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	if since != nil {
		query := endpoint.Query()
		query.Set("since", since.UTC().Format(time.RFC3339Nano))
		endpoint.RawQuery = query.Encode()
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create BMKG request: %w", err)
	}
	request.Header.Set("X-BMKG-Key", client.apiKey)
	request.Header.Set("Accept", "application/json")

	response, err := client.httpClient.Do(request, "bmkg")
	if err != nil {
		return nil, &ingest.SourceError{
			Source: domain.SourceBMKG,
			Kind:   ingest.ErrorUnavailable,
			Err:    err,
		}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, responseError(response)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, invalidResponse(fmt.Errorf("read BMKG response: %w", err))
	}
	if len(body) > maxResponseBytes {
		return nil, invalidResponse(fmt.Errorf("BMKG response exceeds %d-byte limit", maxResponseBytes))
	}
	var envelope rawEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, invalidResponse(fmt.Errorf("decode BMKG response: %w", err))
	}
	return envelope.Data, nil
}

func (client *Client) logSkipped(ctx context.Context, recordType string, index int, err error) {
	client.logger.WarnContext(ctx, "skipping invalid BMKG source record",
		"source", domain.SourceBMKG,
		"record_type", recordType,
		"record_index", index,
		"error", err.Error())
}

func timestampField(raw json.RawMessage, field string) *time.Time {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil
	}
	var value time.Time
	if err := json.Unmarshal(fields[field], &value); err != nil || value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}

func laterCheckpoint(left, right *time.Time) *time.Time {
	if left == nil {
		return cloneTime(right)
	}
	if right != nil && right.After(*left) {
		return cloneTime(right)
	}
	return cloneTime(left)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func warningPriority(level string) (int, bool) {
	switch level {
	case "Waspada":
		return 1, true
	case "Siaga":
		return 2, true
	case "Awas":
		return 3, true
	default:
		return 0, false
	}
}

func strongerWarning(candidate, current mapping.TsunamiWarning) bool {
	candidatePriority, _ := warningPriority(candidate.ThreatLevel)
	currentPriority, _ := warningPriority(current.ThreatLevel)
	return candidatePriority > currentPriority ||
		(candidatePriority == currentPriority && candidate.IssuedAt.After(current.IssuedAt))
}

func responseError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		detail = response.Status
	}
	kind := ingest.ErrorInvalidResponse
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		kind = ingest.ErrorUnauthorized
	} else if response.StatusCode >= 500 {
		kind = ingest.ErrorUnavailable
	}
	return &ingest.SourceError{
		Source:     domain.SourceBMKG,
		Kind:       kind,
		StatusCode: response.StatusCode,
		Err:        errors.New(detail),
	}
}

func invalidResponse(err error) error {
	return &ingest.SourceError{
		Source: domain.SourceBMKG,
		Kind:   ingest.ErrorInvalidResponse,
		Err:    err,
	}
}
