// Package consumer is the Kafka consume loop shared in shape (copied, not imported)
// by every BNPB event consumer.
//
// Delivery semantics: at-least-once. Offsets are committed only after a whole fetch
// has been handled and the handler has persisted its state, so a crash re-delivers
// the uncommitted records instead of losing them. Handlers must therefore be
// idempotent; they deduplicate by (hazard_id, version).
package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/angkaberapa/AAT/services/bnpb-consumer-dashboard/internal/core"
)

// Hazard holds the canonical fields consumers use. Unknown fields are ignored, so
// the producer can add fields without breaking consumers.
type Hazard struct {
	HazardID   string    `json:"hazard_id"`
	Source     string    `json:"source"`
	HazardType string    `json:"hazard_type"`
	Severity   string    `json:"severity"`
	AreaName   string    `json:"area_name"`
	OccurredAt time.Time `json:"occurred_at"`
}

// Message is the value of a record on the hazard topic.
type Message struct {
	EventID     string    `json:"event_id"`
	Type        string    `json:"type"`
	Version     int       `json:"version"`
	PublishedAt time.Time `json:"published_at"`
	Hazard      Hazard    `json:"hazard"`
}

// Handler processes messages. Handle is called once per record; Flush is called after
// every fetch, before offsets are committed, and must persist the handler's state.
type Handler interface {
	Handle(ctx context.Context, msg Message) error
	Flush(ctx context.Context) error
}

type Config struct {
	Bootstrap       string
	Topic           string
	Group           string
	ProcessingDelay time.Duration // simulate a slow consumer in demos; 0 in normal runs
}

// Run consumes until ctx is cancelled.
func Run(ctx context.Context, logger *slog.Logger, cfg Config, handler Handler) error {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(strings.Split(cfg.Bootstrap, ",")...),
		kgo.ConsumerGroup(cfg.Group),
		kgo.ConsumeTopics(cfg.Topic),
		kgo.DisableAutoCommit(),
		// A group with no committed offset (a brand-new consumer) starts from the oldest
		// retained event, so it sees the full history.
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		// If a consumer dies without leaving the group, its partitions are reassigned
		// after this timeout (franz-go default 45 s, measured as a 46 s stall).
		kgo.SessionTimeout(10*time.Second),
	)
	if err != nil {
		return fmt.Errorf("create kafka consumer: %w", err)
	}
	defer client.Close()
	log := logger.With("component", "kafka.consumer", "group", cfg.Group, "topic", cfg.Topic)
	log.InfoContext(ctx, "consumer started")

	for {
		fetches := client.PollFetches(ctx)
		if ctx.Err() != nil {
			return nil
		}
		fetches.EachError(func(topic string, partition int32, err error) {
			log.WarnContext(ctx, "fetch error", "partition", partition, "error", err.Error())
		})

		var handled []*kgo.Record
		fetches.EachRecord(func(record *kgo.Record) {
			if ctx.Err() != nil {
				return // shutting down: leave the rest uncommitted, it is redelivered later
			}
			recordCtx := core.WithCorrelationID(ctx, correlationID(record))
			var msg Message
			if err := json.Unmarshal(record.Value, &msg); err != nil || msg.Hazard.HazardID == "" {
				// A record that can never be decoded would block the partition forever;
				// log it and move on.
				log.ErrorContext(recordCtx, "skipping undecodable record",
					"partition", record.Partition, "offset", record.Offset)
				handled = append(handled, record)
				return
			}
			if cfg.ProcessingDelay > 0 {
				select {
				case <-time.After(cfg.ProcessingDelay):
				case <-ctx.Done():
					return // do not block shutdown; the record is not committed
				}
			}
			if err := handler.Handle(recordCtx, msg); err != nil {
				log.ErrorContext(recordCtx, "handle failed", "event_id", msg.EventID, "error", err.Error())
			}
			handled = append(handled, record)
		})
		if ctx.Err() != nil {
			return nil // close the client so it leaves the group right away
		}
		if len(handled) == 0 {
			continue
		}

		// State first, offsets second: committing before persisting could lose events.
		if err := handler.Flush(ctx); err != nil {
			log.ErrorContext(ctx, "persist state failed, offsets not committed", "error", err.Error())
			continue
		}
		start := time.Now()
		if err := client.CommitRecords(ctx, handled...); err != nil {
			log.WarnContext(ctx, "commit failed, records will be redelivered", "error", err.Error())
			continue
		}
		log.InfoContext(ctx, "outbound call", "target", "kafka", "op", "commit",
			"records", len(handled), "latency_ms", float64(time.Since(start).Microseconds())/1000)
	}
}

// correlationID continues the trace started by the poll cycle that produced the event.
func correlationID(record *kgo.Record) string {
	for _, h := range record.Headers {
		if strings.EqualFold(h.Key, core.CorrelationHeader) && len(h.Value) > 0 {
			return string(h.Value)
		}
	}
	return core.NewCorrelationID()
}
