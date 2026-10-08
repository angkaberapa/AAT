// Package config reads the service configuration from environment variables
// (see .env.example at the repo root).
package config

import (
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-consumer-dashboard/internal/core"
)

type Config struct {
	ServiceName string
	LogLevel    string
	Port        int

	KafkaBootstrapServers string
	KafkaHazardTopic      string
	// Each consumer has its own group so every one of them receives every event.
	KafkaGroupID string

	// State (latest version per hazard) is persisted here, on a volume owned by this
	// consumer, so restarts keep the dashboard and redeliveries stay idempotent.
	StateFile string
	// Artificial delay per event, to demo a slow consumer (P5). 0 = off.
	ProcessingDelay time.Duration
}

func Load() (Config, error) {
	var e core.Env
	c := Config{
		ServiceName: e.String("SERVICE_NAME", "bnpb-consumer-dashboard"),
		LogLevel:    e.String("LOG_LEVEL", "INFO"),
		Port:        e.Int("PORT", 8000),

		KafkaBootstrapServers: e.String("KAFKA_BOOTSTRAP_SERVERS", "kafka:9092"),
		KafkaHazardTopic:      e.String("KAFKA_HAZARD_TOPIC", "hazard-events"),
		KafkaGroupID:          e.String("KAFKA_GROUP_ID", "dashboard-updater"),
	}
	c.StateFile = e.String("STATE_FILE", "/data/state.json")
	delayMs := e.Int("PROCESSING_DELAY_MS", 0)
	e.Check(delayMs >= 0, "PROCESSING_DELAY_MS must not be negative")
	c.ProcessingDelay = time.Duration(delayMs) * time.Millisecond
	return c, e.Err()
}
