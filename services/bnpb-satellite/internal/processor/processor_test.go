package processor

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/feed"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/ingest"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/sse"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/store"
)

type fakeStreamer struct {
	from     string
	events   []sse.Event
	err      error
	onStream func()
}

func (f *fakeStreamer) Stream(_ context.Context, from string, handler func(sse.Event) error) error {
	f.from = from
	if f.onStream != nil {
		f.onStream()
	}
	for _, event := range f.events {
		if err := handler(event); err != nil {
			return err
		}
	}
	return f.err
}

type fakeIngest struct {
	upserts []ingest.UpsertHazardCommand
	deletes []ingest.DeleteHazardCommand
}

func (f *fakeIngest) UpsertHazard(_ context.Context, command ingest.UpsertHazardCommand) (ingest.IngestResult, error) {
	f.upserts = append(f.upserts, command)
	return ingest.IngestResult{HazardID: command.Event.HazardID}, nil
}

func (f *fakeIngest) DeleteHazard(_ context.Context, command ingest.DeleteHazardCommand) (ingest.IngestResult, error) {
	f.deletes = append(f.deletes, command)
	return ingest.IngestResult{HazardID: command.HazardID}, nil
}

func (f *fakeIngest) ReportSourceStatus(context.Context, ingest.SourceStatus) error { return nil }

func (f *fakeIngest) StoreUnprocessed(context.Context, ingest.UnprocessedMessage) error { return nil }

func testProcessor(t *testing.T, stream Streamer) (*Processor, *store.Store, *fakeIngest) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "satellite.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ingestClient := &fakeIngest{}
	proc, err := New(logger, stream, db, ingestClient, Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return proc, db, ingestClient
}

func detectionMessage(t *testing.T, offset, satellite, timeValue, latitude, longitude string, frp float64) feed.Message {
	t.Helper()
	payload := map[string]any{
		"satellite":  satellite,
		"acq_date":   "2026-11-12",
		"acq_time":   timeValue,
		"latitude":   latitude,
		"longitude":  longitude,
		"confidence": "h",
		"frp":        frp,
	}
	raw, _ := json.Marshal(payload)
	message, err := feed.Parse("detection", offset, raw)
	if err != nil {
		t.Fatalf("parse detection: %v", err)
	}
	return message
}

func TestTwoSatellitesProduceOneFire(t *testing.T) {
	proc, db, _ := testProcessor(t, &fakeStreamer{})
	ctx := context.Background()

	first := detectionMessage(t, "1", "N20", "0243", "-2.51230", "113.80440", 10)
	second := detectionMessage(t, "2", "N21", "0244", "-2.51235", "113.80445", 5)
	if err := proc.applyDetection(ctx, first); err != nil {
		t.Fatalf("apply first: %v", err)
	}
	if err := proc.applyDetection(ctx, second); err != nil {
		t.Fatalf("apply second: %v", err)
	}

	fires, err := db.ActiveFireIDs(ctx)
	if err != nil {
		t.Fatalf("active fires: %v", err)
	}
	if len(fires) != 1 {
		t.Fatalf("fires = %v, want one fire", fires)
	}
	items, err := db.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("pending outbox: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("outbox = %d items, want 1", len(items))
	}
	var command ingest.UpsertHazardCommand
	if err := json.Unmarshal(items[0].Payload, &command); err != nil {
		t.Fatalf("decode command: %v", err)
	}
	if command.Event.SourceRefID != fires[0] {
		t.Fatalf("source_ref_id = %q, want %q", command.Event.SourceRefID, fires[0])
	}
	if command.Event.Attributes["detection_count"] != float64(2) {
		t.Fatalf("detection_count = %v", command.Event.Attributes["detection_count"])
	}
	offset, _ := db.Offset(ctx)
	if offset != "2" {
		t.Fatalf("offset = %q, want 2", offset)
	}
}

func TestRetractSplitsFireAndDeletesProjection(t *testing.T) {
	proc, db, _ := testProcessor(t, &fakeStreamer{})
	ctx := context.Background()

	if err := proc.applyDetection(ctx, detectionMessage(t, "1", "N20", "0243", "-2.51230", "113.80440", 10)); err != nil {
		t.Fatalf("apply first: %v", err)
	}
	if err := proc.applyDetection(ctx, detectionMessage(t, "2", "N21", "0244", "-2.51235", "113.80445", 5)); err != nil {
		t.Fatalf("apply second: %v", err)
	}
	fires, _ := db.ActiveFireIDs(ctx)
	message, err := feed.Parse("retract", "3", []byte(`{"detection_key":"N21|2026-11-12|0244|-2.51235|113.80445"}`))
	if err != nil {
		t.Fatalf("parse retract: %v", err)
	}
	if err := proc.applyRetract(ctx, message); err != nil {
		t.Fatalf("apply retract: %v", err)
	}

	after, _ := db.ActiveFireIDs(ctx)
	if len(after) != 0 {
		t.Fatalf("fires after retract = %v, want none", after)
	}
	items, _ := db.PendingOutbox(ctx, 10)
	if len(items) != 2 {
		t.Fatalf("outbox = %d, want upsert and delete", len(items))
	}
	var deleteCommand ingest.DeleteHazardCommand
	if err := json.Unmarshal(items[1].Payload, &deleteCommand); err != nil {
		t.Fatalf("decode delete: %v", err)
	}
	if deleteCommand.Operation != ingest.OperationDelete || deleteCommand.SourceRefID != fires[0] {
		t.Fatalf("delete command = %+v", deleteCommand)
	}
}

func TestReplayDoesNotDuplicateOutbox(t *testing.T) {
	proc, db, ingestClient := testProcessor(t, &fakeStreamer{})
	ctx := context.Background()
	message := detectionMessage(t, "1", "N20", "0243", "-2.51230", "113.80440", 10)
	second := detectionMessage(t, "2", "N21", "0244", "-2.51235", "113.80445", 5)
	if err := proc.applyDetection(ctx, message); err != nil {
		t.Fatalf("apply first: %v", err)
	}
	if err := proc.applyDetection(ctx, second); err != nil {
		t.Fatalf("apply second: %v", err)
	}
	// Replay both events from the same offsets, as after a crashed transaction.
	if err := proc.applyDetection(ctx, message); err != nil {
		t.Fatalf("replay first: %v", err)
	}
	if err := proc.applyDetection(ctx, second); err != nil {
		t.Fatalf("replay second: %v", err)
	}
	items, _ := db.PendingOutbox(ctx, 10)
	if len(items) != 1 {
		t.Fatalf("outbox = %d, want one idempotent upsert", len(items))
	}
	proc.flushOutbox(ctx)
	if len(ingestClient.upserts) != 1 {
		t.Fatalf("upserts sent = %d, want 1", len(ingestClient.upserts))
	}
}

func TestLowConfidenceAdvancesOffsetWithoutStoring(t *testing.T) {
	proc, db, _ := testProcessor(t, &fakeStreamer{})
	ctx := context.Background()
	event := sse.Event{
		ID:   "9",
		Type: "detection",
		Data: []byte(`{"satellite":"N20","acq_date":"2026-11-12","acq_time":"0243","latitude":-2.5,"longitude":113.8,"confidence":"low"}`),
	}
	if err := proc.handle(ctx, event); err != nil {
		t.Fatalf("handle: %v", err)
	}
	count, _ := db.DetectionCount(ctx)
	if count != 0 {
		t.Fatalf("detections = %d, want 0", count)
	}
	offset, _ := db.Offset(ctx)
	if offset != "9" {
		t.Fatalf("offset = %q, want 9", offset)
	}
}

func TestConsumeStartsFromCommittedOffset(t *testing.T) {
	stream := &fakeStreamer{}
	proc, db, _ := testProcessor(t, stream)
	ctx := context.Background()
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		return store.SetOffsetTx(ctx, tx, "41")
	}); err != nil {
		t.Fatalf("set offset: %v", err)
	}
	if err := proc.consume(ctx); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if stream.from != "41" {
		t.Fatalf("from = %q, want 41", stream.from)
	}
}
