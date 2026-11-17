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

func TestAnalyticsAgainstMongo(t *testing.T) {
	uri := os.Getenv("MONGO_INTEGRATION_URI")
	if uri == "" {
		t.Skip("MONGO_INTEGRATION_URI is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := New(ctx, logger, uri, "analytics_integration")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(context.Background())                                         //nolint:errcheck -- test cleanup
	defer store.client.Database("analytics_integration").Drop(context.Background()) //nolint:errcheck -- test cleanup

	at := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	events := []any{
		domain.HazardEvent{HazardID: "HZ-1", Source: domain.SourceBMKG, SourceRefID: "eq-1", HazardType: domain.HazardTypeSeismic, Severity: domain.SeverityWaspada, AreaName: "Bandung", OccurredAt: at, IngestedAt: at, Attributes: map[string]any{"potential_tsunami": false}},
		domain.HazardEvent{HazardID: "HZ-2", Source: domain.SourceBMKG, SourceRefID: "eq-2", HazardType: domain.HazardTypeSeismic, Severity: domain.SeveritySiaga, AreaName: "Palu", OccurredAt: at.Add(time.Hour), IngestedAt: at, Attributes: map[string]any{"potential_tsunami": true, "tsunami_warning": map[string]any{"level": "SIAGA"}}},
		domain.HazardEvent{HazardID: "HZ-3", Source: domain.SourcePVMBG, SourceRefID: "vol-1", HazardType: domain.HazardTypeVolcanic, Severity: domain.SeveritySiaga, AreaName: "Bandung", OccurredAt: at.Add(2 * time.Hour), IngestedAt: at, Attributes: map[string]any{}},
	}
	if _, err := store.events.InsertMany(ctx, events); err != nil {
		t.Fatal(err)
	}

	result, err := store.Analytics(ctx, AnalyticsFilter{Bucket: "hour"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 3 || namedCount(result.BySeverity, "SIAGA") != 2 || namedCount(result.ByArea, "Bandung") != 2 {
		t.Fatalf("result = %#v", result)
	}
	if timeCount(result.TimeSeries, "SEISMIC") != 1 || timeCount(result.TimeSeries, "TSUNAMI") != 1 || timeCount(result.TimeSeries, "VOLCANIC") != 1 {
		t.Fatalf("time series = %#v", result.TimeSeries)
	}
}

func TestImportBatchTransactionAgainstReplicaSet(t *testing.T) {
	uri := os.Getenv("MONGO_REPLICA_INTEGRATION_URI")
	if uri == "" {
		t.Skip("MONGO_REPLICA_INTEGRATION_URI is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := New(ctx, logger, uri, "catalogue_import_integration")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())                                                //nolint:errcheck -- test cleanup
	defer st.client.Database("catalogue_import_integration").Drop(context.Background()) //nolint:errcheck -- test cleanup

	at := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	event := domain.HazardEvent{
		HazardID: "HZ-import-1", Source: domain.SourceBMKG, SourceRefID: "catalogue-1",
		HazardType: domain.HazardTypeSeismic, Severity: domain.SeverityWaspada,
		AreaName: "Palu", Latitude: -0.9, Longitude: 119.8,
		OccurredAt: at, IngestedAt: at, Attributes: map[string]any{"magnitude": 6.1},
	}
	first, err := st.ImportBatch(ctx, []domain.HazardEvent{event})
	if err != nil || first[Inserted] != 1 {
		t.Fatalf("first import=%#v err=%v", first, err)
	}
	second, err := st.ImportBatch(ctx, []domain.HazardEvent{event})
	if err != nil || second[Unchanged] != 1 {
		t.Fatalf("second import=%#v err=%v", second, err)
	}
	var count int64
	if count, err = st.events.CountDocuments(ctx, map[string]any{"hazard_id": event.HazardID}); err != nil || count != 1 {
		t.Fatalf("event count=%d err=%v", count, err)
	}
	var outboxCount int64
	if outboxCount, err = st.events.CountDocuments(ctx, map[string]any{"hazard_id": event.HazardID, "outbox.pending": true}); err != nil || outboxCount != 1 {
		t.Fatalf("pending outbox count=%d err=%v", outboxCount, err)
	}
}

func namedCount(counts []NamedCount, name string) int64 {
	for _, count := range counts {
		if count.Name == name {
			return count.Count
		}
	}
	return 0
}

func timeCount(counts []TimeCount, hazardType string) int64 {
	var total int64
	for _, count := range counts {
		if count.HazardType == hazardType {
			total += count.Count
		}
	}
	return total
}
