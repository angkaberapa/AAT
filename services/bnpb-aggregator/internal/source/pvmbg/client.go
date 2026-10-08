package pvmbg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
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
	baseURL      *url.URL
	token        string
	httpClient   HTTPClient
	mapper       *mapping.Mapper
	logger       *slog.Logger
	since        *time.Time
	pendingSince *time.Time
	initialized  bool
}

var _ ingest.SourceClient = (*Client)(nil)

func NewClient(
	baseURL, token string,
	httpClient HTTPClient,
	mapper *mapping.Mapper,
	loggers ...*slog.Logger,
) (*Client, error) {
	parsedBaseURL, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsedBaseURL.Scheme == "" || parsedBaseURL.Host == "" {
		return nil, fmt.Errorf("invalid PVMBG base URL %q", baseURL)
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("PVMBG token is required")
	}
	if httpClient == nil {
		return nil, errors.New("PVMBG HTTP client is required")
	}
	if mapper == nil {
		return nil, errors.New("PVMBG mapper is required")
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return &Client{
		baseURL:    parsedBaseURL,
		token:      token,
		httpClient: httpClient,
		mapper:     mapper,
		logger:     logger,
	}, nil
}

func (client *Client) Source() domain.Source {
	return domain.SourcePVMBG
}

func (client *Client) Fetch(
	ctx context.Context,
	since *time.Time,
) ([]domain.HazardEvent, error) {
	if client.initialized {
		since = cloneTime(client.since)
	} else {
		since = cloneTime(since)
	}
	endpoint := *client.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/volcanic-reports"
	if since != nil {
		query := endpoint.Query()
		query.Set("since", since.UTC().Format(time.RFC3339Nano))
		endpoint.RawQuery = query.Encode()
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create PVMBG request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Accept", "application/json")

	response, err := client.httpClient.Do(request, "pvmbg")
	if err != nil {
		return nil, &ingest.SourceError{
			Source: domain.SourcePVMBG,
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
		return nil, invalidResponse(fmt.Errorf("read PVMBG response: %w", err))
	}
	if len(body) > maxResponseBytes {
		return nil, invalidResponse(fmt.Errorf("PVMBG response exceeds %d-byte limit", maxResponseBytes))
	}
	var envelope struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, invalidResponse(fmt.Errorf("decode PVMBG response: %w", err))
	}

	events := make([]domain.HazardEvent, 0, len(envelope.Data))
	latest := cloneTime(since)
	for index, raw := range envelope.Data {
		latest = laterCheckpoint(latest, timestampField(raw, "reported_at"))

		var report mapping.VolcanicReport
		if err := json.Unmarshal(raw, &report); err != nil {
			client.logSkipped(ctx, index, err)
			continue
		}
		event, err := client.mapper.MapVolcanicReport(report)
		if err != nil {
			client.logSkipped(ctx, index, err)
			continue
		}
		events = append(events, event)
	}
	client.pendingSince = latest
	return events, nil
}

// CommitFetch advances past every source record observed in the previous
// response, including malformed records whose reported_at could still be read.
func (client *Client) CommitFetch() {
	client.since = cloneTime(client.pendingSince)
	client.initialized = true
	client.pendingSince = nil
}

func (client *Client) logSkipped(ctx context.Context, index int, err error) {
	client.logger.WarnContext(ctx, "skipping invalid PVMBG source record",
		"source", domain.SourcePVMBG,
		"record_type", "volcanic_report",
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
		Source:     domain.SourcePVMBG,
		Kind:       kind,
		StatusCode: response.StatusCode,
		Err:        errors.New(detail),
	}
}

func invalidResponse(err error) error {
	return &ingest.SourceError{
		Source: domain.SourcePVMBG,
		Kind:   ingest.ErrorInvalidResponse,
		Err:    err,
	}
}
