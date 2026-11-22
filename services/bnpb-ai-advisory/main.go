// P5 bonus: AI-native anomaly advisory. Ditulis dengan bantuan LLM (OpenCode,
// DeepSeek V4.1 Flash) untuk Milestone 2 IF4031. Komponen ini membaca topic
// hazard-events dengan consumer group sendiri dan hanya menghasilkan advisory;
// tidak menulis ke Canonical Store dan tidak berada di jalur peringatan.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/angkaberapa/AAT/services/bnpb-ai-advisory/internal/advisory"
	"github.com/angkaberapa/AAT/services/bnpb-ai-advisory/internal/config"
	"github.com/angkaberapa/AAT/services/bnpb-ai-advisory/internal/consumer"
	"github.com/angkaberapa/AAT/services/bnpb-ai-advisory/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-ai-advisory/internal/detector"
	"github.com/angkaberapa/AAT/services/bnpb-ai-advisory/internal/summarizer"
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

	store := advisory.NewStore(cfg.MaxAdvisories)
	recent := advisory.NewRecentEvents(cfg.SummaryMaxEvents)
	det := detector.New(detector.Config{
		Threshold:  cfg.AnomalyThreshold,
		MinSamples: cfg.MinSamples,
		Cooldown:   cfg.Cooldown,
	})
	summary := summarizer.New(summarizer.Config{
		BaseURL:  cfg.LLMBaseURL,
		APIKey:   cfg.LLMAPIKey,
		Model:    cfg.LLMModel,
		Thinking: cfg.LLMThinking,
		Timeout:  cfg.LLMTimeout,
	}, logger)
	processor := advisory.NewProcessor(det, store, recent, logger)

	mux := core.NewMux(cfg.ServiceName)
	advisory.NewAPI(store, cfg.AnomalyThreshold, recent, summary, cfg.SummaryMaxEvents).Register(mux)

	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		err := consumer.Run(ctx, logger, consumer.Config{
			Bootstrap: cfg.KafkaBootstrapServers,
			Topic:     cfg.KafkaHazardTopic,
			Group:     cfg.KafkaGroupID,
		}, processor)
		if err != nil {
			logger.Error("consumer stopped", "error", err.Error())
			stop()
		}
	}()

	if err := core.Serve(ctx, logger, cfg.Port, mux); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
	stop()
	<-consumerDone
}
