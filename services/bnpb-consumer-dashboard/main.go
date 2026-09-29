package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/angkaberapa/AAT/services/bnpb-consumer-dashboard/internal/config"
	"github.com/angkaberapa/AAT/services/bnpb-consumer-dashboard/internal/core"
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

	// HTTP server only serves /health; the real work is the Kafka consumer loop.
	mux := core.NewMux(cfg.ServiceName)

	// TODO(P5): start Kafka consumer goroutine (idempotent handling, one correlation ID per event)

	if err := core.Serve(ctx, logger, cfg.Port, mux); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
