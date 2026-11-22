package pvmbg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/ingest"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/mapping"
)

const (
	maxResponseBytes         = 16 << 20
	maxCapturedResponseBytes = 32 << 20
	maxJSONDepth             = 100
)

type HTTPClient interface {
	Do(req *http.Request, target string) (*http.Response, error)
}

type Client struct {
	baseURL              *url.URL
	token                string
	httpClient           HTTPClient
	mapper               *mapping.Mapper
	logger               *slog.Logger
	since                *time.Time
	pendingSince         *time.Time
	initialized          bool
	failures             ingest.FailureSink
	compatibilityVersion int
}

func (client *Client) SetFailureSink(sink ingest.FailureSink) { client.failures = sink }
func (client *Client) SetCompatibilityVersion(version int)    { client.compatibilityVersion = version }

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
		baseURL:              parsedBaseURL,
		token:                token,
		httpClient:           httpClient,
		mapper:               mapper,
		logger:               logger,
		compatibilityVersion: 2,
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

	body, err := io.ReadAll(io.LimitReader(response.Body, maxCapturedResponseBytes+1))
	if err != nil {
		return nil, invalidResponse(fmt.Errorf("read PVMBG response: %w", err))
	}
	if len(body) > maxCapturedResponseBytes {
		return nil, invalidResponse(fmt.Errorf("PVMBG response exceeds absolute %d-byte capture limit", maxCapturedResponseBytes))
	}
	if len(body) > maxResponseBytes {
		return nil, client.parkResponse(ctx, body, "payload_too_large: PVMBG response exceeds processing limit")
	}
	if !utf8.Valid(body) {
		return nil, client.parkResponse(ctx, body, "invalid_utf8: PVMBG response is not valid UTF-8")
	}
	depth, structurallyComplete := jsonDepth(body)
	if !structurallyComplete {
		return nil, client.parkResponse(ctx, body, "invalid_json: incomplete or unbalanced JSON structure")
	}
	if depth > maxJSONDepth {
		return nil, client.parkResponse(ctx, body, fmt.Sprintf("json_too_deep: depth exceeds %d", maxJSONDepth))
	}
	var envelope struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, client.parkResponse(ctx, body, "invalid_json: "+err.Error())
	}

	events := make([]domain.HazardEvent, 0, len(envelope.Data))
	latest := cloneTime(since)
	for index, raw := range envelope.Data {
		latest = laterCheckpoint(latest, timestampField(raw, "reported_at"))

		report, err := DecodeReport(raw, client.compatibilityVersion)
		if err != nil {
			if persistErr := client.parkRecord(ctx, raw, "schema_incompatible: "+err.Error()); persistErr != nil {
				return nil, persistErr
			}
			client.logSkipped(ctx, index, err)
			continue
		}
		event, err := client.mapper.MapVolcanicReport(report)
		if err != nil {
			if persistErr := client.parkRecord(ctx, raw, "schema_incompatible: "+err.Error()); persistErr != nil {
				return nil, persistErr
			}
			client.logSkipped(ctx, index, err)
			continue
		}
		events = append(events, event)
	}
	client.pendingSince = latest
	return events, nil
}

func DecodeReport(raw []byte, compatibilityVersion int) (mapping.VolcanicReport, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return mapping.VolcanicReport{}, err
	}
	if compatibilityVersion >= 3 {
		if _, exists := fields["alert_level"]; !exists {
			if renamed, found := fields["level"]; found {
				fields["alert_level"] = renamed
				delete(fields, "level")
			}
		}
	}
	if compatibilityVersion >= 4 {
		if encoded, found := fields["eruption_count_24h"]; found {
			var text string
			if json.Unmarshal(encoded, &text) == nil {
				value, err := strconv.Atoi(text)
				if err != nil {
					return mapping.VolcanicReport{}, fmt.Errorf("eruption_count_24h: %w", err)
				}
				fields["eruption_count_24h"], _ = json.Marshal(value)
			}
		}
	}
	if compatibilityVersion >= 5 {
		if _, exists := fields["eruption_count_24h"]; !exists {
			fields["eruption_count_24h"] = json.RawMessage("0")
		}
	}
	normalized, err := json.Marshal(fields)
	if err != nil {
		return mapping.VolcanicReport{}, err
	}
	var report mapping.VolcanicReport
	if err := json.Unmarshal(normalized, &report); err != nil {
		return mapping.VolcanicReport{}, err
	}
	return report, nil
}

func (client *Client) parkResponse(ctx context.Context, raw []byte, reason string) error {
	if err := client.persistFailure(ctx, raw, "", reason, "application/json"); err != nil {
		return invalidResponse(fmt.Errorf("persist invalid PVMBG response: %w", err))
	}
	return invalidResponse(errors.New(reason))
}

func (client *Client) parkRecord(ctx context.Context, raw []byte, reason string) error {
	return client.persistFailure(ctx, raw, sourceRefField(raw), reason, "application/vnd.pvmbg.volcanic-report+json")
}

func (client *Client) persistFailure(ctx context.Context, raw []byte, sourceRefID, reason, contentType string) error {
	if client.failures == nil {
		return errors.New("PVMBG failure sink is not configured")
	}
	digest := sha256.Sum256(append([]byte("PVMBG:"), raw...))
	return client.failures.StoreRawFailure(ctx, ingest.RawFailure{
		MessageID: "msg-" + hex.EncodeToString(digest[:16]), Source: domain.SourcePVMBG,
		SourceRefID: sourceRefID, ReceivedAt: time.Now().UTC(), Reason: reason,
		Retryable: false, Status: "parked", RawPayload: append([]byte(nil), raw...), ContentType: contentType,
	})
}

func sourceRefField(raw []byte) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return ""
	}
	var value string
	_ = json.Unmarshal(fields["report_id"], &value)
	return value
}

func jsonDepth(raw []byte) (int, bool) {
	depth, maximum, inString, escaped := 0, 0, false, false
	for _, char := range raw {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
			} else if char == '"' {
				inString = false
			}
			continue
		}
		if char == '"' {
			inString = true
			continue
		}
		switch char {
		case '{', '[':
			depth++
			if depth > maximum {
				maximum = depth
			}
		case '}', ']':
			depth--
			if depth < 0 {
				return maximum, false
			}
		}
	}
	return maximum, depth == 0 && !inString
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
