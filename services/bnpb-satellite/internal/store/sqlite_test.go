package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/domain"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "satellite.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func floatPtr(value float64) *float64 { return &value }

func testDetection(key string) domain.Detection {
	return domain.Detection{
		Key:        key,
		Satellite:  "N20",
		AcqDate:    "2026-11-12",
		AcqTime:    "0243",
		Latitude:   "-2.51230",
		Longitude:  "113.80440",
		Confidence: "h",
		FRP:        floatPtr(12.5),
		ObservedAt: time.Date(2026, 11, 12, 2, 43, 0, 0, time.UTC),
		Active:     true,
		RawJSON:    `{"satellite":"N20"}`,
	}
}

func TestOffsetRoundTrip(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	offset, err := db.Offset(ctx)
	if err != nil || offset != "" {
		t.Fatalf("initial offset = %q, err = %v", offset, err)
	}
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		return SetOffsetTx(ctx, tx, "1872")
	}); err != nil {
		t.Fatalf("SetOffsetTx: %v", err)
	}
	offset, err = db.Offset(ctx)
	if err != nil || offset != "1872" {
		t.Fatalf("offset = %q, err = %v", offset, err)
	}
}

func TestDetectionInsertAndDeactivate(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	detection := testDetection("N20|2026-11-12|0243|-2.51230|113.80440")

	var inserted bool
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		inserted, err = InsertDetectionTx(ctx, tx, detection)
		return err
	}); err != nil {
		t.Fatalf("InsertDetectionTx: %v", err)
	}
	if !inserted {
		t.Fatal("first insert should be new")
	}
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		inserted, err = InsertDetectionTx(ctx, tx, detection)
		return err
	}); err != nil {
		t.Fatalf("second InsertDetectionTx: %v", err)
	}
	if inserted {
		t.Fatal("duplicate detection should be ignored")
	}
	count, err := db.DetectionCount(ctx)
	if err != nil || count != 1 {
		t.Fatalf("count = %d, err = %v", count, err)
	}
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		return DeactivateDetectionTx(ctx, tx, detection.Key)
	}); err != nil {
		t.Fatalf("DeactivateDetectionTx: %v", err)
	}
}

func TestFireUpsertAndDelete(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	fire := domain.Fire{
		FireID:            "fire-1",
		EventJSON:         []byte(`{"hazard_id":"HZ-1"}`),
		FirstDetectionKey: "N20|2026-11-12|0243|-2.51230|113.80440",
		LastDetectionAt:   time.Date(2026, 11, 12, 2, 43, 0, 0, time.UTC),
		DetectionCount:    2,
		TotalFRP:          25.0,
		Active:            true,
	}

	var version int
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		version, err = UpsertFireTx(ctx, tx, fire)
		return err
	}); err != nil {
		t.Fatalf("UpsertFireTx: %v", err)
	}
	if version != 1 {
		t.Fatalf("version = %d", version)
	}

	fire.EventJSON = []byte(`{"hazard_id":"HZ-1","severity":"SIAGA"}`)
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		version, err = UpsertFireTx(ctx, tx, fire)
		return err
	}); err != nil {
		t.Fatalf("second UpsertFireTx: %v", err)
	}
	if version != 2 {
		t.Fatalf("version = %d", version)
	}
	stored, err := db.Fire(ctx, fire.FireID)
	if err != nil || stored == nil {
		t.Fatalf("Fire: %v, %v", stored, err)
	}
	if stored.Version != 2 || !stored.Active {
		t.Fatalf("stored = %+v", stored)
	}

	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		version, err = DeleteFireTx(ctx, tx, fire.FireID)
		return err
	}); err != nil {
		t.Fatalf("DeleteFireTx: %v", err)
	}
	if version != 3 {
		t.Fatalf("version after delete = %d", version)
	}
	stored, err = db.Fire(ctx, fire.FireID)
	if err != nil || stored == nil || stored.Active {
		t.Fatalf("stored after delete = %+v, err = %v", stored, err)
	}
}

func TestOutboxLifecycle(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	item := OutboxItem{
		ProducerEventID: "satellite-offset-1872-fire-1",
		Operation:       "UPSERT",
		SourcePosition:  "1872",
		Payload:         []byte(`{"operation":"UPSERT"}`),
	}
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := EnqueueOutboxTx(ctx, tx, item)
		return err
	}); err != nil {
		t.Fatalf("EnqueueOutboxTx: %v", err)
	}
	pending, err := db.PendingOutbox(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %v, err = %v", pending, err)
	}
	if err := db.MarkOutboxSent(ctx, pending[0].ID); err != nil {
		t.Fatalf("MarkOutboxSent: %v", err)
	}
	pending, err = db.PendingOutbox(ctx, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after sent = %v, err = %v", pending, err)
	}
	unsent, err := db.HasUnsentOutbox(ctx)
	if err != nil || unsent {
		t.Fatalf("unsent = %v, err = %v", unsent, err)
	}
}

func TestOutboxFailureBlocksCursor(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := EnqueueOutboxTx(ctx, tx, OutboxItem{
			ProducerEventID: "evt-1",
			Operation:       "UPSERT",
			SourcePosition:  "1872",
			Payload:         []byte(`{}`),
		})
		return err
	}); err != nil {
		t.Fatalf("EnqueueOutboxTx: %v", err)
	}
	pending, _ := db.PendingOutbox(ctx, 10)
	if err := db.MarkOutboxFailed(ctx, pending[0].ID, "503", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("MarkOutboxFailed: %v", err)
	}
	unsent, err := db.HasUnsentOutbox(ctx)
	if err != nil || !unsent {
		t.Fatalf("failed outbox must still block cursor: unsent = %v, err = %v", unsent, err)
	}
}
