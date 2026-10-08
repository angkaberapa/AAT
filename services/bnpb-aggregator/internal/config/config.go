// Package config reads the service configuration from environment variables
// (see .env.example at the repo root).
package config

import (
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/core"
)

type Config struct {
	ServiceName string
	LogLevel    string
	Port        int

	// Upstream sources. Each has its own credential; they must never be swapped.
	BMKGBaseURL     string
	BMKGAPIKey      string
	PVMBGBaseURL    string
	PVMBGToken      string
	PollInterval    time.Duration
	UpstreamTimeout time.Duration
	// A source with no successful poll for this long is reported stale (P2).
	// Default: 3 poll intervals + one upstream timeout.
	SourceStaleAfter time.Duration

	// Canonical Store (owned exclusively by this service)
	MongoURI string
	MongoDB  string

	// Message broker
	KafkaBootstrapServers string
	KafkaHazardTopic      string
	// P5: topic created by the aggregator; retention = how long a stopped consumer can catch up.
	KafkaTopicPartitions int
	KafkaTopicRetention  time.Duration
	// P5: outbox relay
	OutboxRelayInterval time.Duration
	OutboxBatchSize     int
}

func Load() (Config, error) {
	var e core.Env
	c := Config{
		ServiceName: e.String("SERVICE_NAME", "bnpb-aggregator"),
		LogLevel:    e.String("LOG_LEVEL", "INFO"),
		Port:        e.Int("PORT", 8000),

		BMKGBaseURL:     e.String("BMKG_BASE_URL", "http://bmkg-mock:8081"),
		BMKGAPIKey:      e.Required("BMKG_API_KEY"),
		PVMBGBaseURL:    e.String("PVMBG_BASE_URL", "http://pvmbg-mock:8082"),
		PVMBGToken:      e.Required("PVMBG_TOKEN"),
		PollInterval:    e.Seconds("POLL_INTERVAL_SECONDS", 3*time.Second),
		UpstreamTimeout: e.Millis("UPSTREAM_TIMEOUT_MS", 5000*time.Millisecond),

		MongoURI: e.Required("MONGO_URI"),
		MongoDB:  e.String("MONGO_DB", "bnpb"),

		KafkaBootstrapServers: e.String("KAFKA_BOOTSTRAP_SERVERS", "kafka:9092"),
		KafkaHazardTopic:      e.String("KAFKA_HAZARD_TOPIC", "hazard-events"),
	}
	c.KafkaTopicPartitions = e.Int("KAFKA_TOPIC_PARTITIONS", 3)
	retentionHours := e.Int("KAFKA_TOPIC_RETENTION_HOURS", 168)
	e.Check(retentionHours >= 1, "KAFKA_TOPIC_RETENTION_HOURS must be at least 1")
	c.KafkaTopicRetention = time.Duration(retentionHours) * time.Hour
	c.OutboxRelayInterval = e.Millis("OUTBOX_RELAY_INTERVAL_MS", time.Second)
	c.OutboxBatchSize = e.Int("OUTBOX_BATCH_SIZE", 100)
	e.Check(c.KafkaTopicPartitions >= 1, "KAFKA_TOPIC_PARTITIONS must be at least 1")
	e.Check(c.OutboxBatchSize >= 1, "OUTBOX_BATCH_SIZE must be at least 1")
	c.SourceStaleAfter = e.Seconds("SOURCE_STALE_AFTER_SECONDS", 3*c.PollInterval+c.UpstreamTimeout)
	return c, e.Err()
}
