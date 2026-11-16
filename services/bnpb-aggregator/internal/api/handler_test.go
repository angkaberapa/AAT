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

type fakeReader struct {
	lastFilter store.Filter
	next       *store.LatestCursor
}

func (r *fakeReader) ListPage(_ context.Context, f store.Filter) (store.Page, error) {
	r.lastFilter = f
	return store.Page{
		Items: []domain.HazardEvent{{HazardID: "HZ-1", Source: domain.SourcePVMBG}},
		Next:  r.next,
	}, nil
}

func TestLatestCursorRoundTrip(t *testing.T) {
	t.Parallel()

	want := store.LatestCursor{
		OccurredAt: time.Date(2026, 11, 16, 9, 30, 0, 123, time.UTC),
		HazardID:   "HZ-7fff",
	}
	raw, ok := encodeLatestCursor(&want).(string)
	if !ok || raw == "" {
		t.Fatalf("encoded cursor = %#v", raw)
	}
	got, err := decodeLatestCursor(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !got.OccurredAt.Equal(want.OccurredAt) || got.HazardID != want.HazardID {
		t.Fatalf("decoded cursor = %#v, want %#v", got, want)
	}
}

func TestLatestPaginationIsExplicitAndReturnsCursor(t *testing.T) {
	t.Parallel()

	next := &store.LatestCursor{
		OccurredAt: time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC),
		HazardID:   "HZ-8000",
	}
	reader := &fakeReader{next: next}
	mux := newTestMux(reader)

	_, first := get(t, mux, "/hazards?sort=latest&limit=50")
	raw, ok := first["next_cursor"].(string)
	if !ok || raw == "" {
		t.Fatalf("next_cursor = %#v", first["next_cursor"])
	}
	_, second := get(t, mux, "/hazards?sort=latest&limit=50&cursor="+raw)
	if second["next_cursor"] == nil {
		// The fake returns the same next boundary; the purpose here is parsing and forwarding it.
		t.Fatal("next_cursor unexpectedly missing")
	}
	if reader.lastFilter.Order != store.OrderLatest || reader.lastFilter.Cursor == nil {
		t.Fatalf("filter = %#v", reader.lastFilter)
	}
	if !reader.lastFilter.Cursor.OccurredAt.Equal(next.OccurredAt) || reader.lastFilter.Cursor.HazardID != next.HazardID {
		t.Fatalf("decoded filter cursor = %#v, want %#v", reader.lastFilter.Cursor, next)
	}
}

func TestLatestPaginationRejectsInvalidCombinations(t *testing.T) {
	t.Parallel()

	mux := newTestMux(&fakeReader{})
	for _, target := range []string{
		"/hazards?cursor=not-a-cursor",
		"/hazards?sort=latest&cursor=not-a-cursor",
		"/hazards?sort=oldest",
		"/hazards?sort=latest&since=2026-11-16T09:00:00Z",
	} {
		rec, _ := get(t, mux, target)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", target, rec.Code)
		}
	}
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
func TestM2HazardTypesAccepted(t *testing.T) {
	mux := newTestMux(&fakeReader{})

	rec, body := get(t, mux, "/hazards?hazard_type=WILDFIRE")
	if rec.Code != http.StatusOK {
		t.Fatalf("WILDFIRE status = %d", rec.Code)
	}
	sources := body["meta"].(map[string]any)["sources"].(map[string]any)
	if _, ok := sources["SATELIT"]; !ok || len(sources) != 1 {
		t.Fatalf("WILDFIRE meta sources = %v, want only SATELIT", sources)
	}

	if rec, _ := get(t, mux, "/hazards?source=SENSOR_PANTAI"); rec.Code != http.StatusOK {
		t.Fatalf("SENSOR_PANTAI status = %d", rec.Code)
	}
	if rec, _ := get(t, mux, "/hazards?hazard_type=FLOOD"); rec.Code != http.StatusBadRequest {
		t.Fatalf("FLOOD status = %d, want 400", rec.Code)
	}
}
