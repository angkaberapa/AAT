// Package ingest implements the client side of the Internal Ingest API
// defined in docs/KONTRAK_INTERNAL_INGEST.md.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	OperationUpsert = "UPSERT"
	OperationDelete = "DELETE"

	StatusRetrying = "retrying"
	StatusParked   = "parked"
)

// HazardEvent is the canonical event shape sent to the Aggregator. The producer
// does not send ingested_at; the Aggregator fills it with server time.
type HazardEvent struct {
	HazardID    string         `json:"hazard_id"`
	Source      string         `json:"source"`
	SourceRefID string         `json:"source_ref_id"`
	HazardType  string         `json:"hazard_type"`
	Severity    string         `json:"severity"`
	AreaName    string         `json:"area_name"`
	Latitude    float64        `json:"latitude"`
	Longitude   float64        `json:"longitude"`
	OccurredAt  time.Time      `json:"occurred_at"`
	Attributes  map[string]any `json:"attributes"`
}

// SourcePosition is the producer's durable position in its source. Satellite
// uses sse_offset and Coastal uses coastal_cursor.
type SourcePosition struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type UpsertHazardCommand struct {
	Operation       string         `json:"operation"`
	Producer        string         `json:"producer"`
	ProducerEventID string         `json:"producer_event_id"`
	SourcePosition  SourcePosition `json:"source_position"`
	Event           HazardEvent    `json:"event"`
}

type DeleteHazardCommand struct {
	Operation       string         `json:"operation"`
	Producer        string         `json:"producer"`
	ProducerEventID string         `json:"producer_event_id"`
	SourcePosition  SourcePosition `json:"source_position"`
	HazardID        string         `json:"hazard_id"`
	Source          string         `json:"source"`
	SourceRefID     string         `json:"source_ref_id"`
}

type IngestResult struct {
	HazardID        string `json:"hazard_id"`
	Result          string `json:"result"`
	Version         int    `json:"version"`
	ProducerEventID string `json:"producer_event_id"`
}

type SourceStatus struct {
	Source      string         `json:"source"`
	Status      string         `json:"status"`
	LastEventAt *time.Time     `json:"last_event_at,omitempty"`
	LagSeconds  *float64       `json:"lag_seconds,omitempty"`
	ReasonCode  string         `json:"reason_code,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

type UnprocessedMessage struct {
	MessageID        string         `json:"message_id"`
	Source           string         `json:"source"`
	SourcePosition   SourcePosition `json:"source_position"`
	ReceivedAt       time.Time      `json:"received_at"`
	Reason           string         `json:"reason"`
	Retryable        bool           `json:"retryable"`
	Status           string         `json:"status"`
	RawPayloadBase64 string         `json:"raw_payload_base64"`
	ContentType      string         `json:"content_type"`
}

// Client is the interface from section 11 of the contract. Services can use a
// fake implementation in tests and the HTTP implementation in production.
type Client interface {
	UpsertHazard(ctx context.Context, command UpsertHazardCommand) (IngestResult, error)
	DeleteHazard(ctx context.Context, command DeleteHazardCommand) (IngestResult, error)
	ReportSourceStatus(ctx context.Context, status SourceStatus) error
	StoreUnprocessed(ctx context.Context, message UnprocessedMessage) error
}

// TransportError is a network failure before a response was received.
type TransportError struct {
	Err error
}

func (e *TransportError) Error() string {
	return "internal ingest transport: " + e.Err.Error()
}

func (e *TransportError) Unwrap() error {
	return e.Err
}

// APIError is a non-2xx response from the Internal Ingest API.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("internal ingest: status %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("internal ingest: status %d: %s: %s", e.StatusCode, e.Code, e.Message)
}

// Retryable reports whether the request may be retried with the same
// idempotency key. Validation errors and conflicts are terminal.
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

func IsConflict(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == 409
}

func IsValidation(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && (apiErr.StatusCode == 400 || apiErr.StatusCode == 422)
}
