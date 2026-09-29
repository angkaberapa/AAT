// Package config reads the service configuration from environment variables
// (see .env.example at the repo root).
package config

import (
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-api/internal/core"
)

type Config struct {
	ServiceName string
	LogLevel    string
	Port        int

	// Canonical data is read ONLY through the aggregator's API, never from Mongo.
	AggregatorBaseURL string
	// Used to fetch the token verification key from the auth service.
	AuthBaseURL string

	UpstreamTimeout time.Duration
}

func Load() (Config, error) {
	var e core.Env
	c := Config{
		ServiceName: e.String("SERVICE_NAME", "bnpb-api"),
		LogLevel:    e.String("LOG_LEVEL", "INFO"),
		Port:        e.Int("PORT", 8080),

		AggregatorBaseURL: e.String("AGGREGATOR_BASE_URL", "http://bnpb-aggregator:8000"),
		AuthBaseURL:       e.String("AUTH_BASE_URL", "http://bnpb-auth:8000"),

		UpstreamTimeout: e.Millis("UPSTREAM_TIMEOUT_MS", 2000*time.Millisecond),
	}
	return c, e.Err()
}
