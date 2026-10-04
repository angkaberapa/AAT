package hazards

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/angkaberapa/AAT/services/bnpb-api/internal/security"
)

const upstreamPayload = `{
  "data": [{
    "hazard_id": "HZ-1",
    "source": "PVMBG",
    "source_ref_id": "RPT-1",
    "hazard_type": "VOLCANIC",
    "severity": "SIAGA",
    "area_name": "Gunung Merapi",
    "latitude": -7.54,
    "longitude": 110.446,
    "occurred_at": "2026-10-04T00:00:00Z",
    "ingested_at": "2026-10-04T00:00:01Z",
    "attributes": {"confidence_level": 0.82}
  }]
}`

type fakeHTTPClient struct {
	calls int
	query string
}

func (client *fakeHTTPClient) Do(request *http.Request, _ string) (*http.Response, error) {
	client.calls++
	client.query = request.URL.RawQuery
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(upstreamPayload)),
	}, nil
}

func TestMediaReceivesOnlySummaryFields(t *testing.T) {
	client := &fakeHTTPClient{}
	handler := newTestHandler(t, client, security.Principal{
		ClientID: "media",
		Scopes:   map[string]struct{}{security.ScopeHazardsSummary: {}},
	})
	request := httptest.NewRequest(http.MethodGet, "/hazards?source=PVMBG", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Data-Scope") != "summary" {
		t.Fatalf("X-Data-Scope = %q", response.Header().Get("X-Data-Scope"))
	}
	event := decodeFirstEvent(t, response)
	for rawField := range rawFields {
		if _, exists := event[rawField]; exists {
			t.Fatalf("media response leaked raw field %q", rawField)
		}
	}
	for summaryField := range summaryFields {
		if _, exists := event[summaryField]; !exists {
			t.Fatalf("media response omitted summary field %q", summaryField)
		}
	}
	if client.query != "source=PVMBG" {
		t.Fatalf("forwarded query = %q, want source=PVMBG", client.query)
	}
}

func TestMediaExplicitRawFieldRequestIsRejectedBeforeUpstreamCall(t *testing.T) {
	client := &fakeHTTPClient{}
	handler := newTestHandler(t, client, security.Principal{
		ClientID: "media",
		Scopes:   map[string]struct{}{security.ScopeHazardsSummary: {}},
	})
	request := httptest.NewRequest(http.MethodGet, "/hazards?fields=hazard_id,latitude", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", response.Code, response.Body.String())
	}
	if client.calls != 0 {
		t.Fatalf("upstream calls = %d, want 0", client.calls)
	}
}

func TestFieldClientCanRequestRawFields(t *testing.T) {
	client := &fakeHTTPClient{}
	handler := newTestHandler(t, client, security.Principal{
		ClientID: "field",
		Scopes:   map[string]struct{}{security.ScopeHazardsRead: {}},
	})
	request := httptest.NewRequest(http.MethodGet, "/hazards?fields=hazard_id,latitude,attributes", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	event := decodeFirstEvent(t, response)
	if len(event) != 3 || event["latitude"] == nil || event["attributes"] == nil {
		t.Fatalf("unexpected selected event: %+v", event)
	}
	if client.query != "" {
		t.Fatalf("fields query leaked to aggregator: %q", client.query)
	}
}

func newTestHandler(t *testing.T, client *fakeHTTPClient, principal security.Principal) http.Handler {
	t.Helper()
	handler, err := NewHandler("http://aggregator.internal:8000", client)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	mux := http.NewServeMux()
	handler.Register(mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			ctx := security.WithPrincipal(context.Background(), principal)
			next.ServeHTTP(w, request.WithContext(ctx))
		})
	})
	return mux
}

func decodeFirstEvent(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Data) != 1 {
		t.Fatalf("data length = %d, want 1", len(payload.Data))
	}
	return payload.Data[0]
}
