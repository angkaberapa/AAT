// Package config reads the service configuration from environment variables
// (see .env.example at the repo root).
package config

import (
	"os"
	"strconv"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-ai-advisory/internal/core"
)

type Config struct {
	ServiceName string
	LogLevel    string
	Port        int

	KafkaBootstrapServers string
	KafkaHazardTopic      string
	// Every consumer uses its own group, so this service sees every event
	// without competing with the dashboard or notifier.
	KafkaGroupID string

	AnomalyThreshold float64
	MinSamples       int
	Cooldown         time.Duration
	MaxAdvisories    int
	SummaryMaxEvents int

	LLMBaseURL  string
	LLMAPIKey   string
	LLMModel    string
	LLMThinking string
	LLMTimeout  time.Duration
}

func Load() (Config, error) {
	var e core.Env
	c := Config{
		ServiceName:           e.String("SERVICE_NAME", "bnpb-ai-advisory"),
		LogLevel:              e.String("LOG_LEVEL", "INFO"),
		Port:                  e.Int("PORT", 8000),
		KafkaBootstrapServers: e.String("KAFKA_BOOTSTRAP_SERVERS", "kafka:9092"),
		KafkaHazardTopic:      e.String("KAFKA_HAZARD_TOPIC", "hazard-events"),
		KafkaGroupID:          e.String("KAFKA_GROUP_ID", "ai-advisory"),
		AnomalyThreshold:      3.5,
		MinSamples:            e.Int("AI_MIN_SAMPLES", 20),
		Cooldown:              e.Seconds("AI_COOLDOWN_SECONDS", 30*time.Second),
		MaxAdvisories:         e.Int("AI_MAX_ADVISORIES", 200),
		SummaryMaxEvents:      e.Int("AI_SUMMARY_MAX_EVENTS", 20),
		LLMBaseURL:            os.Getenv("LLM_BASE_URL"),
		LLMAPIKey:             os.Getenv("LLM_API_KEY"),
		LLMModel:              os.Getenv("LLM_MODEL"),
		LLMThinking:           os.Getenv("LLM_THINKING"),
		LLMTimeout:            e.Millis("LLM_TIMEOUT_MS", 3*time.Second),
	}

	if raw := os.Getenv("AI_ANOMALY_THRESHOLD"); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			e.Check(false, "AI_ANOMALY_THRESHOLD must be a number")
		} else {
			c.AnomalyThreshold = value
		}
	}

	e.Check(c.AnomalyThreshold > 0, "AI_ANOMALY_THRESHOLD must be positive")
	e.Check(c.MinSamples >= 5, "AI_MIN_SAMPLES must be at least 5")
	e.Check(c.MaxAdvisories > 0, "AI_MAX_ADVISORIES must be positive")
	e.Check(c.SummaryMaxEvents > 0, "AI_SUMMARY_MAX_EVENTS must be positive")
	e.Check(c.LLMThinking == "" || c.LLMThinking == "enabled" || c.LLMThinking == "disabled",
		"LLM_THINKING must be empty, enabled, or disabled")
	return c, e.Err()
}
