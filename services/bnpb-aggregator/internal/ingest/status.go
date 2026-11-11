package ingest

import (
	"sync"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
)

// Status values reported per source.
const (
	StatusUnknown     = "unknown"     // no poll has finished yet since startup
	StatusOK          = "ok"          // last poll succeeded recently
	StatusStale       = "stale"       // had fresh data before, polls are now failing
	StatusUnavailable = "unavailable" // polls are failing and none ever succeeded
)

// SourceStatus describes how fresh the canonical data from one source is.
//
// StaleSince is the moment the data stopped being refreshed: the first failed poll
// after the last successful one, or LastSuccessAt+staleAfter when polls stopped
// finishing at all. Data stored before that moment is still served.
type SourceStatus struct {
	Source        domain.Source `json:"source"`
	Status        string        `json:"status"`
	LastSuccessAt *time.Time    `json:"last_success_at,omitempty"`
	LastErrorAt   *time.Time    `json:"last_error_at,omitempty"`
	LastError     string        `json:"last_error,omitempty"`
	StaleSince    *time.Time    `json:"stale_since,omitempty"`
}

// StatusTracker records poll outcomes per source. Safe for concurrent use: pollers
// write from their own goroutines while HTTP handlers read.
type StatusTracker struct {
	mu         sync.RWMutex
	staleAfter time.Duration
	now        func() time.Time
	states     map[domain.Source]*sourceState
	order      []domain.Source
	onChange   func(Record) // P2 M2: dipanggil (di luar lock) tiap kali status satu sumber berubah
}

type sourceState struct {
	lastSuccess  time.Time
	lastError    time.Time
	lastErrorMsg string
	failingSince time.Time // first failure after the last success; zero while healthy
}

// NewStatusTracker tracks the given sources. A source with no successful poll for
// longer than staleAfter is reported stale even if no failure was recorded (e.g. a
// poll that keeps hanging).
func NewStatusTracker(sources []domain.Source, staleAfter time.Duration) *StatusTracker {
	t := &StatusTracker{
		staleAfter: staleAfter,
		now:        time.Now,
		states:     make(map[domain.Source]*sourceState, len(sources)),
	}
	for _, s := range sources {
		t.states[s] = &sourceState{}
		t.order = append(t.order, s)
	}
	return t
}

func (t *StatusTracker) RecordSuccess(source domain.Source) {
	t.mu.Lock()
	st := t.state(source)
	st.lastSuccess = t.now().UTC()
	st.failingSince = time.Time{}
	rec, notify := st.record(source), t.onChange
	t.mu.Unlock()
	if notify != nil {
		notify(rec)
	}
}

func (t *StatusTracker) RecordFailure(source domain.Source, err error) {
	t.mu.Lock()
	st := t.state(source)
	now := t.now().UTC()
	st.lastError = now
	st.lastErrorMsg = err.Error()
	if st.failingSince.IsZero() {
		st.failingSince = now
	}
	rec, notify := st.record(source), t.onChange
	t.mu.Unlock()
	if notify != nil {
		notify(rec)
	}
}

// Dengan ≥3 instance hanya leader yang polling, jadi status mentah tiap sumber disimpan di
// storage (lewat OnChange) dan dibaca semua instance (SharedStatus). Leader baru memulihkan
// status terakhir (Restore) supaya stale_since tidak ter-reset saat failover.

// Record = keadaan mentah poll satu sumber, cukup untuk menghitung ulang SourceStatus.
type Record struct {
	Source       domain.Source
	LastSuccess  time.Time
	LastError    time.Time
	LastErrorMsg string
	FailingSince time.Time
}

func (st *sourceState) record(source domain.Source) Record {
	return Record{
		Source:       source,
		LastSuccess:  st.lastSuccess,
		LastError:    st.lastError,
		LastErrorMsg: st.lastErrorMsg,
		FailingSince: st.failingSince,
	}
}

// OnChange memasang fn yang dipanggil setiap RecordSuccess/RecordFailure.
func (t *StatusTracker) OnChange(fn func(Record)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onChange = fn
}

// Restore memuat status yang tersimpan (dari leader sebelumnya). Tidak memanggil OnChange.
func (t *StatusTracker) Restore(records []Record) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, r := range records {
		st := t.state(r.Source)
		st.lastSuccess = r.LastSuccess
		st.lastError = r.LastError
		st.lastErrorMsg = r.LastErrorMsg
		st.failingSince = r.FailingSince
	}
}

// Describe menghitung SourceStatus dari Record pada waktu now (aturan sama dengan tracker).
func Describe(r Record, staleAfter time.Duration, now time.Time) SourceStatus {
	t := StatusTracker{staleAfter: staleAfter}
	st := sourceState{
		lastSuccess:  r.LastSuccess,
		lastError:    r.LastError,
		lastErrorMsg: r.LastErrorMsg,
		failingSince: r.FailingSince,
	}
	return t.describe(r.Source, &st, now)
}

// Status returns the current status of one source.
func (t *StatusTracker) Status(source domain.Source) SourceStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	st, ok := t.states[source]
	if !ok {
		return SourceStatus{Source: source, Status: StatusUnknown}
	}
	return t.describe(source, st, t.now().UTC())
}

// Snapshot returns the status of every tracked source, in registration order.
func (t *StatusTracker) Snapshot() []SourceStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	now := t.now().UTC()
	out := make([]SourceStatus, 0, len(t.order))
	for _, s := range t.order {
		out = append(out, t.describe(s, t.states[s], now))
	}
	return out
}

func (t *StatusTracker) state(source domain.Source) *sourceState {
	st, ok := t.states[source]
	if !ok {
		st = &sourceState{}
		t.states[source] = st
		t.order = append(t.order, source)
	}
	return st
}

func (t *StatusTracker) describe(source domain.Source, st *sourceState, now time.Time) SourceStatus {
	out := SourceStatus{Source: source, Status: StatusOK}
	if !st.lastSuccess.IsZero() {
		out.LastSuccessAt = timePtr(st.lastSuccess)
	}
	if !st.lastError.IsZero() {
		out.LastErrorAt = timePtr(st.lastError)
		out.LastError = st.lastErrorMsg
	}

	switch {
	case !st.failingSince.IsZero() && st.lastSuccess.IsZero():
		// Nothing fetched since this process started. Data stored by an earlier run
		// may still be served; it is at least as old as the first failure.
		out.Status = StatusUnavailable
		out.StaleSince = timePtr(st.failingSince)
	case !st.failingSince.IsZero():
		out.Status = StatusStale
		out.StaleSince = timePtr(st.failingSince)
	case st.lastSuccess.IsZero():
		out.Status = StatusUnknown
	case t.staleAfter > 0 && now.Sub(st.lastSuccess) > t.staleAfter:
		out.Status = StatusStale
		out.StaleSince = timePtr(st.lastSuccess.Add(t.staleAfter))
	}
	return out
}

func timePtr(t time.Time) *time.Time { return &t }
