package store

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
)

func TestIngestHazardAgainstMongo(t *testing.T) {
	uri := os.Getenv("MONGO_INTEGRATION_URI")
	if uri == "" {
		t.Skip("MONGO_INTEGRATION_URI is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := New(ctx, logger, uri, "ingest_integration")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())                                      //nolint:errcheck -- test cleanup
	defer db.client.Database("ingest_integration").Drop(context.Background()) //nolint:errcheck -- test cleanup

	at := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	event := domain.HazardEvent{
		HazardID:    domain.NewHazardID(domain.SourceSatelit, "fire-1"),
		Source:      domain.SourceSatelit,
		SourceRefID: "fire-1",
		HazardType:  domain.HazardTypeWildfire,
		Severity:    domain.SeverityWaspada,
		AreaName:    "Kalimantan Tengah",
		Latitude:    -2.5,
		Longitude:   113.8,
		OccurredAt:  at,
		IngestedAt:  at,
		Attributes:  map[string]any{"detection_count": 2},
	}

	result, err := db.IngestHazard(ctx, event)
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if result != Inserted {
		t.Fatalf("first result = %q, want inserted", result)
	}

	result, err = db.IngestHazard(ctx, event)
	if err != nil {
		t.Fatalf("repeat ingest: %v", err)
	}
	if result != Unchanged {
		t.Fatalf("repeat result = %q, want unchanged", result)
	}
	pending, err := db.Pending(ctx, 10)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 1 || pending[0].Version != 1 {
		t.Fatalf("pending = %#v", pending)
	}

	event.Severity = domain.SeveritySiaga
	result, err = db.IngestHazard(ctx, event)
	if err != nil {
		t.Fatalf("update ingest: %v", err)
	}
	if result != Updated {
		t.Fatalf("update result = %q, want updated", result)
	}

	checksum, err := db.AuditChecksum(ctx, domain.SourceSatelit)
	if err != nil {
		t.Fatalf("checksum: %v", err)
	}
	if checksum.Count != 1 {
		t.Fatalf("checksum count = %d, want 1", checksum.Count)
	}

	deleted, err := db.DeleteHazard(ctx, event.HazardID)
	if err != nil || !deleted {
		t.Fatalf("delete = %v, %v", deleted, err)
	}
	deleted, err = db.DeleteHazard(ctx, event.HazardID)
	if err != nil || deleted {
		t.Fatalf("repeat delete = %v, %v", deleted, err)
	}
	stored, err := db.Get(ctx, event.HazardID)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if stored != nil {
		t.Fatalf("event still stored: %#v", stored)
	}
	checksum, err = db.AuditChecksum(ctx, domain.SourceSatelit)
	if err != nil {
		t.Fatalf("checksum after delete: %v", err)
	}
	if checksum.Count != 0 {
		t.Fatalf("checksum count after delete = %d", checksum.Count)
	}
}
