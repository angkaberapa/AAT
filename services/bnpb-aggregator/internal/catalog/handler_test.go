package catalog

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/mapping"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

type fakeMapper struct {
	err error
}

func (f fakeMapper) MapSeismicEvent(raw mapping.SeismicEvent, _ *mapping.TsunamiWarning) (domain.HazardEvent, error) {
	if f.err != nil {
		return domain.HazardEvent{}, f.err
	}
	return domain.HazardEvent{HazardID: "HZ-" + raw.EventID, Source: domain.SourceBMKG}, nil
}

type fakeImporter struct {
	events []domain.HazardEvent
	counts map[store.SaveResult]int
}

func (f *fakeImporter) ImportBatch(_ context.Context, events []domain.HazardEvent) (map[store.SaveResult]int, error) {
	f.events = events
	return f.counts, nil
}

func catalogMux(mapper seismicMapper, importer importer) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, slog.New(slog.NewTextHandler(io.Discard, nil)), mapper, importer, "test-token")
	return mux
}

func catalogRequest(mux *http.ServeMux, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/internal/p1/catalog", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestCatalogImportMapsAndStoresBatch(t *testing.T) {
	t.Parallel()
	destination := &fakeImporter{counts: map[store.SaveResult]int{store.Inserted: 1}}
	body := `{"events":[{"event_id":"eq-1","magnitude":6.7,"depth_km":10,"epicenter_lat":-6.2,"epicenter_lon":106.8,"region_name":"Jakarta","occurred_at":"2026-11-16T10:00:00Z","potential_tsunami":false}]}`
	rec := catalogRequest(catalogMux(fakeMapper{}, destination), "test-token", body)
	if rec.Code != http.StatusOK || len(destination.events) != 1 || destination.events[0].HazardID != "HZ-eq-1" {
		t.Fatalf("status=%d body=%s events=%#v", rec.Code, rec.Body.String(), destination.events)
	}
	if !strings.Contains(rec.Body.String(), `"inserted":1`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestCatalogImportRejectsUnauthorizedAndInvalidEvents(t *testing.T) {
	t.Parallel()
	validBody := `{"events":[{"event_id":"eq-1","magnitude":6.7,"depth_km":10,"epicenter_lat":-6.2,"epicenter_lon":106.8,"region_name":"Jakarta","occurred_at":"` + time.Now().UTC().Format(time.RFC3339) + `","potential_tsunami":false}]}`
	if rec := catalogRequest(catalogMux(fakeMapper{}, &fakeImporter{}), "", validBody); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", rec.Code)
	}
	if rec := catalogRequest(catalogMux(fakeMapper{err: errors.New("bad magnitude")}, &fakeImporter{}), "test-token", validBody); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid event status = %d (%s)", rec.Code, rec.Body.String())
	}
}
