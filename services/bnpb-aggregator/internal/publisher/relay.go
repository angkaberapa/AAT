// Package publisher publishes canonical HazardEvents to the message broker (P5).
//
// The relay reads pending entries from the Canonical Store outbox and hands them to a
// Producer. It runs in its own goroutine, so a slow or unavailable broker never blocks
// polling: events simply stay pending and are published once the broker is back.
// Delivery is at-least-once: if publishing succeeds but marking fails, the same version
// is published again, so consumers deduplicate by (hazard_id, version).
package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

const (
	TypeCreated = "hazard.created"
	TypeUpdated = "hazard.updated"
)

// Message is the JSON value of every record on the hazard topic. The record key is
// the hazard_id, so all versions of one hazard land on the same partition in order.
type Message struct {
	EventID     string             `json:"event_id"` // "<hazard_id>:v<version>", unique per version
	Type        string             `json:"type"`
	Version     int                `json:"version"`
	PublishedAt time.Time          `json:"published_at"`
	Hazard      domain.HazardEvent `json:"hazard"`
}

// Record is what the relay hands to a Producer.
type Record struct {
	Key           string
	Value         []byte
	CorrelationID string // from the poll cycle that produced this version
}

type Outbox interface {
	Pending(ctx context.Context, limit int) ([]store.OutboxEntry, error)
	MarkPublished(ctx context.Context, hazardID string, version int) error
}

// Producer publishes all records or returns an error.
type Producer interface {
	Publish(ctx context.Context, records []Record) error
}

type Relay struct {
	outbox    Outbox
	producer  Producer
	logger    *slog.Logger
	interval  time.Duration
	batchSize int
	timeout   time.Duration
	now       func() time.Time
}

func NewRelay(outbox Outbox, producer Producer, logger *slog.Logger, interval time.Duration, batchSize int) *Relay {
	return &Relay{
		outbox:    outbox,
		producer:  producer,
		logger:    logger.With("component", "outbox.relay"),
		interval:  interval,
		batchSize: batchSize,
		timeout:   10 * time.Second,
		now:       time.Now,
	}
}

const maxBackoff = 30 * time.Second

// Run publishes pending events every interval until ctx is cancelled. After a failed
// attempt it waits with exponential backoff (2x interval, doubling, max 30 s).
func (r *Relay) Run(ctx context.Context) {
	wait := r.interval
	for {
		if r.drain(ctx) {
			wait = r.interval
		} else {
			wait = min(max(2*wait, 2*r.interval), maxBackoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// drain publishes batches until the outbox is empty; false when a batch failed.
func (r *Relay) drain(ctx context.Context) bool {
	for ctx.Err() == nil {
		n, err := r.PublishOnce(ctx)
		if err != nil {
			if ctx.Err() == nil {
				r.logger.WarnContext(ctx, "publish failed, events stay pending", "error", err.Error())
			}
			return false
		}
		if n < r.batchSize {
			return true
		}
	}
	return true
}

// PublishOnce publishes at most one batch and returns how many events were published.
func (r *Relay) PublishOnce(parent context.Context) (int, error) {
	ctx := core.WithCorrelationID(parent, core.NewCorrelationID())
	entries, err := r.outbox.Pending(ctx, r.batchSize)
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}

	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		value, err := json.Marshal(newMessage(entry, r.now()))
		if err != nil {
			return 0, fmt.Errorf("encode %s: %w", entry.Event.HazardID, err)
		}
		records = append(records, Record{
			Key:           entry.Event.HazardID,
			Value:         value,
			CorrelationID: entry.CorrelationID,
		})
	}

	publishCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	if err := r.producer.Publish(publishCtx, records); err != nil {
		return 0, err
	}

	for _, entry := range entries {
		if err := r.outbox.MarkPublished(ctx, entry.Event.HazardID, entry.Version); err != nil {
			// Already on the broker; it will be published again later (at-least-once).
			r.logger.WarnContext(ctx, "mark published failed", "hazard_id", entry.Event.HazardID, "error", err.Error())
		}
	}
	r.logger.InfoContext(ctx, "events published", "count", len(entries))
	return len(entries), nil
}

func newMessage(entry store.OutboxEntry, now time.Time) Message {
	typ := TypeUpdated
	if entry.Version == 1 {
		typ = TypeCreated
	}
	return Message{
		EventID:     fmt.Sprintf("%s:v%d", entry.Event.HazardID, entry.Version),
		Type:        typ,
		Version:     entry.Version,
		PublishedAt: now.UTC(),
		Hazard:      entry.Event,
	}
}
