package advisory

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-ai-advisory/internal/summarizer"
)

func TestAPISummaryFallsBackWithoutLLM(t *testing.T) {
	store := NewStore(10)
	recent := NewRecentEvents(10)
	recent.Add(Event{Source: "BMKG", HazardType: "EARTHQUAKE", Severity: "NORMAL", AreaName: "Sumatera Barat", OccurredAt: time.Unix(1, 0)})

	api := NewAPI(store, 3.5, recent,
		summarizer.New(summarizer.Config{}, slog.Default()), 10)
	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/summary", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Data struct {
			Summary string `json:"summary"`
			Source  string `json:"source"`
		} `json:"data"`
		Meta struct {
			EventCount int `json:"event_count"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Summary == "" || body.Data.Source != "fallback" {
		t.Fatalf("summary = %+v, want fallback text", body.Data)
	}
	if body.Meta.EventCount != 1 {
		t.Fatalf("event_count = %d, want 1", body.Meta.EventCount)
	}
}
