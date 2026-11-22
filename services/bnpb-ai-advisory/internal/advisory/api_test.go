package advisory

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-ai-advisory/internal/detector"
	"github.com/angkaberapa/AAT/services/bnpb-ai-advisory/internal/summarizer"
)

func testAPI(store *Store) *API {
	return NewAPI(store, 3.5, NewRecentEvents(10),
		summarizer.New(summarizer.Config{}, slog.Default()), 10)
}

func TestAPIAdvisoriesNewestFirst(t *testing.T) {
	store := NewStore(10)
	store.Add(detector.Advisory{ID: "old", CreatedAt: time.Unix(1, 0)})
	store.Add(detector.Advisory{ID: "new", CreatedAt: time.Unix(2, 0)})

	mux := http.NewServeMux()
	testAPI(store).Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/advisories?limit=1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Data []detector.Advisory `json:"data"`
		Meta map[string]any      `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].ID != "new" {
		t.Fatalf("data = %+v, want only newest", body.Data)
	}
	if body.Meta["kind"] != "derived_advisory" {
		t.Fatalf("meta.kind = %v", body.Meta["kind"])
	}
}

func TestAPIRejectsInvalidLimit(t *testing.T) {
	mux := http.NewServeMux()
	testAPI(NewStore(10)).Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/advisories?limit=abc", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
