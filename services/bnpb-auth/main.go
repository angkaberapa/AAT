package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/angkaberapa/AAT/services/bnpb-auth/internal/config"
	"github.com/angkaberapa/AAT/services/bnpb-auth/internal/core"
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

	// TODO(P3): POST /token (client credentials -> access + refresh token with scope)
	// TODO(P3): POST /token/refresh (rotate refresh token, reject the old one)
	// TODO(P3): expose verification key (JWKS) for bnpb-api

	if err := core.Serve(ctx, logger, cfg.Port, mux); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
