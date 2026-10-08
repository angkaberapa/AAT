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
	AuthBaseURL  string
	JWTIssuer    string
	JWTAudience  string
	JWKSCacheTTL time.Duration

	UpstreamTimeout time.Duration

	// P2: resource-exhaustion protection. 0 disables the respective limit.
	MaxConcurrentRequests int
	RateLimitRPS          int // per client identity (JWT subject)
	RateLimitBurst        int
}

func Load() (Config, error) {
	var e core.Env
	c := Config{
		ServiceName: e.String("SERVICE_NAME", "bnpb-api"),
		LogLevel:    e.String("LOG_LEVEL", "INFO"),
		Port:        e.Int("PORT", 8080),

		AggregatorBaseURL: e.String("AGGREGATOR_BASE_URL", "http://bnpb-aggregator:8000"),
		AuthBaseURL:       e.String("AUTH_BASE_URL", "http://bnpb-auth:8000"),
		JWTIssuer:         e.String("JWT_ISSUER", "bnpb-auth"),
		JWTAudience:       e.String("JWT_AUDIENCE", "bnpb-api"),
		JWKSCacheTTL:      e.Seconds("JWKS_CACHE_TTL_SECONDS", 5*time.Minute),

		UpstreamTimeout: e.Millis("UPSTREAM_TIMEOUT_MS", 2000*time.Millisecond),

		MaxConcurrentRequests: e.Int("MAX_CONCURRENT_REQUESTS", 128),
		RateLimitRPS:          e.Int("RATE_LIMIT_RPS", 100),
		RateLimitBurst:        e.Int("RATE_LIMIT_BURST", 200),
	}
	e.Check(c.MaxConcurrentRequests >= 0, "MAX_CONCURRENT_REQUESTS must not be negative")
	e.Check(c.RateLimitRPS >= 0, "RATE_LIMIT_RPS must not be negative")
	e.Check(c.RateLimitBurst >= 1, "RATE_LIMIT_BURST must be at least 1")
	return c, e.Err()
}
