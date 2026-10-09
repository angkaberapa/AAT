package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/api"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/config"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/ingest"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/mapping"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/publisher"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/reference"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/source/bmkg"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/source/pvmbg"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

func main() {
	core.RunHealthcheckIfRequested()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid configuration:\n%v\n", err)
		os.Exit(1)
	}
	logger := core.NewLogger(cfg.ServiceName, cfg.LogLevel)

	// Cancelled on SIGTERM (docker compose stop) / Ctrl+C; background loops should stop on it.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	mux := core.NewMux(cfg.ServiceName)

	//P4: Mongo (Canonical Store) + internal read API
	connectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	st, err := store.New(connectCtx, logger, cfg.MongoURI, cfg.MongoDB)
	cancel()
	if err != nil {
		logger.Error("canonical store", "error", err.Error())
		os.Exit(1)
	}
	//P2: status kebaruan data per sumber, diisi poller dan dibaca API (meta.sources / stale_since).
	tracker := ingest.NewStatusTracker([]domain.Source{domain.SourceBMKG, domain.SourcePVMBG}, cfg.SourceStaleAfter)
	api.Register(mux, logger, st, tracker, api.WithSnapshotLimits(cfg.SnapshotDefaultLimit, cfg.SnapshotMaxLimit))

	//P5: producer Kafka + relay outbox.
	producer, err := publisher.NewKafkaProducer(cfg.KafkaBootstrapServers, cfg.KafkaHazardTopic, logger)
	if err != nil {
		logger.Error("kafka producer", "error", err.Error())
		os.Exit(1)
	}
	relay := publisher.NewRelay(st, producer, logger, cfg.OutboxRelayInterval, cfg.OutboxBatchSize)
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		producer.EnsureTopicUntilReady(ctx, int32(cfg.KafkaTopicPartitions), cfg.KafkaTopicRetention)
		relay.Run(ctx)
	}()

	//P2: poller BMKG & PVMBG
	volcanoes, err := reference.Volcanoes()
	if err != nil {
		logger.Error("volcano reference table", "error", err.Error())
		os.Exit(1)
	}

	mapper, err := mapping.NewMapper(volcanoes)
	if err != nil {
		logger.Error("mapper", "error", err.Error())
		os.Exit(1)
	}

	httpClient := core.NewHTTPClient(logger, cfg.UpstreamTimeout)

	bmkgClient, err := bmkg.NewClient(cfg.BMKGBaseURL, cfg.BMKGAPIKey, httpClient, mapper, logger)
	if err != nil {
		logger.Error("bmkg client", "error", err.Error())
		os.Exit(1)
	}

	pvmbgClient, err := pvmbg.NewClient(cfg.PVMBGBaseURL, cfg.PVMBGToken, httpClient, mapper, logger)
	if err != nil {
		logger.Error("pvmbg client", "error", err.Error())
		os.Exit(1)
	}

	//P5: Save nulis data + nandain pending di outbox dalam satu update atomik, relay yang ngirim.
	handle := func(ctx context.Context, events []domain.HazardEvent) error {
		counts := map[store.SaveResult]int{}
		for _, e := range events {
			result, err := st.Save(ctx, e)
			if err != nil {
				return err
			}
			counts[result]++
		}
		logger.InfoContext(ctx, "events stored",
			"inserted", counts[store.Inserted],
			"updated", counts[store.Updated],
			"unchanged", counts[store.Unchanged])
		return nil
	}

	//Polling di goroutine sendiri biar HTTP server bisa langsung listen.
	pollingDone := make(chan struct{})
	go func() {
		defer close(pollingDone)
		ingest.Run(ctx, logger, cfg.PollInterval, []ingest.SourceClient{bmkgClient, pvmbgClient}, handle, tracker)
	}()

	if err := core.Serve(ctx, logger, cfg.Port, mux); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}

	//Serve balik kalau ctx ke-cancel. Panggil stop() buat mastiin polling juga dapet sinyal cancel, terus tungguin selesai sebelum store di-close.
	stop()
	<-pollingDone
	<-relayDone
	producer.Close()

	//Gak pakai defer buat Close, biar urutannya jelas: polling berhenti dulu, baru store.
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = st.Close(closeCtx)
	closeCancel()
}
