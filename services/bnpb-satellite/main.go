// P4 Feed Satelit ingestion service. Ditulis dengan bantuan LLM (OpenCode,
// DeepSeek V4.1 Flash) untuk Milestone 2 IF4031. Keputusan desain dan hasil
// uji ditinjau oleh anggota kelompok.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/config"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/ingest"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/metrics"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/processor"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/sse"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/store"
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

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		logger.Error("open satellite store", "error", err.Error())
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	stream, err := sse.NewClient(cfg.FeedBaseURL, cfg.FeedToken)
	if err != nil {
		logger.Error("feed client", "error", err.Error())
		os.Exit(1)
	}
	ingestClient, err := ingest.NewHTTPClient(cfg.AggregatorBaseURL, cfg.SatelliteIngestToken, cfg.AggregatorTimeout)
	if err != nil {
		logger.Error("ingest client", "error", err.Error())
		os.Exit(1)
	}
	recorder := metrics.New()
	proc, err := processor.New(logger, stream, db, ingestClient, processor.Config{
		AllowedLateness: cfg.AllowedLateness,
		ReconnectMin:    cfg.ReconnectMin,
		ReconnectMax:    cfg.ReconnectMax,
	}, recorder)
	if err != nil {
		logger.Error("processor", "error", err.Error())
		os.Exit(1)
	}

	mux := core.NewMux(cfg.ServiceName)
	mux.Handle("GET /metrics", recorder.Handler())
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		stats, err := db.Stats(r.Context())
		if err != nil {
			core.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		core.WriteJSON(w, http.StatusOK, stats)
	})

	processorDone := make(chan struct{})
	go func() {
		defer close(processorDone)
		proc.Run(ctx)
	}()

	if err := core.Serve(ctx, logger, cfg.Port, mux); err != nil {
		logger.Error("server error", "error", err.Error())
		os.Exit(1)
	}
	stop()
	<-processorDone
}
