// Package sse implements the Feed Satelit Server-Sent Events client.
package sse

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Event is one dispatched SSE frame.
type Event struct {
	ID   string
	Type string
	Data []byte
}

type TransportError struct {
	Err error
}

func (e *TransportError) Error() string {
	return "sse transport: " + e.Err.Error()
}

func (e *TransportError) Unwrap() error {
	return e.Err
}

type APIError struct {
	StatusCode int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("sse: status %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("sse: status %d: %s: %s", e.StatusCode, e.Code, e.Message)
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
