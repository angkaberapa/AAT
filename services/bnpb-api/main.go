package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/angkaberapa/AAT/services/bnpb-api/internal/config"
	"github.com/angkaberapa/AAT/services/bnpb-api/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-api/internal/hazards"
	"github.com/angkaberapa/AAT/services/bnpb-api/internal/security"
)

func main() {
	core.RunHealthcheckIfRequested()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid configuration:\n%v\n", err)
		os.Exit(1)
	}
	logger := core.NewLogger(cfg.ServiceName, cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	mux := core.NewMux(cfg.ServiceName)
	// Outbound calls to aggregator/auth must go through this (correlation ID + latency log).
	httpClient := core.NewHTTPClient(logger, cfg.UpstreamTimeout)
	jwks, err := security.NewJWKSProvider(cfg.AuthBaseURL, httpClient, cfg.JWKSCacheTTL)
	if err != nil {
		logger.Error("initialize JWKS provider", "error", err)
		os.Exit(1)
	}
	verifier, err := security.NewVerifier(jwks, cfg.JWTIssuer, cfg.JWTAudience)
	if err != nil {
		logger.Error("initialize token verifier", "error", err)
		os.Exit(1)
	}
	hazardHandler, err := hazards.NewHandler(cfg.AggregatorBaseURL, httpClient)
	if err != nil {
		logger.Error("initialize hazard handler", "error", err)
		os.Exit(1)
	}
	hazardHandler.Register(mux, verifier.Middleware)

	// TODO(P2): rate limiter / concurrency limit, stale-data response on PVMBG outage

	if err := core.Serve(ctx, logger, cfg.Port, mux); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
