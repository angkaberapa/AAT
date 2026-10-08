package notify

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/angkaberapa/AAT/services/bnpb-consumer-notifier/internal/consumer"
)

func msg(id string, version int, severity string) consumer.Message {
	return consumer.Message{
		EventID: id, Version: version,
		Hazard: consumer.Hazard{HazardID: id, HazardType: "VOLCANIC", Severity: severity, AreaName: "Gunung Merapi"},
	}
}

func newTestNotifier(t *testing.T, path string) *Notifier {
	t.Helper()
	n, err := New(consumer.NewFileState(path), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNotifiesOnlyFromSiagaAndOnEscalation(t *testing.T) {
	n := newTestNotifier(t, "")
	ctx := context.Background()
	n.Handle(ctx, msg("HZ-1", 1, "WASPADA")) // below threshold
	n.Handle(ctx, msg("HZ-1", 2, "SIAGA"))   // alert
	n.Handle(ctx, msg("HZ-1", 3, "SIAGA"))   // same level, other change: no alert
	n.Handle(ctx, msg("HZ-1", 4, "AWAS"))    // escalation: alert again
	n.Handle(ctx, msg("HZ-2", 1, "NORMAL"))

	if n.state.Sent != 2 || n.state.Recent[0].Severity != "AWAS" || n.state.Processed != 5 {
		t.Fatalf("state = %+v", n.state)
	}
}

func TestRedeliveryNeverAlertsTwice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	ctx := context.Background()
	n := newTestNotifier(t, path)
	n.Handle(ctx, msg("HZ-1", 1, "AWAS"))
	n.Handle(ctx, msg("HZ-1", 1, "AWAS"))
	if err := n.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	restarted := newTestNotifier(t, path)
	restarted.Handle(ctx, msg("HZ-1", 1, "AWAS")) // uncommitted record redelivered after restart
	if restarted.state.Sent != 1 || restarted.state.Duplicates != 2 {
		t.Fatalf("state = %+v", restarted.state)
	}
}
