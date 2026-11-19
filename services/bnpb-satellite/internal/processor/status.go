package processor

// Laporan kesehatan Feed Satelit ke Aggregator (POST /internal/ingest/source-status), sesuai
// docs/KONTRAK_INTERNAL_INGEST.md bagian 4. Ditulis dengan bantuan LLM (Claude).
//
// Dikirim tiap statusEvery (lebih sering dari batas stale Aggregator), jadi kalau service ini
// sendiri mati, Aggregator melihat laporan berhenti dan menandai SATELIT stale.

import (
	"context"
	"errors"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/ingest"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/sse"
)

const (
	statusEvery = 5 * time.Second
	// Feed mengirim heartbeat; tanpa event apa pun selama ini, stream dianggap terputus.
	feedSilentAfter = 30 * time.Second
)

// streamState dilindungi p.mu.
type streamState struct {
	lastEventAt  time.Time // event terakhir dari feed (termasuk heartbeat)
	reconnecting bool
	lastErr      error
}

func (p *Processor) markFeedEvent() {
	p.mu.Lock()
	p.feed.lastEventAt = p.now()
	p.feed.reconnecting = false
	p.feed.lastErr = nil
	p.mu.Unlock()
}

func (p *Processor) markStreamEnded(err error) {
	p.mu.Lock()
	p.feed.reconnecting = true
	p.feed.lastErr = err
	p.mu.Unlock()
}

func (p *Processor) currentStatus() ingest.SourceStatus {
	p.mu.Lock()
	state := p.feed
	p.mu.Unlock()

	status := ingest.SourceStatus{Source: sourceName}
	if !state.lastEventAt.IsZero() && p.now().Sub(state.lastEventAt) <= feedSilentAfter {
		status.Status = "ok"
		last := state.lastEventAt.UTC()
		status.LastEventAt = &last
		return status
	}
	status.Status = "degraded"
	status.ReasonCode = "disconnected"
	if state.reconnecting {
		status.ReasonCode = "reconnecting"
	}
	var apiErr *sse.APIError
	if errors.As(state.lastErr, &apiErr) {
		// Feed menolak (token, batas koneksi, sedang tidak tersedia): tidak akan pulih hanya
		// dengan reconnect biasa.
		status.Status = "unavailable"
		status.ReasonCode = "upstream_unavailable"
	}
	if state.lastErr != nil {
		status.Details = map[string]any{"error": state.lastErr.Error()}
	}
	return status
}

func (p *Processor) statusLoop(ctx context.Context) {
	ticker := time.NewTicker(statusEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reportCtx, cancel := context.WithTimeout(ctx, statusEvery)
			if err := p.ingest.ReportSourceStatus(reportCtx, p.currentStatus()); err != nil && ctx.Err() == nil {
				p.logger.WarnContext(ctx, "report source status failed", "error", err.Error())
			}
			cancel()
		}
	}
}
