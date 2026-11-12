// Package config reads the service configuration from environment variables.
package config

import (
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/core"
)

type Config struct {
	ServiceName string
	LogLevel    string
	Port        int

	DBPath string

	SensorBaseURL      string
	SensorToken        string
	SensorTimeout      time.Duration
	SensorPollInterval time.Duration
	SensorPageLimit    int
	SensorMaxStaleness time.Duration

	AggregatorBaseURL  string
	CoastalIngestToken string
	AggregatorTimeout  time.Duration
}

func Load() (Config, error) {
	var e core.Env
	c := Config{
		ServiceName: e.String("SERVICE_NAME", "bnpb-coastal"),
		LogLevel:    e.String("LOG_LEVEL", "INFO"),
		Port:        e.Int("PORT", 8000),

		DBPath: e.String("COASTAL_DB_PATH", "/data/coastal.db"),

		SensorBaseURL:      e.String("SENSOR_BASE_URL", "http://sensor-pantai:8080"),
		SensorToken:        e.Required("PANTAI_AUTH_TOKEN"),
		SensorPollInterval: e.Seconds("SENSOR_POLL_INTERVAL_SECONDS", 2*time.Second),
		SensorPageLimit:    e.Int("SENSOR_PAGE_LIMIT", 500),
		SensorMaxStaleness: e.Seconds("SENSOR_MAX_STALENESS_SECONDS", 15*time.Minute),

		AggregatorBaseURL:  e.String("AGGREGATOR_BASE_URL", "http://bnpb-aggregator:8000"),
		CoastalIngestToken: e.Required("COASTAL_INGEST_TOKEN"),
	}
	c.SensorTimeout = e.Millis("SENSOR_TIMEOUT_MS", 5*time.Second)
	c.AggregatorTimeout = e.Millis("AGGREGATOR_TIMEOUT_MS", 5*time.Second)
	e.Check(c.SensorPageLimit >= 1 && c.SensorPageLimit <= 1000, "SENSOR_PAGE_LIMIT must be between 1 and 1000")
	e.Check(c.SensorMaxStaleness > 0, "SENSOR_MAX_STALENESS_SECONDS must be positive")
	e.Check(c.SensorPollInterval > 0, "SENSOR_POLL_INTERVAL_SECONDS must be positive")
	return c, e.Err()
}
