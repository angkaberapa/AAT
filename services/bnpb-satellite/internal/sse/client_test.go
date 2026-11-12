package sse

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/core"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "satellite-token")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func TestStreamParsesEvents(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/stream" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("from"); got != "1872" {
			t.Errorf("from = %q", got)
		}
		if got := r.Header.Get("Last-Event-ID"); got != "1872" {
			t.Errorf("Last-Event-ID = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer satellite-token" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("accept = %q", got)
		}
		if got := r.Header.Get(core.CorrelationHeader); got != "corr-1" {
			t.Errorf("correlation = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer is not a flusher")
		}
		_, _ = fmt.Fprint(w, ": heartbeat\n\n")
		_, _ = fmt.Fprint(w, "event: detection\ndata: {\"id\":1}\nid: 1873\n\n")
		_, _ = fmt.Fprint(w, "event: update\ndata: line1\ndata: line2\nid: 1874\n\n")
		_, _ = fmt.Fprint(w, "event: retract\ndata: {\"id\":2}\nid: 1875\n\n")
		flusher.Flush()
	})

	var events []Event
	ctx := core.WithCorrelationID(context.Background(), "corr-1")
	err := client.Stream(ctx, "1872", func(event Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Type != "detection" || events[0].ID != "1873" || string(events[0].Data) != `{"id":1}` {
		t.Fatalf("first event = %+v", events[0])
	}
	if string(events[1].Data) != "line1\nline2" {
		t.Fatalf("multiline data = %q", events[1].Data)
	}
	if events[2].Type != "retract" || events[2].ID != "1875" {
		t.Fatalf("third event = %+v", events[2])
	}
}

func TestStreamDispatchesPendingEventAtEOF(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "event: detection\ndata: {\"id\":1}\nid: 1873")
	})
	var events []Event
	if err := client.Stream(context.Background(), "", func(event Event) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(events) != 1 || events[0].Type != "detection" || events[0].ID != "1873" {
		t.Fatalf("events = %+v", events)
	}
}

func TestStreamAPIErrors(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		retryable bool
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"error":{"code":"unauthorized","message":"bad token"}}`},
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{"error":{"code":"too_many_connections","message":"slow down"}}`, retryable: true},
		{name: "unavailable", status: http.StatusServiceUnavailable, body: `{"error":{"code":"unavailable","message":"down"}}`, retryable: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			err := client.Stream(context.Background(), "", func(Event) error { return nil })
			if err == nil {
				t.Fatal("expected error")
			}
			if IsRetryable(err) != tc.retryable {
				t.Fatalf("IsRetryable = %v (%v)", IsRetryable(err), err)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status {
				t.Fatalf("error = %T %v", err, err)
			}
		})
	}
}

func TestStreamHandlerErrorStops(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "event: detection\ndata: {\"id\":1}\n\n")
		_, _ = fmt.Fprint(w, "event: detection\ndata: {\"id\":2}\n\n")
	})
	stop := errors.New("stop")
	err := client.Stream(context.Background(), "", func(Event) error { return stop })
	if !errors.Is(err, stop) {
		t.Fatalf("error = %v", err)
	}
}

func TestStreamSkipsCommentsAndEmptyData(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, ": heartbeat\n\n")
		_, _ = fmt.Fprint(w, "event: detection\n\n")
		_, _ = fmt.Fprint(w, "event: update\ndata: ok\n\n")
	})
	var events []Event
	if err := client.Stream(context.Background(), "", func(event Event) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(events) != 1 || events[0].Type != "update" || string(events[0].Data) != "ok" {
		t.Fatalf("events = %+v", events)
	}
}

func TestNewClientValidation(t *testing.T) {
	if _, err := NewClient("://bad", "token"); err == nil {
		t.Fatal("expected bad URL error")
	}
	if _, err := NewClient("http://example.com", " "); err == nil {
		t.Fatal("expected empty token error")
	}
}

func TestIsRetryableTransportError(t *testing.T) {
	if !IsRetryable(&TransportError{Err: errors.New("connection refused")}) {
		t.Fatal("transport error should be retryable")
	}
	if IsRetryable(&TransportError{Err: context.Canceled}) {
		t.Fatal("context cancellation should not be retryable")
	}
}

func TestAPIErrorRetryAfter(t *testing.T) {
	if got := parseRetryAfter("2"); got != 2*time.Second {
		t.Fatalf("retry after = %v", got)
	}
	if got := parseRetryAfter("not-a-time"); got != 0 {
		t.Fatalf("invalid retry after = %v", got)
	}
	if !strings.Contains((&APIError{StatusCode: 429, Code: "too_many"}).Error(), "429") {
		t.Fatal("APIError should include status")
	}
}
