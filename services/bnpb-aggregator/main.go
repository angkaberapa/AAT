package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/api"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/config"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/ingest"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/leader"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/mapping"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/publisher"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/reference"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/source/bmkg"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/source/pvmbg"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

// Sumber yang di-poll aggregator ini. SATELIT (P4) dan SENSOR_PANTAI (P5) menyusul.
var polledSources = []domain.Source{domain.SourceBMKG, domain.SourcePVMBG}

func main() {
	core.RunHealthcheckIfRequested()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid configuration:\n%v\n", err)
		os.Exit(1)
	}
	logger := core.NewLogger(cfg.ServiceName, cfg.LogLevel).With("instance", cfg.InstanceID)

	// Cancelled on SIGTERM (docker compose stop / pod dihapus) / Ctrl+C; background loops should stop on it.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	mux := core.NewMux(cfg.ServiceName)

	//P4: Mongo (Canonical Store) + internal read API
	st, err := connectStore(ctx, logger, cfg)
	if err != nil {
		logger.Error("canonical store", "error", err.Error())
		os.Exit(1)
	}

	//P2 M2: status kebaruan per sumber dibaca dari storage bersama, jadi leader & follower sama.
	statuses := ingest.NewSharedStatus(func(ctx context.Context) ([]ingest.Record, error) {
		recs, err := st.SourceStatuses(ctx)
		return toIngestRecords(recs), err
	}, polledSources, cfg.SourceStaleAfter, time.Second, logger)
	api.Register(mux, logger, st, statuses, api.WithSnapshotLimits(cfg.SnapshotDefaultLimit, cfg.SnapshotMaxLimit))

	//P5: producer Kafka + relay outbox (relay hanya jalan di leader).
	producer, err := publisher.NewKafkaProducer(cfg.KafkaBootstrapServers, cfg.KafkaHazardTopic, logger)
	if err != nil {
		logger.Error("kafka producer", "error", err.Error())
		os.Exit(1)
	}
	relay := publisher.NewRelay(st, producer, logger, cfg.OutboxRelayInterval, cfg.OutboxBatchSize)

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
	// P2 M2 kriteria 2: log upstream bisa menunjukkan instance mana yang memanggil.
	httpClient.SetHeader("X-Instance-ID", cfg.InstanceID)

	t := &termRunner{cfg: cfg, logger: logger, store: st, relay: relay, producer: producer, mapper: mapper, http: httpClient}

	//P2 M2: hanya leader yang polling + relay. Instance lain tetap melayani API baca.
	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		err := leader.Run(ctx, leader.Config{
			Enabled:       cfg.LeaderElection,
			LeaseName:     cfg.LeaseName,
			Namespace:     cfg.PodNamespace,
			Identity:      cfg.InstanceID,
			LeaseDuration: cfg.LeaseDuration,
			RenewDeadline: cfg.LeaseRenewDeadline,
			RetryPeriod:   cfg.LeaseRetryPeriod,
		}, logger, t.run)
		if err != nil {
			logger.Error("leader election", "error", err.Error())
			stop()
		}
	}()

	if err := core.Serve(ctx, logger, cfg.Port, mux); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}

	//Serve balik kalau ctx ke-cancel. Tunggu term leader selesai (polling + relay berhenti,
	//lease dilepas) sebelum producer dan store ditutup.
	stop()
	<-leaderDone
	producer.Close()

	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = st.Close(closeCtx)
	closeCancel()
}

// connectStore mencoba terus sampai storage siap (di Kubernetes tidak ada depends_on; pod
// aggregator bisa start sebelum Mongo). Pod belum Ready selama ini karena HTTP belum listen.
func connectStore(ctx context.Context, logger *slog.Logger, cfg config.Config) (*store.Store, error) {
	for {
		connectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		st, err := store.New(connectCtx, logger, cfg.MongoURI, cfg.MongoDB)
		cancel()
		if err == nil {
			return st, nil
		}
		if ctx.Err() != nil {
			return nil, err
		}
		logger.Warn("canonical store not reachable yet, retrying", "error", err.Error())
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// termRunner menjalankan satu masa jabatan leader.
type termRunner struct {
	cfg      config.Config
	logger   *slog.Logger
	store    *store.Store
	relay    *publisher.Relay
	producer *publisher.KafkaProducer
	mapper   *mapping.Mapper
	http     *core.HTTPClient
}

// run: ambil fencing token (epoch) baru, pulihkan status sumber, lalu polling + relay sampai
// lease hilang (leadCtx dibatalkan) atau storage menolak tulisan karena epoch basi.
func (t *termRunner) run(leadCtx context.Context) {
	termCtx, endTerm := context.WithCancel(leadCtx)
	defer endTerm()
	ctx := core.WithCorrelationID(termCtx, core.NewCorrelationID())

	epoch, err := t.store.AcquireEpoch(ctx, t.cfg.InstanceID)
	if err != nil {
		t.logger.ErrorContext(ctx, "cannot acquire leader epoch, giving up leadership", "error", err.Error())
		return
	}
	logger := t.logger.With("epoch", epoch)
	logger.InfoContext(ctx, "leader term started")

	stepDown := func(ctx context.Context, err error) {
		logger.WarnContext(ctx, "write rejected by fencing token, stepping down", "error", err.Error())
		endTerm()
	}

	tracker := ingest.NewStatusTracker(polledSources, t.cfg.SourceStaleAfter)
	if recs, err := t.store.SourceStatuses(ctx); err == nil {
		tracker.Restore(toIngestRecords(recs))
	} else {
		logger.WarnContext(ctx, "restore source status failed, starting empty", "error", err.Error())
	}
	tracker.OnChange(func(r ingest.Record) {
		if err := t.store.SaveSourceStatus(termCtx, epoch, toStoreRecord(r)); err != nil {
			if store.IsFenced(err) {
				stepDown(termCtx, err)
			} else if termCtx.Err() == nil {
				logger.Warn("persist source status failed", "source", r.Source, "error", err.Error())
			}
		}
	})

	//P5: hasil satu siklus poll + penanda outbox disimpan dalam satu transaksi ter-fence.
	handle := func(ctx context.Context, events []domain.HazardEvent) error {
		counts, err := t.store.SaveBatch(ctx, epoch, events)
		if err != nil {
			if store.IsFenced(err) {
				stepDown(ctx, err)
			}
			return err
		}
		logger.InfoContext(ctx, "events stored",
			"inserted", counts[store.Inserted],
			"updated", counts[store.Updated],
			"unchanged", counts[store.Unchanged])
		return nil
	}

	// Client sumber dibuat baru tiap masa jabatan: cache korelasi warning BMKG dan kursor
	// dibangun ulang dari riwayat penuh. Simpan bersifat idempoten (content hash), jadi data
	// yang sudah disimpan leader sebelumnya tidak menjadi duplikat dan tidak ada celah.
	bmkgClient, err := bmkg.NewClient(t.cfg.BMKGBaseURL, t.cfg.BMKGAPIKey, t.http, t.mapper, logger)
	if err != nil {
		logger.ErrorContext(ctx, "bmkg client", "error", err.Error())
		return
	}
	pvmbgClient, err := pvmbg.NewClient(t.cfg.PVMBGBaseURL, t.cfg.PVMBGToken, t.http, t.mapper, logger)
	if err != nil {
		logger.ErrorContext(ctx, "pvmbg client", "error", err.Error())
		return
	}

	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		t.producer.EnsureTopicUntilReady(termCtx, int32(t.cfg.KafkaTopicPartitions), t.cfg.KafkaTopicRetention)
		t.relay.Run(termCtx)
	}()

	ingest.Run(termCtx, logger, t.cfg.PollInterval, []ingest.SourceClient{bmkgClient, pvmbgClient}, handle, tracker)
	endTerm()
	<-relayDone
	logger.Info("leader term ended")
}

func toIngestRecords(recs []store.SourceStatusRecord) []ingest.Record {
	out := make([]ingest.Record, len(recs))
	for i, r := range recs {
		out[i] = ingest.Record(r)
	}
	return out
}

func toStoreRecord(r ingest.Record) store.SourceStatusRecord {
	return store.SourceStatusRecord(r)
}
