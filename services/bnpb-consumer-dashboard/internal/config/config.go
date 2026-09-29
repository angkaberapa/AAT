// Package config reads the service configuration from environment variables
// (see .env.example at the repo root).
package config

import "github.com/angkaberapa/AAT/services/bnpb-consumer-dashboard/internal/core"

type Config struct {
	ServiceName string
	LogLevel    string
	Port        int

	KafkaBootstrapServers string
	KafkaHazardTopic      string
	// Each consumer has its own group so every one of them receives every event.
	KafkaGroupID string
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
	return c, e.Err()
}
