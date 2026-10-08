package dashboard

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/angkaberapa/AAT/services/bnpb-consumer-dashboard/internal/consumer"
)

func msg(id string, version int, severity string) consumer.Message {
	return consumer.Message{
		EventID: id, Version: version,
		Hazard: consumer.Hazard{HazardID: id, HazardType: "SEISMIC", Severity: severity},
	}
}

func newTestDashboard(t *testing.T, path string) *Dashboard {
	t.Helper()
	d, err := New(consumer.NewFileState(path), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDuplicatesAndOldVersionsAreIgnored(t *testing.T) {
	d := newTestDashboard(t, "")
	ctx := context.Background()
	d.Handle(ctx, msg("HZ-1", 1, "SIAGA"))
	d.Handle(ctx, msg("HZ-1", 2, "AWAS"))
	d.Handle(ctx, msg("HZ-1", 2, "AWAS"))  // redelivery
	d.Handle(ctx, msg("HZ-1", 1, "SIAGA")) // stale duplicate

	v := d.View(10)
	if v.Total != 1 || v.BySeverity["AWAS"] != 1 || v.Stats.Processed != 2 || v.Stats.Duplicates != 2 {
		t.Fatalf("view = %+v", v)
	}
}

func TestStateSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	ctx := context.Background()
	d := newTestDashboard(t, path)
	d.Handle(ctx, msg("HZ-1", 1, "SIAGA"))
	if err := d.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	restarted := newTestDashboard(t, path)
	if restarted.View(10).Total != 1 {
		t.Fatal("state lost after restart")
	}
	// Redelivery of an uncommitted record after the restart is still a no-op.
	restarted.Handle(ctx, msg("HZ-1", 1, "SIAGA"))
	if v := restarted.View(10); v.Stats.Processed != 1 || v.Stats.Duplicates != 1 {
		t.Fatalf("view = %+v", v.Stats)
	}
}
