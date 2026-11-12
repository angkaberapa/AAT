package sensor

import (
	"context"
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
	client, err := NewHTTPClient(server.URL, "sensor-token", time.Second)
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	return client
}

func TestFetchPageSendsContract(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/v1/observations" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("limit"); got != "2" {
			t.Errorf("limit = %q", got)
		}
		if got := r.URL.Query().Get("cursor"); got != "cursor-1" {
			t.Errorf("cursor = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sensor-token" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get(core.CorrelationHeader); got != "corr-1" {
			t.Errorf("correlation id = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"generated_at":"2026-11-12T10:30:00Z",
			"items":[
				{"observation_id":"obs-1","station_id":"stn-padang-01"},
				{"observation_id":"obs-2","station_id":"stn-cilacap-01"}
			],
			"next_cursor":"cursor-2"
		}`))
	})

	ctx := core.WithCorrelationID(context.Background(), "corr-1")
	page, err := client.FetchPage(ctx, "cursor-1", 2)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if !page.GeneratedAt.Equal(time.Date(2026, 11, 12, 10, 30, 0, 0, time.UTC)) {
		t.Fatalf("generated at = %v", page.GeneratedAt)
	}
	if len(page.Items) != 2 {
		t.Fatalf("items = %d", len(page.Items))
	}
	if page.NextCursor != "cursor-2" {
		t.Fatalf("next cursor = %q", page.NextCursor)
	}
}

func TestFetchPageOmitsEmptyCursor(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if _, exists := r.URL.Query()["cursor"]; exists {
			t.Errorf("cursor should be omitted, query = %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"generated_at":"2026-11-12T10:30:00Z","items":[],"next_cursor":""}`))
	})
	page, err := client.FetchPage(context.Background(), "", 100)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatalf("page = %+v", page)
	}
}

func TestFetchPageErrors(t *testing.T) {
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
			_, err := client.FetchPage(context.Background(), "", 10)
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

func TestFetchPageRejectsInvalidGeneratedAt(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"generated_at":"not-a-time","items":[],"next_cursor":""}`))
	})
	if _, err := client.FetchPage(context.Background(), "", 10); err == nil {
		t.Fatal("expected invalid generated_at error")
	}
}

func TestFetchPageTimeoutIsRetryable(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{"generated_at":"2026-11-12T10:30:00Z","items":[]}`))
	})
	client.http.Timeout = 5 * time.Millisecond

	_, err := client.FetchPage(context.Background(), "", 10)
	if err == nil {
		t.Fatal("expected timeout")
	}
	if !IsRetryable(err) {
		t.Fatalf("timeout must be retryable, got %v", err)
	}
}

func TestFetchPageValidatesLimit(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("server must not be called")
	})
	if _, err := client.FetchPage(context.Background(), "", 0); err == nil {
		t.Fatal("expected limit error")
	}
	if _, err := client.FetchPage(context.Background(), "", 1001); err == nil {
		t.Fatal("expected limit error")
	}
}
