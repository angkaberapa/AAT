package dashboard

import (
	"context"
	"fmt"
	"os"
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

// Ditulis dengan bantuan LLM (Claude).
func TestLoadsLegacyM1StateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	legacy := `{"hazards":{
	  "HZ-1":{"hazard":{"hazard_id":"HZ-1","hazard_type":"SEISMIC","severity":"SIAGA"},"version":2,"received_at":"2026-11-01T10:00:00Z"},
	  "HZ-2":{"hazard":{"hazard_id":"HZ-2","hazard_type":"COASTAL","severity":"AWAS"},"version":1,"received_at":"2026-11-01T11:00:00Z"}
	},"processed":3,"duplicates_ignored":1}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	d := newTestDashboard(t, path)
	v := d.View(10)
	if v.Total != 2 || v.ByType["COASTAL"] != 1 || v.BySeverity["SIAGA"] != 1 || v.Stats.Processed != 3 {
		t.Fatalf("view = %+v", v)
	}
	if len(v.Latest) != 2 || v.Latest[0].Hazard.HazardID != "HZ-2" {
		t.Fatalf("latest = %+v", v.Latest)
	}
	// Dedupe tetap jalan dari ringkasan hasil migrasi.
	d.Handle(context.Background(), msg("HZ-1", 2, "SIAGA"))
	if d.View(10).Stats.Duplicates != 2 {
		t.Fatal("redelivered version should be ignored after migration")
	}
}

func TestLatestIsBoundedAndNewestFirst(t *testing.T) {
	d := newTestDashboard(t, "")
	for i := 0; i < keepLatest+20; i++ {
		d.Handle(context.Background(), msg(fmt.Sprintf("HZ-%d", i), 1, "WASPADA"))
	}
	if len(d.state.Latest) != keepLatest || d.View(10).Total != keepLatest+20 {
		t.Fatalf("latest=%d total=%d", len(d.state.Latest), d.View(10).Total)
	}
}
