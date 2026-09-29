package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/angkaberapa/AAT/services/bnpb-api/internal/config"
	"github.com/angkaberapa/AAT/services/bnpb-api/internal/core"
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
	_ = httpClient

	// TODO: read routes (GET /hazards, GET /hazards/{id}) proxied to the aggregator
	// TODO(P2): rate limiter / concurrency limit, stale-data response on PVMBG outage
	// TODO(P3): verify JWT, enforce scope-based field filtering (media = Ringkasan only)

	if err := core.Serve(ctx, logger, cfg.Port, mux); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
