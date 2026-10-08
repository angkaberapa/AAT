package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/ingest"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

type fakeReader struct{ lastFilter store.Filter }

func (r *fakeReader) List(_ context.Context, f store.Filter) ([]domain.HazardEvent, error) {
	r.lastFilter = f
	return []domain.HazardEvent{{HazardID: "HZ-1", Source: domain.SourcePVMBG}}, nil
}

func (r *fakeReader) Get(_ context.Context, id string) (*domain.HazardEvent, error) {
	if id != "HZ-1" {
		return nil, nil
	}
	return &domain.HazardEvent{HazardID: "HZ-1", Source: domain.SourcePVMBG}, nil
}

type fakeStatuses map[domain.Source]ingest.SourceStatus

func (f fakeStatuses) Status(s domain.Source) ingest.SourceStatus { return f[s] }

func (f fakeStatuses) Snapshot() []ingest.SourceStatus {
	return []ingest.SourceStatus{f[domain.SourceBMKG], f[domain.SourcePVMBG]}
}

func newTestMux(reader Reader) *http.ServeMux {
	staleSince := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	statuses := fakeStatuses{
		domain.SourceBMKG: {Source: domain.SourceBMKG, Status: ingest.StatusOK},
		domain.SourcePVMBG: {
			Source: domain.SourcePVMBG, Status: ingest.StatusStale,
			StaleSince: &staleSince, LastError: "HTTP 503: secret upstream detail",
		},
	}
	mux := http.NewServeMux()
	Register(mux, slog.New(slog.NewTextHandler(io.Discard, nil)), reader, statuses)
	return mux
}

func get(t *testing.T, mux *http.ServeMux, target string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v (%s)", target, err, rec.Body.String())
	}
	return rec, body
}

func TestVolcanicQueryCarriesStaleMarker(t *testing.T) {
	_, body := get(t, newTestMux(&fakeReader{}), "/hazards?hazard_type=VOLCANIC")

	meta := body["meta"].(map[string]any)
	if meta["stale"] != true {
		t.Fatalf("meta.stale = %v", meta["stale"])
	}
	sources := meta["sources"].(map[string]any)
	if _, ok := sources["BMKG"]; ok {
		t.Fatal("seismic source should not be listed for a volcanic query")
	}
	pvmbg := sources["PVMBG"].(map[string]any)
	if pvmbg["status"] != "stale" || pvmbg["stale_since"] != "2026-10-07T10:00:00Z" {
		t.Fatalf("PVMBG meta = %v", pvmbg)
	}
	if _, leaked := pvmbg["last_error"]; leaked {
		t.Fatal("internal error detail leaked into meta")
	}
	if len(body["data"].([]any)) != 1 {
		t.Fatal("stored data must still be served while the source is stale")
	}
}

func TestSeismicQueryIsFreshWhilePVMBGIsDown(t *testing.T) {
	_, body := get(t, newTestMux(&fakeReader{}), "/hazards?hazard_type=SEISMIC")
	meta := body["meta"].(map[string]any)
	if meta["stale"] != false {
		t.Fatalf("meta = %v", meta)
	}
}

func TestLimitDefaultsAndValidation(t *testing.T) {
	reader := &fakeReader{}
	mux := newTestMux(reader)

	get(t, mux, "/hazards")
	if reader.lastFilter.Limit != defaultLimit {
		t.Fatalf("default limit = %d", reader.lastFilter.Limit)
	}
	get(t, mux, "/hazards?limit=5")
	if reader.lastFilter.Limit != 5 {
		t.Fatalf("limit = %d", reader.lastFilter.Limit)
	}
	for _, bad := range []string{"0", "-1", "abc", "1001"} {
		rec, _ := get(t, mux, "/hazards?limit="+bad)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("limit=%s status = %d", bad, rec.Code)
		}
	}
}

func TestSourcesEndpointIncludesErrorDetail(t *testing.T) {
	_, body := get(t, newTestMux(&fakeReader{}), "/sources")
	data := body["data"].([]any)
	if len(data) != 2 || data[1].(map[string]any)["last_error"] == nil {
		t.Fatalf("sources = %v", data)
	}
}

func TestSingleEventCarriesFreshnessHeaders(t *testing.T) {
	rec, body := get(t, newTestMux(&fakeReader{}), "/hazards/HZ-1")
	if body["hazard_id"] != "HZ-1" {
		t.Fatalf("body = %v", body)
	}
	h := rec.Header()
	if h.Get(HeaderDataStale) != "true" || h.Get(HeaderSourceStatus) != "stale" ||
		h.Get(HeaderStaleSince) != "2026-10-07T10:00:00Z" {
		t.Fatalf("headers = %v", h)
	}
}

func TestListSetsStaleHeader(t *testing.T) {
	rec, _ := get(t, newTestMux(&fakeReader{}), "/hazards?hazard_type=SEISMIC")
	if rec.Header().Get(HeaderDataStale) != "false" {
		t.Fatalf("X-Data-Stale = %q", rec.Header().Get(HeaderDataStale))
	}
}
