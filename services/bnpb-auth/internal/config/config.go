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
	JWTIssuer       string
	JWTAudience     string
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
		JWTIssuer:       e.String("JWT_ISSUER", "bnpb-auth"),
		JWTAudience:     e.String("JWT_AUDIENCE", "bnpb-api"),
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
	e.Check(c.RefreshTokenTTL > c.AccessTokenTTL, "REFRESH_TOKEN_TTL_SECONDS must exceed ACCESS_TOKEN_TTL_SECONDS")
	e.Check(c.ClientMediaID == "" || (c.ClientMediaID != c.ClientFieldID && c.ClientMediaID != c.ClientOpsID), "CLIENT_MEDIA_ID must be distinct")
	e.Check(c.ClientFieldID == "" || c.ClientFieldID != c.ClientOpsID, "CLIENT_FIELD_ID must be distinct")
	e.Check(c.ClientMediaSecret == "" || (c.ClientMediaSecret != c.ClientFieldSecret && c.ClientMediaSecret != c.ClientOpsSecret), "CLIENT_MEDIA_SECRET must be distinct")
	e.Check(c.ClientFieldSecret == "" || c.ClientFieldSecret != c.ClientOpsSecret, "CLIENT_FIELD_SECRET must be distinct")
	return c, e.Err()
}
