package processor

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/ingest"
	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/sensor"
	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/store"
)

var pageGeneratedAt = time.Date(2026, 11, 12, 10, 30, 0, 0, time.UTC)

type fakeSensor struct {
	page   sensor.Page
	err    error
	calls  int
	cursor string
}

func (f *fakeSensor) FetchPage(ctx context.Context, cursor string, limit int) (sensor.Page, error) {
	f.calls++
	f.cursor = cursor
	if f.err != nil {
		return sensor.Page{}, f.err
	}
	return f.page, nil
}

type fakeIngest struct {
	upserts         []ingest.UpsertHazardCommand
	deletes         []ingest.DeleteHazardCommand
	statuses        []ingest.SourceStatus
	unprocessed     []ingest.UnprocessedMessage
	failUpsert      error
	failUnprocessed error
}

func (f *fakeIngest) UpsertHazard(ctx context.Context, command ingest.UpsertHazardCommand) (ingest.IngestResult, error) {
	if f.failUpsert != nil {
		return ingest.IngestResult{}, f.failUpsert
	}
	f.upserts = append(f.upserts, command)
	return ingest.IngestResult{HazardID: command.Event.HazardID, Result: "inserted", Version: 1, ProducerEventID: command.ProducerEventID}, nil
}

func (f *fakeIngest) DeleteHazard(ctx context.Context, command ingest.DeleteHazardCommand) (ingest.IngestResult, error) {
	f.deletes = append(f.deletes, command)
	return ingest.IngestResult{HazardID: command.HazardID, Result: "deleted", Version: 1, ProducerEventID: command.ProducerEventID}, nil
}

func (f *fakeIngest) ReportSourceStatus(ctx context.Context, status ingest.SourceStatus) error {
	f.statuses = append(f.statuses, status)
	return nil
}

func (f *fakeIngest) StoreUnprocessed(ctx context.Context, message ingest.UnprocessedMessage) error {
	if f.failUnprocessed != nil {
		return f.failUnprocessed
	}
	f.unprocessed = append(f.unprocessed, message)
	return nil
}

func newTestProcessor(t *testing.T, page sensor.Page, fake *fakeIngest) (*Processor, *store.Store, *fakeSensor) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "coastal.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sensorClient := &fakeSensor{page: page}
	processor, err := New(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		sensorClient,
		db,
		fake,
		Config{PollInterval: time.Second, PageLimit: 500, MaxStaleness: 15 * time.Minute},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	processor.now = func() time.Time { return pageGeneratedAt.Add(time.Second) }
	return processor, db, sensorClient
}

func validObservation(id string) json.RawMessage {
	return json.RawMessage(`{
		"observation_id":"` + id + `",
		"station_id":"stn-padang-01",
		"observed_at":"2026-11-12T10:29:30Z",
		"critical":false,
		"quality":"ok",
		"sea_level_anomaly_m":0.2,
		"wave_height_m":1.1
	}`)
}

func invalidQualityObservation(id string) json.RawMessage {
	return json.RawMessage(`{
		"observation_id":"` + id + `",
		"station_id":"stn-padang-01",
		"observed_at":"2026-11-12T10:29:30Z",
		"critical":false,
		"quality":"suspect",
		"sea_level_anomaly_m":0.2,
		"wave_height_m":1.1
	}`)
}

func TestPollOnceProcessesValidPage(t *testing.T) {
	fake := &fakeIngest{}
	processor, db, _ := newTestProcessor(t, sensor.Page{
		GeneratedAt: pageGeneratedAt,
		Items:       []json.RawMessage{validObservation("obs-1")},
		NextCursor:  "cursor-2",
	}, fake)

	processor.pollOnce(context.Background())

	if len(fake.upserts) != 1 {
		t.Fatalf("upserts = %d", len(fake.upserts))
	}
	if fake.upserts[0].Event.SourceRefID != "obs-1" || fake.upserts[0].Operation != ingest.OperationUpsert {
		t.Fatalf("command = %+v", fake.upserts[0])
	}
	cursor, err := db.Cursor(context.Background())
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	if cursor != "cursor-2" {
		t.Fatalf("cursor = %q", cursor)
	}
	stats, err := db.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Observations != 1 || stats.OutboxPending != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(fake.statuses) == 0 || fake.statuses[len(fake.statuses)-1].Status != "ok" {
		t.Fatalf("statuses = %+v", fake.statuses)
	}
}

func TestPollOnceQuarantinesInvalidItem(t *testing.T) {
	fake := &fakeIngest{}
	processor, db, _ := newTestProcessor(t, sensor.Page{
		GeneratedAt: pageGeneratedAt,
		Items: []json.RawMessage{
			invalidQualityObservation("obs-bad"),
			validObservation("obs-good"),
		},
		NextCursor: "cursor-2",
	}, fake)

	processor.pollOnce(context.Background())

	if len(fake.upserts) != 1 || fake.upserts[0].Event.SourceRefID != "obs-good" {
		t.Fatalf("upserts = %+v", fake.upserts)
	}
	if len(fake.unprocessed) != 1 {
		t.Fatalf("unprocessed = %+v", fake.unprocessed)
	}
	if !strings.Contains(fake.unprocessed[0].Reason, "contract_violation") {
		t.Fatalf("reason = %q", fake.unprocessed[0].Reason)
	}
	if fake.unprocessed[0].Status != ingest.StatusParked || fake.unprocessed[0].Retryable {
		t.Fatalf("unprocessed = %+v", fake.unprocessed[0])
	}
	cursor, _ := db.Cursor(context.Background())
	if cursor != "cursor-2" {
		t.Fatalf("cursor = %q", cursor)
	}
}

func TestPollOnceDoesNotCommitCursorWhenIngestFails(t *testing.T) {
	fake := &fakeIngest{failUpsert: &ingest.APIError{StatusCode: 503, Code: "unavailable", Message: "down"}}
	processor, db, _ := newTestProcessor(t, sensor.Page{
		GeneratedAt: pageGeneratedAt,
		Items:       []json.RawMessage{validObservation("obs-1")},
		NextCursor:  "cursor-2",
	}, fake)

	processor.pollOnce(context.Background())

	cursor, err := db.Cursor(context.Background())
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	if cursor != "" {
		t.Fatalf("cursor must not advance, got %q", cursor)
	}
	unsent, err := db.HasUnsentOutbox(context.Background())
	if err != nil || !unsent {
		t.Fatalf("unsent outbox = %v, err = %v", unsent, err)
	}
	if len(fake.statuses) == 0 {
		t.Fatal("expected source status")
	}
	last := fake.statuses[len(fake.statuses)-1]
	if last.Status != "degraded" || last.ReasonCode != "ingest_outbox_pending" {
		t.Fatalf("last status = %+v", last)
	}
}

func TestPollOnceCircuitOpenReportsReason(t *testing.T) {
	fake := &fakeIngest{}
	processor, _, sensorClient := newTestProcessor(t, sensor.Page{}, fake)
	sensorClient.err = sensor.ErrCircuitOpen

	processor.pollOnce(context.Background())

	if len(fake.statuses) == 0 {
		t.Fatal("expected source status")
	}
	last := fake.statuses[len(fake.statuses)-1]
	if last.Status != "degraded" || last.ReasonCode != "circuit_open" {
		t.Fatalf("last status = %+v", last)
	}
}

func TestPollOnceSensorFailureReportsStatus(t *testing.T) {
	fake := &fakeIngest{}
	processor, db, sensorClient := newTestProcessor(t, sensor.Page{}, fake)
	sensorClient.err = &sensor.APIError{StatusCode: 503, Code: "unavailable", Message: "down"}

	processor.pollOnce(context.Background())

	cursor, _ := db.Cursor(context.Background())
	if cursor != "" {
		t.Fatalf("cursor = %q", cursor)
	}
	if len(fake.statuses) == 0 || fake.statuses[len(fake.statuses)-1].ReasonCode != "upstream_unavailable" {
		t.Fatalf("statuses = %+v", fake.statuses)
	}
}

func TestPollOnceDeduplicatesObservation(t *testing.T) {
	fake := &fakeIngest{}
	page := sensor.Page{
		GeneratedAt: pageGeneratedAt,
		Items:       []json.RawMessage{validObservation("obs-1")},
		NextCursor:  "cursor-2",
	}
	processor, db, sensorClient := newTestProcessor(t, page, fake)

	processor.pollOnce(context.Background())
	sensorClient.page.NextCursor = "cursor-3"
	processor.pollOnce(context.Background())

	if len(fake.upserts) != 1 {
		t.Fatalf("duplicate observation should not be sent twice, upserts = %d", len(fake.upserts))
	}
	cursor, _ := db.Cursor(context.Background())
	if cursor != "cursor-3" {
		t.Fatalf("cursor = %q", cursor)
	}
}
