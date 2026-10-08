package publisher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/core"
)

// KafkaProducer publishes records to one topic with acks=all and the idempotent
// producer (franz-go defaults), so a retried batch is not duplicated by the broker.
type KafkaProducer struct {
	client *kgo.Client
	topic  string
	logger *slog.Logger
}

func NewKafkaProducer(bootstrap, topic string, logger *slog.Logger) (*KafkaProducer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(strings.Split(bootstrap, ",")...),
		kgo.DefaultProduceTopic(topic),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.RecordDeliveryTimeout(10*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("create kafka client: %w", err)
	}
	return &KafkaProducer{client: client, topic: topic, logger: logger.With("component", "kafka.producer")}, nil
}

// Publish sends all records and logs the latency of the call.
//
// franz-go keeps retrying while no broker is reachable and does not honour the context
// or RecordDeliveryTimeout in that state (measured: one call blocked 39 s during a Kafka
// outage). So Publish first checks that a broker answers, and if the produce call still
// outlives ctx, the buffered records are aborted and the batch is retried later by the
// relay (at-least-once).
func (p *KafkaProducer) Publish(ctx context.Context, records []Record) error {
	krecords := make([]*kgo.Record, 0, len(records))
	for _, r := range records {
		krecords = append(krecords, &kgo.Record{
			Key:     []byte(r.Key),
			Value:   r.Value,
			Headers: []kgo.RecordHeader{{Key: core.CorrelationHeader, Value: []byte(r.CorrelationID)}},
		})
	}

	start := time.Now()
	err := p.client.Ping(ctx)
	if err == nil {
		err = p.produce(ctx, krecords)
	} else {
		err = fmt.Errorf("broker unreachable: %w", err)
	}
	attrs := []any{
		"target", "kafka",
		"topic", p.topic,
		"records", len(records),
		"latency_ms", float64(time.Since(start).Microseconds()) / 1000,
	}
	if err != nil {
		p.logger.WarnContext(ctx, "outbound call failed", append(attrs, "error", err.Error())...)
		return fmt.Errorf("produce to %s: %w", p.topic, err)
	}
	p.logger.InfoContext(ctx, "outbound call", attrs...)
	return nil
}

func (p *KafkaProducer) produce(ctx context.Context, records []*kgo.Record) error {
	done := make(chan error, 1)
	go func() { done <- p.client.ProduceSync(context.Background(), records...).FirstErr() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		abortCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.client.AbortBufferedRecords(abortCtx) // fails the stuck records, so ProduceSync returns
		<-done
		return fmt.Errorf("produce timed out: %w", ctx.Err())
	}
}

// EnsureTopic creates the topic with explicit partitions and retention. Retention
// decides how long a stopped consumer can be down and still catch up.
func (p *KafkaProducer) EnsureTopic(ctx context.Context, partitions int32, retention time.Duration) error {
	retentionMs := strconv.FormatInt(retention.Milliseconds(), 10)
	resp, err := kadm.NewClient(p.client).CreateTopic(ctx, partitions, 1,
		map[string]*string{"retention.ms": &retentionMs}, p.topic)
	if err == nil {
		err = resp.Err
	}
	if err != nil && !errors.Is(err, kerr.TopicAlreadyExists) {
		return fmt.Errorf("create topic %s: %w", p.topic, err)
	}
	return nil
}

// EnsureTopicUntilReady retries EnsureTopic until it succeeds or ctx ends, so the
// aggregator can start (and poll) while Kafka is still down.
func (p *KafkaProducer) EnsureTopicUntilReady(ctx context.Context, partitions int32, retention time.Duration) {
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := p.EnsureTopic(attemptCtx, partitions, retention)
		cancel()
		if err == nil {
			p.logger.InfoContext(ctx, "topic ready", "topic", p.topic, "partitions", partitions, "retention", retention.String())
			return
		}
		p.logger.WarnContext(ctx, "topic not ready, retrying", "topic", p.topic, "error", err.Error())
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (p *KafkaProducer) Close() { p.client.Close() }
