package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/domain"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "coastal.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func floatPtr(value float64) *float64 { return &value }

func testObservation(id string) domain.Observation {
	return domain.Observation{
		ObservationID:    id,
		StationID:        "stn-padang-01",
		ObservedAt:       time.Date(2026, 11, 12, 10, 30, 0, 0, time.UTC),
		ReceivedAt:       time.Date(2026, 11, 12, 10, 30, 1, 0, time.UTC),
		GeneratedAt:      time.Date(2026, 11, 12, 10, 30, 2, 0, time.UTC),
		Critical:         true,
		Quality:          "ok",
		SeaLevelAnomalyM: floatPtr(1.25),
		WaveHeightM:      floatPtr(1.24),
		RawJSON:          `{"observation_id":"` + id + `"}`,
	}
}

func TestCursorRoundTrip(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()

	cursor, err := db.Cursor(ctx)
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	if cursor != "" {
		t.Fatalf("initial cursor = %q", cursor)
	}

	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		return SetCursorTx(ctx, tx, "cursor-1")
	}); err != nil {
		t.Fatalf("SetCursorTx: %v", err)
	}

	cursor, err = db.Cursor(ctx)
	if err != nil {
		t.Fatalf("Cursor after set: %v", err)
	}
	if cursor != "cursor-1" {
		t.Fatalf("cursor = %q", cursor)
	}
}

func TestInsertObservationIsIdempotent(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	observation := testObservation("obs-1")

	var inserted bool
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		inserted, err = InsertObservationTx(ctx, tx, observation)
		return err
	}); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if !inserted {
		t.Fatal("first insert should be new")
	}

	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		inserted, err = InsertObservationTx(ctx, tx, observation)
		return err
	}); err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if inserted {
		t.Fatal("second insert should be ignored")
	}

	stats, err := db.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Observations != 1 {
		t.Fatalf("observations = %d", stats.Observations)
	}
}

func TestOutboxLifecycle(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	item := OutboxItem{
		ProducerEventID: "coastal-observation-1",
		Operation:       "UPSERT",
		SourcePosition:  "cursor-1",
		Payload:         []byte(`{"operation":"UPSERT"}`),
	}

	var inserted bool
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		inserted, err = EnqueueOutboxTx(ctx, tx, item)
		return err
	}); err != nil {
		t.Fatalf("EnqueueOutboxTx: %v", err)
	}
	if !inserted {
		t.Fatal("first enqueue should be new")
	}

	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		inserted, err = EnqueueOutboxTx(ctx, tx, item)
		return err
	}); err != nil {
		t.Fatalf("second EnqueueOutboxTx: %v", err)
	}
	if inserted {
		t.Fatal("duplicate producer_event_id should be ignored")
	}

	pending, err := db.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("PendingOutbox: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d", len(pending))
	}
	if pending[0].ProducerEventID != item.ProducerEventID || string(pending[0].Payload) != string(item.Payload) {
		t.Fatalf("pending item = %+v", pending[0])
	}

	if err := db.MarkOutboxSent(ctx, pending[0].ID); err != nil {
		t.Fatalf("MarkOutboxSent: %v", err)
	}
	pending, err = db.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("PendingOutbox after sent: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after sent = %d", len(pending))
	}
}

func TestOutboxFailureSchedulesRetry(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := EnqueueOutboxTx(ctx, tx, OutboxItem{
			ProducerEventID: "evt-1",
			Operation:       "UPSERT",
			SourcePosition:  "cursor-1",
			Payload:         []byte(`{}`),
		})
		return err
	}); err != nil {
		t.Fatalf("EnqueueOutboxTx: %v", err)
	}

	pending, err := db.PendingOutbox(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %v, err = %v", pending, err)
	}
	if err := db.MarkOutboxFailed(ctx, pending[0].ID, "503", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("MarkOutboxFailed: %v", err)
	}
	pending, err = db.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("PendingOutbox: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("failed item should not be pending before retry: %d", len(pending))
	}
	unsent, err := db.HasUnsentOutbox(ctx)
	if err != nil {
		t.Fatalf("HasUnsentOutbox: %v", err)
	}
	if !unsent {
		t.Fatal("failed item must still block cursor commit")
	}
}

func TestQuarantineLifecycle(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	item := QuarantineItem{
		MessageID:        "coastal-observation-1",
		SourcePosition:   "cursor-1",
		ReceivedAt:       time.Now().UTC(),
		Reason:           "contract_violation: quality is suspect",
		Retryable:        false,
		Status:           "parked",
		RawPayloadBase64: "e30=",
		ContentType:      "application/json",
	}

	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := EnqueueQuarantineTx(ctx, tx, item)
		return err
	}); err != nil {
		t.Fatalf("EnqueueQuarantineTx: %v", err)
	}

	pending, err := db.PendingQuarantine(ctx, 10)
	if err != nil {
		t.Fatalf("PendingQuarantine: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d", len(pending))
	}
	if pending[0].MessageID != item.MessageID || pending[0].Reason != item.Reason || pending[0].Retryable {
		t.Fatalf("pending item = %+v", pending[0])
	}

	if err := db.MarkQuarantineSent(ctx, item.MessageID); err != nil {
		t.Fatalf("MarkQuarantineSent: %v", err)
	}
	pending, err = db.PendingQuarantine(ctx, 10)
	if err != nil {
		t.Fatalf("PendingQuarantine after sent: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after sent = %d", len(pending))
	}
}

func TestLastEventAt(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()

	last, err := db.LastEventAt(ctx)
	if err != nil {
		t.Fatalf("LastEventAt empty: %v", err)
	}
	if last != nil {
		t.Fatalf("last = %v", last)
	}

	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := InsertObservationTx(ctx, tx, testObservation("obs-1"))
		return err
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	last, err = db.LastEventAt(ctx)
	if err != nil {
		t.Fatalf("LastEventAt: %v", err)
	}
	if last == nil || !last.Equal(time.Date(2026, 11, 12, 10, 30, 0, 0, time.UTC)) {
		t.Fatalf("last = %v", last)
	}
}
