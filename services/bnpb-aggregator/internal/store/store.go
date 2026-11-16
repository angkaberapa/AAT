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

const (
	collectionName = "hazard_events"
)

type Store struct {
	client     *mongo.Client
	events     *mongo.Collection
	leadership *mongo.Collection // P2 M2: dokumen fence (epoch leader)
	statuses   *mongo.Collection // P2 M2: status poll per sumber, dibaca semua instance
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

	if err := migrateLegacyEventIndexes(ctx, events); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("migrate event indexes: %w", err)
	}
	_, err = events.Indexes().CreateMany(ctx, append(eventIndexes(), outboxIndexes()...))
	if err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("create indexes: %w", err)
	}

	db := client.Database(database)
	return &Store{
		client:     client,
		events:     events,
		leadership: db.Collection(leadershipCollection),
		statuses:   db.Collection(sourceStatusCollect),
	}, nil
}

// eventIndexes returns indexes that remain valid after hazard_events is sharded by {hazard_id: 1}
func eventIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "hazard_id", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{{Key: "source", Value: 1}, {Key: "source_ref_id", Value: 1}},
			Options: options.Index().
				SetName("source_ref_lookup"),
		},
		{
			Keys: bson.D{{Key: "ingested_at", Value: 1}, {Key: "hazard_id", Value: 1}},
			Options: options.Index().
				SetName("audit_ingested_order"),
		},
		{
			Keys: bson.D{{Key: "source", Value: 1}, {Key: "ingested_at", Value: 1}, {Key: "hazard_id", Value: 1}},
			Options: options.Index().
				SetName("audit_source_ingested_order"),
		},
		{
			Keys: bson.D{{Key: "occurred_at", Value: -1}, {Key: "hazard_id", Value: -1}},
			Options: options.Index().
				SetName("latest_hazard_order"),
		},
	}
}

func migrateLegacyEventIndexes(ctx context.Context, events *mongo.Collection) error {
	cur, err := events.Indexes().List(ctx)
	if err != nil {
		var commandErr mongo.CommandError
		if errors.As(err, &commandErr) && commandErr.Code == 26 { // NamespaceNotFound
			return nil
		}
		return fmt.Errorf("list event indexes: %w", err)
	}
	defer cur.Close(ctx)

	var indexes []struct {
		Name   string `bson:"name"`
		Unique bool   `bson:"unique"`
	}
	if err := cur.All(ctx, &indexes); err != nil {
		return fmt.Errorf("decode event indexes: %w", err)
	}
	for _, index := range indexes {
		if index.Name != "source_1_source_ref_id_1" || !index.Unique {
			continue
		}
		if err := events.Indexes().DropOne(ctx, index.Name); err != nil {
			return fmt.Errorf("drop legacy unique source index: %w", err)
		}
	}
	return nil
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