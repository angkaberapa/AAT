package ingest

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
)

// Handler dipanggil tiap selesai satu poll cycle (store dulu, baru publish). kalau balikin error, cursor gak digeser, jadi record yang sama bakal ke-fetch lagi di cycle berikutnya.
type Handler func(ctx context.Context, events []domain.HazardEvent) error

// Run loop polling sampai ctx di-cancel. tiap client dapet goroutine sendiri, jadi satu sumber yang lambat / mati gak nge-block yang lain.
// Hasil tiap cycle dicatat di tracker (boleh nil) buat penanda kebaruan data (stale_since, P2).
func Run(ctx context.Context, logger *slog.Logger, interval time.Duration, clients []SourceClient, handle Handler, tracker *StatusTracker) {
	var wg sync.WaitGroup

	for _, client := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := &poller{logger: logger, client: client, handle: handle, tracker: tracker}
			p.loop(ctx, interval)
		}()
	}

	wg.Wait()
}

type poller struct {
	logger  *slog.Logger
	client  SourceClient
	handle  Handler
	tracker *StatusTracker

	//since = OccurredAt paling baru yang udah di-handle. cuma diakses dari goroutine poller-nya sendiri, jadi gak perlu lock.
	since *time.Time
}

func (p *poller) loop(ctx context.Context, interval time.Duration) {
	//cycle pertama langsung jalan, gak nunggu ticker.
	p.cycle(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.cycle(ctx)
		}
	}
}

// cycle = satu kali poll satu sumber, dengan correlation id sendiri biar gampang di-trace di log.
func (p *poller) cycle(parent context.Context) {
	ctx := core.WithCorrelationID(parent, core.NewCorrelationID())
	source := string(p.client.Source())

	events, err := p.client.Fetch(ctx, p.since)
	if err != nil {
		//kalau errornya karena ctx di-cancel, jangan log apa-apa. ini shutdown normal, bukan kegagalan.
		if ctx.Err() != nil {
			return
		}
		p.logFetchError(ctx, source, err)
		p.recordFailure(err)
		return
	}

	if len(events) == 0 {
		p.commitFetch()
		p.logger.DebugContext(ctx, "poll cycle: nothing new", "source", source)
		p.recordSuccess()
		return
	}

	if err := p.handle(ctx, events); err != nil {
		if ctx.Err() != nil {
			return
		}
		p.logger.ErrorContext(ctx, "handle events failed, will retry next cycle",
			"source", source, "error", err.Error())
		//data sumber gak berhasil disimpan, jadi canonical data-nya juga gak makin baru.
		p.recordFailure(err)
		return
	}
	p.recordSuccess()
	p.commitFetch()

	//cursor baru digeser setelah handler sukses. kalau digeser duluan, event yang gagal ke-handle bakal hilang selamanya.
	if latest := newestOccurredAt(events); p.since == nil || latest.After(*p.since) {
		p.since = &latest
	}

	p.logger.InfoContext(ctx, "poll cycle done",
		"source", source, "events", len(events))
}

func (p *poller) commitFetch() {
	if committer, ok := p.client.(FetchCommitter); ok {
		committer.CommitFetch()
	}
}

func (p *poller) recordSuccess() {
	if p.tracker != nil {
		p.tracker.RecordSuccess(p.client.Source())
	}
}

func (p *poller) recordFailure(err error) {
	if p.tracker != nil {
		p.tracker.RecordFailure(p.client.Source(), err)
	}
}

func (p *poller) logFetchError(ctx context.Context, source string, err error) {
	attrs := []any{"source", source, "error", err.Error()}

	var se *SourceError
	if errors.As(err, &se) {
		attrs = append(attrs, "kind", string(se.Kind))
		if se.Kind == ErrorUnauthorized {
			p.logger.ErrorContext(ctx, "source rejected our credentials", attrs...)
			return
		}
	}

	p.logger.WarnContext(ctx, "poll failed", attrs...)
}

// dipanggil cuma kalau events gak kosong, jadi aman akses events[0].
func newestOccurredAt(events []domain.HazardEvent) time.Time {
	latest := events[0].OccurredAt
	for _, e := range events[1:] {
		if e.OccurredAt.After(latest) {
			latest = e.OccurredAt
		}
	}
	return latest.UTC()
}