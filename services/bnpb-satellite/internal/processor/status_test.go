package processor

// Ditulis dengan bantuan LLM (Claude).

import (
	"errors"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/sse"
)

func TestCurrentStatus(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	p := &Processor{now: func() time.Time { return now }}

	if st := p.currentStatus(); st.Status != "degraded" || st.ReasonCode != "disconnected" {
		t.Fatalf("before any event: %+v", st)
	}

	p.markFeedEvent()
	if st := p.currentStatus(); st.Status != "ok" || st.LastEventAt == nil {
		t.Fatalf("after event: %+v", st)
	}

	p.markStreamEnded(errors.New("read stream: EOF"))
	now = now.Add(feedSilentAfter + time.Second)
	if st := p.currentStatus(); st.Status != "degraded" || st.ReasonCode != "reconnecting" {
		t.Fatalf("stream dropped: %+v", st)
	}

	p.markStreamEnded(&sse.APIError{StatusCode: 503, Message: "unavailable"})
	if st := p.currentStatus(); st.Status != "unavailable" || st.ReasonCode != "upstream_unavailable" {
		t.Fatalf("feed rejected: %+v", st)
	}
}
