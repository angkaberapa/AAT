package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/core"
)

const maxResponseBytes = 1 << 20

// HTTPClient sends commands to the Aggregator Internal Ingest API.
type HTTPClient struct {
	baseURL *url.URL
	token   string
	http    *http.Client
}

func NewHTTPClient(baseURL, token string, timeout time.Duration) (*HTTPClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid aggregator base URL %q", baseURL)
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("internal ingest token is required")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("internal ingest timeout must be positive")
	}
	return &HTTPClient{
		baseURL: parsed,
		token:   token,
		http:    &http.Client{Timeout: timeout},
	}, nil
}

func (c *HTTPClient) UpsertHazard(ctx context.Context, command UpsertHazardCommand) (IngestResult, error) {
	if command.Operation == "" {
		command.Operation = OperationUpsert
	}
	if command.Operation != OperationUpsert {
		return IngestResult{}, fmt.Errorf("upsert hazard: operation must be %s", OperationUpsert)
	}
	if err := requireCommandID(command.Producer, command.ProducerEventID); err != nil {
		return IngestResult{}, fmt.Errorf("upsert hazard: %w", err)
	}

	var result IngestResult
	err := c.doJSON(ctx, "/internal/ingest/hazards", command.ProducerEventID, command, &result)
	return result, err
}

func (c *HTTPClient) DeleteHazard(ctx context.Context, command DeleteHazardCommand) (IngestResult, error) {
	if command.Operation == "" {
		command.Operation = OperationDelete
	}
	if command.Operation != OperationDelete {
		return IngestResult{}, fmt.Errorf("delete hazard: operation must be %s", OperationDelete)
	}
	if err := requireCommandID(command.Producer, command.ProducerEventID); err != nil {
		return IngestResult{}, fmt.Errorf("delete hazard: %w", err)
	}
	if command.HazardID == "" || command.SourceRefID == "" {
		return IngestResult{}, fmt.Errorf("delete hazard: hazard_id and source_ref_id are required")
	}

	var result IngestResult
	err := c.doJSON(ctx, "/internal/ingest/hazards", command.ProducerEventID, command, &result)
	return result, err
}

func (c *HTTPClient) ReportSourceStatus(ctx context.Context, status SourceStatus) error {
	if status.Source == "" || status.Status == "" {
		return fmt.Errorf("source status: source and status are required")
	}
	return c.doJSON(ctx, "/internal/ingest/source-status", "", status, nil)
}

func (c *HTTPClient) StoreUnprocessed(ctx context.Context, message UnprocessedMessage) error {
	if message.MessageID == "" {
		return fmt.Errorf("unprocessed: message_id is required")
	}
	if message.Source == "" || message.Status == "" {
		return fmt.Errorf("unprocessed: source and status are required")
	}
	return c.doJSON(ctx, "/internal/ingest/unprocessed", message.MessageID, message, nil)
}

func requireCommandID(producer, eventID string) error {
	if strings.TrimSpace(producer) == "" {
		return fmt.Errorf("producer is required")
	}
	if strings.TrimSpace(eventID) == "" {
		return fmt.Errorf("producer_event_id is required")
	}
	return nil
}

func (c *HTTPClient) doJSON(ctx context.Context, path, idempotencyKey string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawQuery = ""
	endpoint.Fragment = ""

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if correlationID := core.CorrelationID(ctx); correlationID != "-" {
		request.Header.Set(core.CorrelationHeader, correlationID)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return &TransportError{Err: err}
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read internal ingest response: %w", err)
	}

	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if out == nil || len(bytes.TrimSpace(responseBody)) == 0 {
			return nil
		}
		if err := json.Unmarshal(responseBody, out); err != nil {
			return fmt.Errorf("decode internal ingest response: %w", err)
		}
		return nil
	}

	return decodeAPIError(response, responseBody)
}

func decodeAPIError(response *http.Response, body []byte) error {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &envelope)

	message := strings.TrimSpace(envelope.Error.Message)
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}

	return &APIError{
		StatusCode: response.StatusCode,
		Code:       envelope.Error.Code,
		Message:    message,
		RetryAfter: parseRetryAfter(response.Header.Get("Retry-After")),
	}
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		delay := time.Until(when)
		if delay < 0 {
			return 0
		}
		return delay
	}
	return 0
}
