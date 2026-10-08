package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

type fakeOutbox struct {
	pending   []store.OutboxEntry
	published map[string]int
	markErr   error
}

func (o *fakeOutbox) Pending(_ context.Context, limit int) ([]store.OutboxEntry, error) {
	if len(o.pending) < limit {
		limit = len(o.pending)
	}
	return append([]store.OutboxEntry(nil), o.pending[:limit]...), nil
}

func (o *fakeOutbox) MarkPublished(_ context.Context, id string, version int) error {
	if o.markErr != nil {
		return o.markErr
	}
	o.published[id] = version
	for i, e := range o.pending {
		if e.Event.HazardID == id && e.Version == version {
			o.pending = append(o.pending[:i], o.pending[i+1:]...)
			break
		}
	}
	return nil
}

type fakeProducer struct {
	records []Record
	err     error
	calls   int
}

func (p *fakeProducer) Publish(_ context.Context, records []Record) error {
	p.calls++
	if p.err != nil {
		return p.err
	}
	p.records = append(p.records, records...)
	return nil
}

func entry(id string, version int) store.OutboxEntry {
	return store.OutboxEntry{
		Event:         domain.HazardEvent{HazardID: id, Severity: domain.SeveritySiaga},
		Version:       version,
		CorrelationID: "poll-" + id,
	}
}

func newTestRelay(o Outbox, p Producer, batch int) *Relay {
	r := NewRelay(o, p, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, batch)
	r.now = func() time.Time { return time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC) }
	return r
}

func TestPublishesPendingAndMarksThem(t *testing.T) {
	outbox := &fakeOutbox{pending: []store.OutboxEntry{entry("HZ-1", 1), entry("HZ-2", 3)}, published: map[string]int{}}
	producer := &fakeProducer{}

	n, err := newTestRelay(outbox, producer, 10).PublishOnce(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
	if outbox.published["HZ-1"] != 1 || outbox.published["HZ-2"] != 3 || len(outbox.pending) != 0 {
		t.Fatalf("published = %v, pending = %v", outbox.published, outbox.pending)
	}

	rec := producer.records[1]
	if rec.Key != "HZ-2" || rec.CorrelationID != "poll-HZ-2" {
		t.Fatalf("record = %+v", rec)
	}
	var msg Message
	if err := json.Unmarshal(rec.Value, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.EventID != "HZ-2:v3" || msg.Type != TypeUpdated || msg.Version != 3 || msg.Hazard.HazardID != "HZ-2" {
		t.Fatalf("message = %+v", msg)
	}
	var first Message
	_ = json.Unmarshal(producer.records[0].Value, &first)
	if first.Type != TypeCreated {
		t.Fatalf("version 1 should be %q, got %q", TypeCreated, first.Type)
	}
}

func TestBrokerFailureKeepsEventsPending(t *testing.T) {
	outbox := &fakeOutbox{pending: []store.OutboxEntry{entry("HZ-1", 1)}, published: map[string]int{}}
	producer := &fakeProducer{err: errors.New("kafka down")}

	if _, err := newTestRelay(outbox, producer, 10).PublishOnce(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if len(outbox.pending) != 1 || len(outbox.published) != 0 {
		t.Fatal("event must stay pending when the broker rejects it")
	}

	// Broker back: the same event is published on the next attempt.
	producer.err = nil
	if n, err := newTestRelay(outbox, producer, 10).PublishOnce(context.Background()); n != 1 || err != nil {
		t.Fatalf("retry n = %d, err = %v", n, err)
	}
}

func TestMarkFailureIsAtLeastOnce(t *testing.T) {
	outbox := &fakeOutbox{pending: []store.OutboxEntry{entry("HZ-1", 1)}, published: map[string]int{}, markErr: errors.New("mongo down")}
	producer := &fakeProducer{}
	relay := newTestRelay(outbox, producer, 10)

	if n, err := relay.PublishOnce(context.Background()); n != 1 || err != nil {
		t.Fatalf("n = %d, err = %v", n, err)
	}
	outbox.markErr = nil
	relay.PublishOnce(context.Background())
	if len(producer.records) != 2 || producer.records[0].Key != producer.records[1].Key {
		t.Fatalf("expected the same event twice (at-least-once), got %d records", len(producer.records))
	}
}

func TestDrainPublishesAllBatches(t *testing.T) {
	var pending []store.OutboxEntry
	for i := 0; i < 25; i++ {
		pending = append(pending, entry("HZ-"+string(rune('a'+i)), 1))
	}
	outbox := &fakeOutbox{pending: pending, published: map[string]int{}}
	producer := &fakeProducer{}

	newTestRelay(outbox, producer, 10).drain(context.Background())
	if len(producer.records) != 25 || producer.calls != 3 || len(outbox.pending) != 0 {
		t.Fatalf("records = %d, calls = %d, pending = %d", len(producer.records), producer.calls, len(outbox.pending))
	}
}
