package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newTestTracker(staleAfter time.Duration) (*StatusTracker, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)}
	tr := NewStatusTracker([]domain.Source{domain.SourceBMKG, domain.SourcePVMBG}, staleAfter)
	tr.now = clock.now
	return tr, clock
}

func TestStatusUnknownBeforeFirstPoll(t *testing.T) {
	tr, _ := newTestTracker(time.Minute)
	if got := tr.Status(domain.SourcePVMBG); got.Status != StatusUnknown || got.StaleSince != nil {
		t.Fatalf("status = %+v", got)
	}
}

func TestStatusStaleSinceFirstFailureAfterSuccess(t *testing.T) {
	tr, clock := newTestTracker(time.Hour)
	tr.RecordSuccess(domain.SourcePVMBG)
	lastOK := clock.t

	clock.t = clock.t.Add(3 * time.Second)
	firstFail := clock.t
	tr.RecordFailure(domain.SourcePVMBG, errors.New("HTTP 503"))
	clock.t = clock.t.Add(3 * time.Second)
	tr.RecordFailure(domain.SourcePVMBG, errors.New("timeout"))

	got := tr.Status(domain.SourcePVMBG)
	if got.Status != StatusStale {
		t.Fatalf("status = %q", got.Status)
	}
	if !got.StaleSince.Equal(firstFail) {
		t.Fatalf("stale_since = %v, want first failure %v", got.StaleSince, firstFail)
	}
	if !got.LastSuccessAt.Equal(lastOK) || got.LastError != "timeout" {
		t.Fatalf("status = %+v", got)
	}

	// Other sources are unaffected.
	if tr.Status(domain.SourceBMKG).Status != StatusUnknown {
		t.Fatal("BMKG status changed by a PVMBG failure")
	}
}

func TestStatusUnavailableWhenNeverSucceeded(t *testing.T) {
	tr, _ := newTestTracker(time.Hour)
	tr.RecordFailure(domain.SourcePVMBG, errors.New("down since startup"))
	got := tr.Status(domain.SourcePVMBG)
	if got.Status != StatusUnavailable || got.StaleSince == nil || got.LastSuccessAt != nil {
		t.Fatalf("status = %+v", got)
	}
}

func TestStatusRecoversAfterSuccess(t *testing.T) {
	tr, clock := newTestTracker(time.Hour)
	tr.RecordFailure(domain.SourcePVMBG, errors.New("down"))
	clock.t = clock.t.Add(time.Minute)
	tr.RecordSuccess(domain.SourcePVMBG)

	got := tr.Status(domain.SourcePVMBG)
	if got.Status != StatusOK || got.StaleSince != nil {
		t.Fatalf("status = %+v", got)
	}
	// The last error stays visible for operators.
	if got.LastError != "down" {
		t.Fatalf("last_error = %q", got.LastError)
	}
}

func TestStatusStaleWhenNoSuccessForTooLong(t *testing.T) {
	tr, clock := newTestTracker(20 * time.Second)
	tr.RecordSuccess(domain.SourceBMKG)
	lastOK := clock.t
	clock.t = clock.t.Add(21 * time.Second)

	got := tr.Status(domain.SourceBMKG)
	if got.Status != StatusStale || !got.StaleSince.Equal(lastOK.Add(20*time.Second)) {
		t.Fatalf("status = %+v", got)
	}
}

func TestSnapshotKeepsRegistrationOrder(t *testing.T) {
	tr, _ := newTestTracker(time.Minute)
	snap := tr.Snapshot()
	if len(snap) != 2 || snap[0].Source != domain.SourceBMKG || snap[1].Source != domain.SourcePVMBG {
		t.Fatalf("snapshot = %+v", snap)
	}
}

// --- poller integration with the tracker ---

type scriptedClient struct {
	source  domain.Source
	mu      sync.Mutex
	errs    []error // consumed one per Fetch; nil entry = success with one event
	commits int
}

func (c *scriptedClient) Source() domain.Source { return c.source }

func (c *scriptedClient) Fetch(ctx context.Context, _ *time.Time) ([]domain.HazardEvent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var err error
	if len(c.errs) > 0 {
		err, c.errs = c.errs[0], c.errs[1:]
	}
	if err != nil {
		return nil, err
	}
	return []domain.HazardEvent{{SourceRefID: "X", OccurredAt: time.Now()}}, nil
}

func (c *scriptedClient) CommitFetch() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commits++
}

func TestPollerRecordsOutcomes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tr := NewStatusTracker([]domain.Source{domain.SourcePVMBG}, time.Hour)
	client := &scriptedClient{source: domain.SourcePVMBG}
	handleErr := error(nil)
	p := &poller{
		logger:  logger,
		client:  client,
		handle:  func(context.Context, []domain.HazardEvent) error { return handleErr },
		tracker: tr,
	}
	ctx := context.Background()

	p.cycle(ctx)
	if got := tr.Status(domain.SourcePVMBG).Status; got != StatusOK {
		t.Fatalf("after success: %q", got)
	}

	client.errs = []error{&SourceError{Source: domain.SourcePVMBG, Kind: ErrorUnavailable, Err: errors.New("503")}}
	p.cycle(ctx)
	if got := tr.Status(domain.SourcePVMBG).Status; got != StatusStale {
		t.Fatalf("after fetch failure: %q", got)
	}

	p.cycle(ctx)
	if got := tr.Status(domain.SourcePVMBG).Status; got != StatusOK {
		t.Fatalf("after recovery: %q", got)
	}

	// Storing failed: the canonical data did not get fresher.
	handleErr = errors.New("mongo down")
	p.cycle(ctx)
	if got := tr.Status(domain.SourcePVMBG); got.Status != StatusStale || got.LastError != "mongo down" {
		t.Fatalf("after handler failure: %+v", got)
	}
	if client.commits != 2 {
		t.Fatalf("commits = %d, want only the two successfully stored cycles", client.commits)
	}
}
