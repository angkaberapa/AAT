package ingest

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
)

// SharedStatus = StatusProvider yang membaca status sumber dari storage bersama, sehingga
// leader maupun follower menjawab meta.sources / stale_since yang sama (P2 M2). Hasil baca
// di-cache sebentar supaya tiap request API tidak menambah satu query ke storage.
type SharedStatus struct {
	load       func(ctx context.Context) ([]Record, error)
	sources    []domain.Source
	staleAfter time.Duration
	ttl        time.Duration
	logger     *slog.Logger
	now        func() time.Time

	mu       sync.Mutex
	cached   map[domain.Source]Record
	loadedAt time.Time
}

func NewSharedStatus(load func(ctx context.Context) ([]Record, error), sources []domain.Source,
	staleAfter, ttl time.Duration, logger *slog.Logger) *SharedStatus {
	return &SharedStatus{
		load: load, sources: sources, staleAfter: staleAfter, ttl: ttl,
		logger: logger, now: time.Now,
	}
}

func (s *SharedStatus) records() map[domain.Source]Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != nil && s.now().Sub(s.loadedAt) < s.ttl {
		return s.cached
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	recs, err := s.load(ctx)
	if err != nil {
		// Storage tidak terjangkau: pakai salinan terakhir (umurnya tetap dihitung dari
		// LastSuccess, jadi lama-lama tetap terlihat stale), jangan gagalkan request.
		s.logger.Warn("read shared source status failed", "error", err.Error())
		if s.cached == nil {
			s.cached = map[domain.Source]Record{}
		}
		s.loadedAt = s.now()
		return s.cached
	}
	m := make(map[domain.Source]Record, len(recs))
	for _, r := range recs {
		m[r.Source] = r
	}
	s.cached, s.loadedAt = m, s.now()
	return m
}

func (s *SharedStatus) Status(source domain.Source) SourceStatus {
	r, ok := s.records()[source]
	if !ok {
		return SourceStatus{Source: source, Status: StatusUnknown}
	}
	return Describe(r, s.staleAfter, s.now().UTC())
}

func (s *SharedStatus) Snapshot() []SourceStatus {
	recs := s.records()
	now := s.now().UTC()
	out := make([]SourceStatus, 0, len(s.sources))
	for _, src := range s.sources {
		r, ok := recs[src]
		if !ok {
			out = append(out, SourceStatus{Source: src, Status: StatusUnknown})
			continue
		}
		out = append(out, Describe(r, s.staleAfter, now))
	}
	return out
}
