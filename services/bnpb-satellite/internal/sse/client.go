package sse

import (
	"bufio"
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

const maxEventBytes = 4 << 20

// Client reads the Feed Satelit event stream. It does not reconnect on its own;
// the caller decides the backoff and calls Stream again with the last offset.
type Client struct {
	baseURL *url.URL
	token   string
	http    *http.Client
}

func NewClient(baseURL, token string) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid satellite base URL %q", baseURL)
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("satellite token is required")
	}
	return &Client{
		baseURL: parsed,
		token:   token,
		http:    &http.Client{},
	}, nil
}

// Stream opens /v1/stream and calls handler for every event. It returns nil when
// the server closes the stream and an error when the request fails or handler
// stops the stream. `from` is sent as both query parameter and Last-Event-ID.
func (c *Client) Stream(ctx context.Context, from string, handler func(Event) error) error {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/v1/stream"
	query := endpoint.Query()
	if from != "" {
		query.Set("from", from)
	}
	endpoint.RawQuery = query.Encode()
	endpoint.Fragment = ""

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create stream request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "text/event-stream")
	if from != "" {
		request.Header.Set("Last-Event-ID", from)
	}
	if correlationID := core.CorrelationID(ctx); correlationID != "-" {
		request.Header.Set(core.CorrelationHeader, correlationID)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return &TransportError{Err: err}
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return decodeAPIError(response, body)
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), maxEventBytes)

	var event Event
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if data.Len() > 0 {
				event.Data = []byte(data.String())
				if err := handler(event); err != nil {
					return err
				}
			}
			event = Event{}
			data.Reset()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event.Type = value
		case "data":
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		case "id":
			event.ID = value
		case "retry":
			// The caller owns retry policy. Ignore the server hint.
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stream: %w", err)
	}
	if data.Len() > 0 {
		event.Data = []byte(data.String())
		if err := handler(event); err != nil {
			return err
		}
	}
	return nil
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
