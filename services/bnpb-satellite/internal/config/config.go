// Package config reads the service configuration from environment variables.
package config

import (
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/core"
)

type Config struct {
	ServiceName string
	LogLevel    string
	Port        int

	DBPath string

	FeedBaseURL string
	FeedToken   string

	ReconnectMin time.Duration
	ReconnectMax time.Duration

	AllowedLateness time.Duration

	AggregatorBaseURL    string
	SatelliteIngestToken string
	AggregatorTimeout    time.Duration
}

func Load() (Config, error) {
	var e core.Env
	c := Config{
		ServiceName: e.String("SERVICE_NAME", "bnpb-satellite"),
		LogLevel:    e.String("LOG_LEVEL", "INFO"),
		Port:        e.Int("PORT", 8000),

		DBPath: e.String("SATELLITE_DB_PATH", "/data/satellite.db"),

		FeedBaseURL: e.String("SATELLITE_BASE_URL", "https://satellite.naufarrel.dev"),
		FeedToken:   e.Required("SATELLITE_TOKEN"),

		ReconnectMin: e.Seconds("SATELLITE_RECONNECT_MIN_SECONDS", time.Second),
		ReconnectMax: e.Seconds("SATELLITE_RECONNECT_MAX_SECONDS", 30*time.Second),

		AllowedLateness: e.Seconds("SATELLITE_ALLOWED_LATENESS_SECONDS", 360*time.Minute),

		AggregatorBaseURL:    e.String("AGGREGATOR_BASE_URL", "http://bnpb-aggregator:8000"),
		SatelliteIngestToken: e.Required("SATELLITE_INGEST_TOKEN"),
	}
	c.AggregatorTimeout = e.Millis("AGGREGATOR_TIMEOUT_MS", 5*time.Second)
	e.Check(c.ReconnectMax >= c.ReconnectMin, "SATELLITE_RECONNECT_MAX_SECONDS must be at least the minimum")
	e.Check(c.AllowedLateness > 0, "SATELLITE_ALLOWED_LATENESS_SECONDS must be positive")
	return c, e.Err()
}
