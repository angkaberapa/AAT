// Package config reads the service configuration from environment variables
// (see .env.example at the repo root).
package config

import (
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-auth/internal/core"
)

type Config struct {
	ServiceName string
	LogLevel    string
	Port        int

	JWTSigningKey   string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration

	// Three distinct downstream identities (not one account with a role flag).
	ClientMediaID, ClientMediaSecret string
	ClientFieldID, ClientFieldSecret string
	ClientOpsID, ClientOpsSecret     string
}

func Load() (Config, error) {
	var e core.Env
	c := Config{
		ServiceName: e.String("SERVICE_NAME", "bnpb-auth"),
		LogLevel:    e.String("LOG_LEVEL", "INFO"),
		Port:        e.Int("PORT", 8000),

		JWTSigningKey:   e.Required("JWT_SIGNING_KEY"),
		AccessTokenTTL:  e.Seconds("ACCESS_TOKEN_TTL_SECONDS", 60*time.Second),
		RefreshTokenTTL: e.Seconds("REFRESH_TOKEN_TTL_SECONDS", time.Hour),

		ClientMediaID:     e.Required("CLIENT_MEDIA_ID"),
		ClientMediaSecret: e.Required("CLIENT_MEDIA_SECRET"),
		ClientFieldID:     e.Required("CLIENT_FIELD_ID"),
		ClientFieldSecret: e.Required("CLIENT_FIELD_SECRET"),
		ClientOpsID:       e.Required("CLIENT_OPS_ID"),
		ClientOpsSecret:   e.Required("CLIENT_OPS_SECRET"),
	}
	e.Check(c.JWTSigningKey == "" || len(c.JWTSigningKey) >= 32, "JWT_SIGNING_KEY must be at least 32 characters")
	return c, e.Err()
}
