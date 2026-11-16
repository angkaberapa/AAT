package analytics

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-consumer-dashboard/internal/core"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestProxyKeepsCredentialServerSideAndPreservesQuery(t *testing.T) {
	t.Parallel()

	service := New("http://aggregator", "audit-secret", time.Second, testLogger())
	service.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer audit-secret" {
			t.Fatalf("authorization = %q", got)
		}
		if r.URL.Path != "/internal/analytics/data" || r.URL.Query().Get("bucket") != "hour" ||
			r.URL.Query().Get("from") != "2026-11-16T00:00:00Z" {
			t.Fatalf("upstream target = %s", r.URL.String())
		}
		if r.Header.Get(core.CorrelationHeader) != "test-correlation" {
			t.Fatalf("correlation id = %q", r.Header.Get(core.CorrelationHeader))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"total":3,"partial":false}`)),
		}, nil
	})
	mux := http.NewServeMux()
	service.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/analytics/data?bucket=hour&from=2026-11-16T00:00:00Z&ignored=secret", nil)
	req = req.WithContext(core.WithCorrelationID(req.Context(), "test-correlation"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":3`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDashboardPageIsEmbeddedWithoutCredential(t *testing.T) {
	t.Parallel()
	service := New("http://aggregator", "do-not-render-this-token", time.Second, testLogger())
	mux := http.NewServeMux()
	service.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/analytics", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Hazard Analytics") {
		t.Fatalf("status=%d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "do-not-render-this-token") {
		t.Fatal("audit credential leaked into browser page")
	}
}

func TestAnalyticsProxyFailsClosedWithoutCredential(t *testing.T) {
	t.Parallel()
	service := New("http://aggregator", "", time.Second, testLogger())
	mux := http.NewServeMux()
	service.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/analytics/data", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
}
