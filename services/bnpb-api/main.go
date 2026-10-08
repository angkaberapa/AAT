package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/angkaberapa/AAT/services/bnpb-api/internal/config"
	"github.com/angkaberapa/AAT/services/bnpb-api/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-api/internal/hazards"
	"github.com/angkaberapa/AAT/services/bnpb-api/internal/limit"
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
	// P2: global in-flight cap first (cheap, protects even token verification), then
	// authentication, then a per-client token bucket keyed by the verified identity.
	concurrency := limit.Concurrency(cfg.MaxConcurrentRequests)
	perClient := limit.NewPerClient(float64(cfg.RateLimitRPS), cfg.RateLimitBurst, func(r *http.Request) (string, bool) {
		principal, ok := security.PrincipalFromContext(r.Context())
		return principal.ClientID, ok
	})
	hazardHandler.Register(mux, func(next http.Handler) http.Handler {
		return concurrency(verifier.Middleware(perClient.Middleware(next)))
	})

	if err := core.Serve(ctx, logger, cfg.Port, mux); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
