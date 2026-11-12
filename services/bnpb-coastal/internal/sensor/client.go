package sensor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/core"
)

const maxBodyBytes = 8 << 20

// HTTPClient reads pages from the Sensor Pantai observations endpoint.
type HTTPClient struct {
	baseURL *url.URL
	token   string
	http    *http.Client
}

func NewHTTPClient(baseURL, token string, timeout time.Duration) (*HTTPClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid sensor base URL %q", baseURL)
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("sensor token is required")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("sensor timeout must be positive")
	}
	return &HTTPClient{
		baseURL: parsed,
		token:   token,
		http:    &http.Client{Timeout: timeout},
	}, nil
}

func (c *HTTPClient) FetchPage(ctx context.Context, cursor string, limit int) (Page, error) {
	if limit < 1 || limit > 1000 {
		return Page{}, fmt.Errorf("sensor limit must be between 1 and 1000")
	}

	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/v1/observations"
	query := endpoint.Query()
	query.Set("limit", strconv.Itoa(limit))
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	endpoint.RawQuery = query.Encode()
	endpoint.Fragment = ""

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Page{}, fmt.Errorf("create sensor request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if correlationID := core.CorrelationID(ctx); correlationID != "-" {
		request.Header.Set(core.CorrelationHeader, correlationID)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return Page{}, &TransportError{Err: err}
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes))
	if err != nil {
		return Page{}, fmt.Errorf("read sensor response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Page{}, decodeAPIError(response, body)
	}

	var envelope struct {
		GeneratedAt string            `json:"generated_at"`
		Items       []json.RawMessage `json:"items"`
		NextCursor  string            `json:"next_cursor"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return Page{}, fmt.Errorf("decode sensor response: %w", err)
	}
	if envelope.GeneratedAt == "" {
		return Page{}, fmt.Errorf("sensor response is missing generated_at")
	}
	generatedAt, err := time.Parse(time.RFC3339, envelope.GeneratedAt)
	if err != nil {
		return Page{}, fmt.Errorf("parse sensor generated_at: %w", err)
	}
	if envelope.Items == nil {
		envelope.Items = []json.RawMessage{}
	}
	return Page{
		GeneratedAt: generatedAt.UTC(),
		Items:       envelope.Items,
		NextCursor:  envelope.NextCursor,
	}, nil
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
