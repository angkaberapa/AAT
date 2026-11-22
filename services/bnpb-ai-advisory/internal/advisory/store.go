package advisory

import (
	"sync"

	"github.com/angkaberapa/AAT/services/bnpb-ai-advisory/internal/detector"
)

// Store keeps the most recent advisories in memory. Advisories are not part of
// the canonical store and are intentionally lossy across restarts: they are a
// derived, best-effort signal, not an alert of record.
type Store struct {
	mu    sync.RWMutex
	items []detector.Advisory
	max   int
}

func NewStore(max int) *Store {
	if max <= 0 {
		max = 200
	}
	return &Store{max: max}
}

func (s *Store) Add(item detector.Advisory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, item)
	if len(s.items) > s.max {
		s.items = s.items[len(s.items)-s.max:]
	}
}

// List returns the newest advisories first.
func (s *Store) List(limit int) []detector.Advisory {
	if limit <= 0 {
		limit = 20
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit > len(s.items) {
		limit = len(s.items)
	}
	out := make([]detector.Advisory, 0, limit)
	for i := len(s.items) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.items[i])
	}
	return out
}

func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}
