package p1tool

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/mapping"
)

func TestImportAuthenticatesAndDecodesResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected request: method=%s authorization=%q content-type=%q", r.Method, r.Header.Get("Authorization"), r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"received":1,"inserted":1,"updated":0,"unchanged":0}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Import(t.Context(), []mapping.SeismicEvent{{EventID: "eq-1"}})
	if err != nil || result.Inserted != 1 || result.Received != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestImportReturnsUpstreamProblem(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "secret", time.Second)
	if _, err := client.Import(t.Context(), []mapping.SeismicEvent{{EventID: "eq-1"}}); err == nil {
		t.Fatal("expected upstream error")
	}
}
