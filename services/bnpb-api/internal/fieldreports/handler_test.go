package fieldreports

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/angkaberapa/AAT/services/bnpb-api/internal/security"
)

type fakeClient struct{ request *http.Request }

func (client *fakeClient) Do(request *http.Request, target string) (*http.Response, error) {
	client.request = request
	return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(`{"accepted":true}`)), Header: make(http.Header)}, nil
}

func TestFieldReportRequiresScopeAndForwardsIdempotently(t *testing.T) {
	client := &fakeClient{}
	handler, err := New("http://aggregator:8000", "internal-secret", client)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	handler.Register(mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := security.Principal{ClientID: "field-team", Scopes: map[string]struct{}{security.ScopeFieldReportsWrite: {}}}
			next.ServeHTTP(w, r.WithContext(security.WithPrincipal(context.Background(), principal)))
		})
	})
	req := httptest.NewRequest(http.MethodPost, "/field-reports", strings.NewReader(`{"occurred_at":"2026-11-16T00:00:00Z"}`))
	req.Header.Set("Idempotency-Key", "report-1")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if client.request.Header.Get("Authorization") != "Bearer internal-secret" || client.request.Header.Get("Idempotency-Key") != "report-1" || client.request.Header.Get("X-Client-ID") != "field-team" {
		t.Fatalf("unexpected forwarded headers: %v", client.request.Header)
	}
}

func TestFieldReportWithoutWriteScopeIsForbidden(t *testing.T) {
	handler, _ := New("http://aggregator:8000", "internal-secret", &fakeClient{})
	mux := http.NewServeMux()
	handler.Register(mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(security.WithPrincipal(r.Context(), security.Principal{ClientID: "media", Scopes: map[string]struct{}{security.ScopeHazardsSummary: {}}})))
		})
	})
	req := httptest.NewRequest(http.MethodPost, "/field-reports", strings.NewReader(`{}`))
	req.Header.Set("Idempotency-Key", "report-1")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
