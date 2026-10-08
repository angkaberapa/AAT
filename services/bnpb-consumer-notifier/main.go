package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/angkaberapa/AAT/services/bnpb-consumer-notifier/internal/config"
	"github.com/angkaberapa/AAT/services/bnpb-consumer-notifier/internal/consumer"
	"github.com/angkaberapa/AAT/services/bnpb-consumer-notifier/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-consumer-notifier/internal/notify"
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

	notifier, err := notify.New(consumer.NewFileState(cfg.StateFile), logger)
	if err != nil {
		logger.Error("load notifier state", "error", err.Error())
		os.Exit(1)
	}
	mux := core.NewMux(cfg.ServiceName)
	notifier.Register(mux)

	// P5: subscribe to the hazard topic with our own consumer group. The producer does
	// not know this consumer exists.
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		err := consumer.Run(ctx, logger, consumer.Config{
			Bootstrap:       cfg.KafkaBootstrapServers,
			Topic:           cfg.KafkaHazardTopic,
			Group:           cfg.KafkaGroupID,
			ProcessingDelay: cfg.ProcessingDelay,
		}, notifier)
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
