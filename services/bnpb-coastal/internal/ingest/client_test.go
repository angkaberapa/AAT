package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/core"
)

func testClient(t *testing.T, handler http.HandlerFunc) *HTTPClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewHTTPClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	return client
}

func TestNewHTTPClientValidation(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		token   string
		timeout time.Duration
	}{
		{name: "bad url", baseURL: "://bad", token: "x", timeout: time.Second},
		{name: "empty token", baseURL: "http://example.com", token: " ", timeout: time.Second},
		{name: "zero timeout", baseURL: "http://example.com", token: "x", timeout: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewHTTPClient(tc.baseURL, tc.token, tc.timeout); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestUpsertHazardSendsContract(t *testing.T) {
	var got UpsertHazardCommand
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/internal/ingest/hazards" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "coastal-observation-1" {
			t.Errorf("idempotency key = %q", got)
		}
		if got := r.Header.Get(core.CorrelationHeader); got != "corr-1" {
			t.Errorf("correlation id = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(IngestResult{
			HazardID:        "HZ-1",
			Result:          "inserted",
			Version:         1,
			ProducerEventID: "coastal-observation-1",
		})
	})

	ctx := core.WithCorrelationID(context.Background(), "corr-1")
	result, err := client.UpsertHazard(ctx, UpsertHazardCommand{
		Operation:       OperationUpsert,
		Producer:        "bnpb-coastal",
		ProducerEventID: "coastal-observation-1",
		SourcePosition:  SourcePosition{Type: "coastal_cursor", Value: "cursor-1"},
		Event: HazardEvent{
			HazardID:    "HZ-1",
			Source:      "SENSOR_PANTAI",
			SourceRefID: "obs-1",
			HazardType:  "COASTAL",
			Severity:    "AWAS",
			AreaName:    "Sumatera Barat",
			Latitude:    -0.95,
			Longitude:   100.35,
			OccurredAt:  time.Date(2026, 11, 12, 10, 30, 0, 0, time.UTC),
			Attributes:  map[string]any{"critical": true},
		},
	})
	if err != nil {
		t.Fatalf("UpsertHazard: %v", err)
	}
	if result.Result != "inserted" || result.Version != 1 {
		t.Fatalf("result = %+v", result)
	}
	if got.Operation != OperationUpsert || got.Producer != "bnpb-coastal" {
		t.Fatalf("body = %+v", got)
	}
	if got.Event.SourceRefID != "obs-1" || got.Event.HazardType != "COASTAL" {
		t.Fatalf("event = %+v", got.Event)
	}
}

func TestDeleteHazardSendsContract(t *testing.T) {
	var got DeleteHazardCommand
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Idempotency-Key"); got != "satellite-offset-9-fire-1" {
			t.Errorf("idempotency key = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(IngestResult{HazardID: "HZ-1", Result: "deleted", Version: 2})
	})

	result, err := client.DeleteHazard(context.Background(), DeleteHazardCommand{
		Operation:       OperationDelete,
		Producer:        "bnpb-satellite",
		ProducerEventID: "satellite-offset-9-fire-1",
		SourcePosition:  SourcePosition{Type: "sse_offset", Value: "9"},
		HazardID:        "HZ-1",
		Source:          "SATELIT",
		SourceRefID:     "fire-1",
	})
	if err != nil {
		t.Fatalf("DeleteHazard: %v", err)
	}
	if result.Result != "deleted" {
		t.Fatalf("result = %+v", result)
	}
	if got.Operation != OperationDelete || got.HazardID != "HZ-1" || got.SourceRefID != "fire-1" {
		t.Fatalf("body = %+v", got)
	}
}

func TestReportSourceStatus(t *testing.T) {
	var got SourceStatus
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/ingest/source-status" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "" {
			t.Errorf("source status must not send idempotency key, got %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	last := time.Date(2026, 11, 12, 10, 30, 0, 0, time.UTC)
	lag := 14.7
	if err := client.ReportSourceStatus(context.Background(), SourceStatus{
		Source:      "SENSOR_PANTAI",
		Status:      "degraded",
		LastEventAt: &last,
		LagSeconds:  &lag,
		ReasonCode:  "circuit_open",
		Details:     map[string]any{"cursor": "coastal-1837"},
	}); err != nil {
		t.Fatalf("ReportSourceStatus: %v", err)
	}
	if got.Source != "SENSOR_PANTAI" || got.Status != "degraded" || got.ReasonCode != "circuit_open" {
		t.Fatalf("body = %+v", got)
	}
}

func TestStoreUnprocessed(t *testing.T) {
	var got UnprocessedMessage
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/ingest/unprocessed" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "coastal-observation-392" {
			t.Errorf("idempotency key = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	if err := client.StoreUnprocessed(context.Background(), UnprocessedMessage{
		MessageID:        "coastal-observation-392",
		Source:           "SENSOR_PANTAI",
		SourcePosition:   SourcePosition{Type: "coastal_cursor", Value: "392"},
		ReceivedAt:       time.Date(2026, 11, 12, 10, 30, 0, 0, time.UTC),
		Reason:           "contract_violation: observed_at is missing",
		Retryable:        false,
		Status:           StatusParked,
		RawPayloadBase64: "e30=",
		ContentType:      "application/json",
	}); err != nil {
		t.Fatalf("StoreUnprocessed: %v", err)
	}
	if got.MessageID != "coastal-observation-392" || got.Status != StatusParked {
		t.Fatalf("body = %+v", got)
	}
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		retryAfter string
		conflict   bool
		validation bool
		retryable  bool
	}{
		{name: "conflict", status: http.StatusConflict, conflict: true},
		{name: "validation", status: http.StatusUnprocessableEntity, validation: true},
		{name: "bad request", status: http.StatusBadRequest, validation: true},
		{name: "rate limited", status: http.StatusTooManyRequests, retryAfter: "2", retryable: true},
		{name: "unavailable", status: http.StatusServiceUnavailable, retryable: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"code":"test","message":"test error"}}`))
			})

			_, err := client.UpsertHazard(context.Background(), UpsertHazardCommand{
				Producer:        "bnpb-coastal",
				ProducerEventID: "evt-1",
				Event:           HazardEvent{HazardID: "HZ-1"},
			})
			if err == nil {
				t.Fatal("expected error")
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error type = %T (%v)", err, err)
			}
			if IsConflict(err) != tc.conflict {
				t.Errorf("IsConflict = %v", IsConflict(err))
			}
			if IsValidation(err) != tc.validation {
				t.Errorf("IsValidation = %v", IsValidation(err))
			}
			if IsRetryable(err) != tc.retryable {
				t.Errorf("IsRetryable = %v", IsRetryable(err))
			}
			if tc.retryAfter != "" && apiErr.RetryAfter != 2*time.Second {
				t.Errorf("RetryAfter = %v", apiErr.RetryAfter)
			}
		})
	}
}

func TestTransportErrorIsRetryable(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	client.http.Timeout = 5 * time.Millisecond

	_, err := client.UpsertHazard(context.Background(), UpsertHazardCommand{
		Producer:        "bnpb-coastal",
		ProducerEventID: "evt-1",
		Event:           HazardEvent{HazardID: "HZ-1"},
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !IsRetryable(err) {
		t.Fatalf("timeout must be retryable, got %v", err)
	}
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("error type = %T", err)
	}
}

func TestCommandValidation(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("server must not be called for invalid command")
	})
	if _, err := client.UpsertHazard(context.Background(), UpsertHazardCommand{}); err == nil {
		t.Fatal("expected missing producer error")
	}
	if _, err := client.DeleteHazard(context.Background(), DeleteHazardCommand{}); err == nil {
		t.Fatal("expected missing producer error")
	}
	if err := client.ReportSourceStatus(context.Background(), SourceStatus{}); err == nil {
		t.Fatal("expected missing status error")
	}
	if err := client.StoreUnprocessed(context.Background(), UnprocessedMessage{}); err == nil {
		t.Fatal("expected missing message id error")
	}
}
