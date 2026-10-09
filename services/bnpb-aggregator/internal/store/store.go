package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
)

// semua event masuk ke satu collection. belum ada rencana sharding.
const (
	collectionName = "hazard_events"
)

type Store struct {
	client *mongo.Client
	events *mongo.Collection
}
const serverSelectionTimeout = 3 * time.Second

// New bikin koneksi + pastiin index ada. sengaja gak pakai schema, biar nambah field di Attributes gak perlu migrasi.
func New(ctx context.Context, logger *slog.Logger, uri, database string) (*Store, error) {
	client, err := mongo.Connect(options.Client().
		ApplyURI(uri).
		SetBSONOptions(&options.BSONOptions{DefaultDocumentM: true}).
		SetServerSelectionTimeout(serverSelectionTimeout).
		SetMonitor(latencyMonitor(logger)))
	if err != nil {
		return nil, fmt.Errorf("connect mongo: %w", err)
	}

	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("ping mongo: %w", err)
	}

	events := client.Database(database).Collection(collectionName)

	//source + source_ref_id = natural key. retry/repoll harus jadi upsert, bukan duplikat.
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "source", Value: 1}, {Key: "source_ref_id", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		//todo: cek lagi apakah index ini kepake. setauku query List selalu pakai sort ingested_at, jadi kemungkinan iya.
		{Keys: bson.D{{Key: "ingested_at", Value: 1}}},
		//hazard_id = hash(source + source_ref_id). dipakai buat get by id.
		{
			Keys:    bson.D{{Key: "hazard_id", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
	}
	_, err = events.Indexes().CreateMany(ctx, append(indexes, outboxIndexes()...))
	if err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("create indexes: %w", err)
	}

	return &Store{client: client, events: events}, nil
}

func latencyMonitor(logger *slog.Logger) *event.CommandMonitor {
	return &event.CommandMonitor{
		Succeeded: func(ctx context.Context, e *event.CommandSucceededEvent) {
			logger.InfoContext(ctx, "outbound call", "target", "mongo", "command", e.CommandName, "latency_ms", millis(e.Duration))
		},
		Failed: func(ctx context.Context, e *event.CommandFailedEvent) {
			logger.WarnContext(ctx, "outbound call failed", "target", "mongo", "command", e.CommandName, "latency_ms", millis(e.Duration), "error", e.Failure.Error())
		},
	}
}

func millis(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

// Save ada di outbox.go: upsert idempoten + penanda outbox untuk publisher (P5).

// Filter buat List. field yang zero dianggap gak dipakai.
type Filter struct {
	Since      *time.Time //ingested_at > Since
	Source     domain.Source
	HazardType domain.HazardType
	Severity   domain.Severity
	//Limit > 0 batasi ukuran snapshot (P2): Limit event TERBARU, tetap urut naik.
	//Diabaikan kalau Since diisi: banyak event bisa punya ingested_at yang sama persis
	//(satu batch), jadi motong hasil inkremental bikin event dengan timestamp kembar kelewat.
	Limit int
}

// List balikin event yang match, urut dari yang paling lama di-ingest. hasilnya gak pernah nil, biar caller gak perlu cek.
func (s *Store) List(ctx context.Context, f Filter) ([]domain.HazardEvent, error) {
	filter := bson.D{}

	if f.Since != nil {
		filter = append(filter, bson.E{
			Key:   "ingested_at",
			Value: bson.D{{Key: "$gt", Value: *f.Since}},
		})
	}
	if f.Source != "" {
		filter = append(filter, bson.E{Key: "source", Value: f.Source})
	}
	if f.HazardType != "" {
		filter = append(filter, bson.E{Key: "hazard_type", Value: f.HazardType})
	}
	if f.Severity != "" {
		filter = append(filter, bson.E{Key: "severity", Value: f.Severity})
	}

	newestFirst := f.Limit > 0 && f.Since == nil
	opts := options.Find().SetSort(bson.D{{Key: "ingested_at", Value: 1}})
	if newestFirst {
		//ambil N terbaru (sort turun + limit), nanti dibalik lagi biar urutannya tetap naik.
		opts.SetSort(bson.D{{Key: "ingested_at", Value: -1}}).SetLimit(int64(f.Limit))
	}

	cur, err := s.events.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("find hazard events: %w", err)
	}

	//sengaja di-init, biar json-nya `[]` bukan `null` kalau kosong.
	events := []domain.HazardEvent{}
	if err := cur.All(ctx, &events); err != nil {
		return nil, fmt.Errorf("decode hazard events: %w", err)
	}
	if newestFirst {
		slices.Reverse(events)
	}
	return events, nil
}

// Get by hazard_id. return (nil, nil) kalau gak ketemu, caller yang mutusin mau 404 atau bukan.
func (s *Store) Get(ctx context.Context, hazardID string) (*domain.HazardEvent, error) {
	var e domain.HazardEvent

	err := s.events.FindOne(ctx, bson.D{{Key: "hazard_id", Value: hazardID}}).Decode(&e)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get hazard event %s: %w", hazardID, err)
	}
	return &e, nil
}

func (s *Store) Close(ctx context.Context) error {
	return s.client.Disconnect(ctx)
}