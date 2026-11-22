package advisory

import (
	"sync"
	"time"
)

// Event is the small slice of a hazard event needed for a situation summary.
type Event struct {
	Source     string    `json:"source"`
	HazardType string    `json:"hazard_type"`
	Severity   string    `json:"severity"`
	AreaName   string    `json:"area_name"`
	OccurredAt time.Time `json:"occurred_at"`
}

// RecentEvents keeps the newest events in memory for the summary endpoint.
// It is not a store of record; losing it on restart is harmless.
type RecentEvents struct {
	mu    sync.RWMutex
	items []Event
	max   int
}

func NewRecentEvents(max int) *RecentEvents {
	if max <= 0 {
		max = 20
	}
	return &RecentEvents{max: max}
}

func (r *RecentEvents) Add(event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, event)
	if len(r.items) > r.max {
		r.items = r.items[len(r.items)-r.max:]
	}
}

// List returns newest first.
func (r *RecentEvents) List(limit int) []Event {
	if limit <= 0 || limit > r.max {
		limit = r.max
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if limit > len(r.items) {
		limit = len(r.items)
	}
	out := make([]Event, 0, limit)
	for i := len(r.items) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, r.items[i])
	}
	return out
}
