// Package sensor implements the pull client for the Sensor Pantai feed.
package sensor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Page is one response from GET /v1/observations. Items stay raw so a single
// malformed observation can be quarantined without failing the whole page.
type Page struct {
	GeneratedAt time.Time
	Items       []json.RawMessage
	NextCursor  string
}

// TransportError is a network failure before a response was received.
type TransportError struct {
	Err error
}

func (e *TransportError) Error() string {
	return "sensor transport: " + e.Err.Error()
}

func (e *TransportError) Unwrap() error {
	return e.Err
}

// APIError is a non-2xx response from the sensor.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("sensor: status %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("sensor: status %d: %s: %s", e.StatusCode, e.Code, e.Message)
}

func (e *APIError) Retryable() bool {
	return e.StatusCode == 429 || e.StatusCode >= 500
}

func IsRetryable(err error) bool {
	var transportErr *TransportError
	if errors.As(err, &transportErr) {
		return !errors.Is(transportErr.Err, context.Canceled)
	}
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Retryable()
}
