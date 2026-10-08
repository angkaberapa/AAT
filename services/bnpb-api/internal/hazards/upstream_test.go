package hazards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/angkaberapa/AAT/services/bnpb-api/internal/security"
)

type stubClient struct {
	status int
	body   string
	header http.Header
	err    error
}

func (c stubClient) Do(*http.Request, string) (*http.Response, error) {
	if c.err != nil {
		return nil, c.err
	}
	header := c.header
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: c.status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(c.body)),
	}, nil
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func serveWith(t *testing.T, client HTTPClient, target string, scope string) *httptest.ResponseRecorder {
	t.Helper()
	handler, err := NewHandler("http://aggregator.internal:8000", client)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	handler.Register(mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := security.Principal{ClientID: "c", Scopes: map[string]struct{}{scope: {}}}
			next.ServeHTTP(w, r.WithContext(security.WithPrincipal(context.Background(), principal)))
		})
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, rec.Body.String())
	}
	return body["error"]
}

func TestUpstreamFailuresMapToExplicitStatuses(t *testing.T) {
	cases := []struct {
		name       string
		client     stubClient
		wantStatus int
		wantCode   string
		retryAfter bool
	}{
		{
			name:       "aggregator down",
			client:     stubClient{err: &url.Error{Op: "Get", URL: "x", Err: errors.New("connection refused")}},
			wantStatus: http.StatusServiceUnavailable, wantCode: "aggregator_unavailable", retryAfter: true,
		},
		{
			name:       "aggregator timeout",
			client:     stubClient{err: &url.Error{Op: "Get", URL: "x", Err: timeoutErr{}}},
			wantStatus: http.StatusGatewayTimeout, wantCode: "aggregator_timeout", retryAfter: true,
		},
		{
			name:       "deadline exceeded",
			client:     stubClient{err: fmt.Errorf("wrapped: %w", context.DeadlineExceeded)},
			wantStatus: http.StatusGatewayTimeout, wantCode: "aggregator_timeout", retryAfter: true,
		},
		{
			name:       "unknown id passes through",
			client:     stubClient{status: http.StatusNotFound, body: `{"error":"hazard event not found"}`},
			wantStatus: http.StatusNotFound, wantCode: "not_found",
		},
		{
			name:       "bad query passes through",
			client:     stubClient{status: http.StatusBadRequest, body: `{"error":"invalid 'limit'"}`},
			wantStatus: http.StatusBadRequest, wantCode: "invalid_request",
		},
		{
			name:       "aggregator 500 is not forwarded as 500",
			client:     stubClient{status: http.StatusInternalServerError, body: `{"error":"boom"}`},
			wantStatus: http.StatusBadGateway, wantCode: "upstream_error", retryAfter: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serveWith(t, tc.client, "/hazards/HZ-1", security.ScopeHazardsRead)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if code := errorCode(t, rec); code != tc.wantCode {
				t.Fatalf("error = %q, want %q", code, tc.wantCode)
			}
			if got := rec.Header().Get("Retry-After") != ""; got != tc.retryAfter {
				t.Fatalf("Retry-After present = %v, want %v", got, tc.retryAfter)
			}
		})
	}
}

func TestFreshnessMetaReachesMediaClient(t *testing.T) {
	body := `{"data":[{"hazard_id":"HZ-1","source":"PVMBG","latitude":-7.5,"attributes":{}}],
	          "meta":{"stale":true,"sources":{"PVMBG":{"status":"stale","stale_since":"2026-10-07T10:00:00Z"}}}}`
	rec := serveWith(t, stubClient{status: http.StatusOK, body: body}, "/hazards?hazard_type=VOLCANIC", security.ScopeHazardsSummary)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var payload struct {
		Data []map[string]any `json:"data"`
		Meta map[string]any   `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Meta["stale"] != true {
		t.Fatalf("meta = %v", payload.Meta)
	}
	if _, leaked := payload.Data[0]["latitude"]; leaked {
		t.Fatal("raw field leaked to media")
	}
}

func TestFreshnessHeadersForwardedForSingleEvent(t *testing.T) {
	header := http.Header{}
	header.Set("X-Data-Stale", "true")
	header.Set("X-Source-Status", "stale")
	header.Set("X-Stale-Since", "2026-10-07T10:00:00Z")
	header.Set("X-Internal-Thing", "must not be forwarded")
	client := stubClient{status: http.StatusOK, header: header,
		body: `{"hazard_id":"HZ-1","source":"PVMBG","latitude":-7.5}`}

	rec := serveWith(t, client, "/hazards/HZ-1", security.ScopeHazardsSummary)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, name := range []string{"X-Data-Stale", "X-Source-Status", "X-Stale-Since"} {
		if rec.Header().Get(name) != header.Get(name) {
			t.Fatalf("%s = %q", name, rec.Header().Get(name))
		}
	}
	if rec.Header().Get("X-Internal-Thing") != "" {
		t.Fatal("unrelated upstream header forwarded")
	}
}
