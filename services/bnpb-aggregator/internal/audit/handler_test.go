package audit

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
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

const testToken = "audit-test-token"

type fakeReader struct {
	filter          store.AuditFilter
	eventsCalls     int
	checksumCalls   int
	analyticsCalls  int
	page            store.AuditPage
	checksum        store.ChecksumResult
	analytics       store.AnalyticsResult
	analyticsFilter store.AnalyticsFilter
}

type fakeLocator struct {
	location *Location
	err      error
	calls    int
	id       string
}

func (f *fakeReader) AuditUnprocessed(_ context.Context, _ store.UnprocessedFilter) (store.UnprocessedPage, error) {
	return store.UnprocessedPage{Items: []store.UnprocessedMessage{}}, nil
}

func (f *fakeReader) GetUnprocessed(_ context.Context, _ string) (*store.UnprocessedMessage, error) {
	return nil, nil
}

func (f *fakeLocator) Locate(_ context.Context, id string) (*Location, error) {
	f.calls++
	f.id = id
	return f.location, f.err
}

func (f *fakeReader) AuditEvents(_ context.Context, filter store.AuditFilter) (store.AuditPage, error) {
	f.eventsCalls++
	f.filter = filter
	return f.page, nil
}

func (f *fakeReader) AuditChecksum(_ context.Context, _ domain.Source) (store.ChecksumResult, error) {
	f.checksumCalls++
	return f.checksum, nil
}

func (f *fakeReader) Analytics(_ context.Context, filter store.AnalyticsFilter) (store.AnalyticsResult, error) {
	f.analyticsCalls++
	f.analyticsFilter = filter
	return f.analytics, nil
}

func testMux(reader Reader) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, slog.New(slog.NewTextHandler(io.Discard, nil)), reader, &fakeLocator{}, testToken)
	return mux
}

func testMuxWithLocator(reader Reader, locator Locator) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, slog.New(slog.NewTextHandler(io.Discard, nil)), reader, locator, testToken)
	return mux
}

func request(t *testing.T, mux *http.ServeMux, target, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func cursorString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func TestAuditEndpointsRequireDedicatedBearerToken(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{}
	for _, token := range []string{"", "wrong-token"} {
		rec := request(t, testMux(reader), "/internal/audit/events", token)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q status = %d, want 401", token, rec.Code)
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Error.Code != "unauthorized" {
			t.Fatalf("error code = %q", body.Error.Code)
		}
	}
	if reader.eventsCalls != 0 {
		t.Fatalf("unauthorized requests reached storage %d times", reader.eventsCalls)
	}
}

func TestAuditEventsCursorPreservesOriginalFilters(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	boundary := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	reader := &fakeReader{page: store.AuditPage{
		Items: []domain.HazardEvent{{HazardID: "HZ-1", Source: domain.SourceBMKG}},
		Next:  &store.AuditCursor{IngestedAt: boundary, HazardID: "HZ-1"},
	}}
	mux := testMux(reader)

	first := request(t, mux, "/internal/audit/events?source=BMKG&since=2026-11-16T08:00:00Z&limit=1", testToken)
	if first.Code != http.StatusOK {
		t.Fatalf("first page status = %d (%s)", first.Code, first.Body.String())
	}
	var firstBody eventsResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatal(err)
	}
	if firstBody.Partial || firstBody.UnavailableUnits == nil || len(firstBody.Items) != 1 {
		t.Fatalf("first page = %#v", firstBody)
	}

	second := request(t, mux, "/internal/audit/events?cursor="+cursorString(firstBody.NextCursor), testToken)
	if second.Code != http.StatusOK {
		t.Fatalf("second page status = %d (%s)", second.Code, second.Body.String())
	}
	if reader.filter.Source != domain.SourceBMKG || reader.filter.Since == nil || !reader.filter.Since.Equal(since) {
		t.Fatalf("restored filter = %#v", reader.filter)
	}
	if reader.filter.Cursor == nil || !reader.filter.Cursor.IngestedAt.Equal(boundary) || reader.filter.Cursor.HazardID != "HZ-1" {
		t.Fatalf("restored cursor = %#v", reader.filter.Cursor)
	}
}

func TestAuditCursorRejectsChangedSource(t *testing.T) {
	t.Parallel()

	filter := store.AuditFilter{Source: domain.SourceBMKG, Limit: 1}
	raw := encodeEventsCursor(&store.AuditCursor{
		IngestedAt: time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC),
		HazardID:   "HZ-1",
	}, filter)
	rec := request(t, testMux(&fakeReader{}),
		"/internal/audit/events?source=PVMBG&cursor="+cursorString(raw), testToken)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAuditChecksumResponseMatchesContract(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	reader := &fakeReader{checksum: store.ChecksumResult{
		Count: 2, Checksum: "abc123", AsOf: asOf,
	}}
	rec := request(t, testMux(reader), "/internal/audit/checksum?source=BMKG", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	var body checksumResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Source != domain.SourceBMKG || body.Count != 2 || body.Checksum != "abc123" ||
		body.Algorithm != "sha256" || !body.AsOf.Equal(asOf) || body.Partial || body.UnavailableUnits == nil {
		t.Fatalf("checksum response = %#v", body)
	}
	if reader.checksumCalls != 1 {
		t.Fatalf("checksum calls = %d", reader.checksumCalls)
	}
}

func TestAuditChecksumRequiresKnownSource(t *testing.T) {
	t.Parallel()

	for _, target := range []string{
		"/internal/audit/checksum",
		"/internal/audit/checksum?source=UNKNOWN",
	} {
		rec := request(t, testMux(&fakeReader{}), target, testToken)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", target, rec.Code)
		}
	}
}

func TestAuditLocationMatchesContract(t *testing.T) {
	t.Parallel()

	locator := &fakeLocator{location: &Location{
		HazardID: "HZ-1",
		Entries: []LocationEntry{
			{Node: "node-a", Unit: "shard-0", Role: "primary"},
			{Node: "node-b", Unit: "shard-0-r1", Role: "replica"},
		},
	}}
	rec := request(t, testMuxWithLocator(&fakeReader{}, locator), "/internal/audit/location?hazard_id=HZ-1", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	var body Location
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if locator.calls != 1 || locator.id != "HZ-1" || body.HazardID != "HZ-1" || len(body.Entries) != 2 || body.Partial {
		t.Fatalf("locator=%#v body=%#v", locator, body)
	}
}

func TestAuditLocationValidatesAndReturnsNotFound(t *testing.T) {
	t.Parallel()

	locator := &fakeLocator{}
	mux := testMuxWithLocator(&fakeReader{}, locator)
	if rec := request(t, mux, "/internal/audit/location", testToken); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing id status = %d", rec.Code)
	}
	if locator.calls != 0 {
		t.Fatal("invalid request reached locator")
	}
	if rec := request(t, mux, "/internal/audit/location?hazard_id=missing", testToken); rec.Code != http.StatusNotFound {
		t.Fatalf("missing event status = %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestCanonicalAnalyticsResponseAndRange(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{analytics: store.AnalyticsResult{
		Total:      3,
		TimeSeries: []store.TimeCount{{Bucket: time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC), HazardType: "SEISMIC", Count: 2}},
		BySeverity: []store.NamedCount{{Name: "SIAGA", Count: 2}},
		ByArea:     []store.NamedCount{{Name: "Bandung", Count: 2}},
	}}
	rec := request(t, testMux(reader), "/internal/analytics/data?from=2026-11-01T00:00:00Z&to=2026-11-16T00:00:00Z&bucket=day", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	var body analyticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if reader.analyticsCalls != 1 || reader.analyticsFilter.From == nil || reader.analyticsFilter.To == nil ||
		reader.analyticsFilter.Bucket != "day" || body.Total != 3 || body.Partial || body.UnavailableUnits == nil {
		t.Fatalf("filter=%#v body=%#v", reader.analyticsFilter, body)
	}
}

func TestCanonicalAnalyticsRejectsInvalidRange(t *testing.T) {
	t.Parallel()
	for _, target := range []string{
		"/internal/analytics/data?bucket=week",
		"/internal/analytics/data?from=2026-11-17T00:00:00Z&to=2026-11-16T00:00:00Z",
		"/internal/analytics/data?from=not-a-time",
	} {
		if rec := request(t, testMux(&fakeReader{}), target, testToken); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d", target, rec.Code)
		}
	}
}

func TestPartialMetadataIsNotHidden(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{
		page:      store.AuditPage{Items: []domain.HazardEvent{}, Partial: true, UnavailableUnits: []string{"shard-1"}},
		checksum:  store.ChecksumResult{Checksum: "partial-hash", AsOf: time.Now().UTC(), Partial: true, UnavailableUnits: []string{"shard-1"}},
		analytics: store.AnalyticsResult{Partial: true, Degraded: true, UnavailableUnits: []string{"shard-1"}},
	}

	events := request(t, testMux(reader), "/internal/audit/events", testToken)
	var eventsBody eventsResponse
	if err := json.Unmarshal(events.Body.Bytes(), &eventsBody); err != nil || !eventsBody.Partial || len(eventsBody.UnavailableUnits) != 1 {
		t.Fatalf("events body=%s err=%v", events.Body.String(), err)
	}
	checksum := request(t, testMux(reader), "/internal/audit/checksum?source=BMKG", testToken)
	var checksumBody checksumResponse
	if err := json.Unmarshal(checksum.Body.Bytes(), &checksumBody); err != nil || !checksumBody.Partial || len(checksumBody.UnavailableUnits) != 1 {
		t.Fatalf("checksum body=%s err=%v", checksum.Body.String(), err)
	}
	analytics := request(t, testMux(reader), "/internal/analytics/data", testToken)
	var analyticsBody analyticsResponse
	if err := json.Unmarshal(analytics.Body.Bytes(), &analyticsBody); err != nil || !analyticsBody.Partial || !analyticsBody.Degraded || len(analyticsBody.UnavailableUnits) != 1 {
		t.Fatalf("analytics body=%s err=%v", analytics.Body.String(), err)
	}
}
