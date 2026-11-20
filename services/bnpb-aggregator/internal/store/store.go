package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
)

const (
	collectionName = "hazard_events"
)

type Store struct {
	client       *mongo.Client
	events       *mongo.Collection
	leadership   *mongo.Collection // P2 M2: dokumen fence (epoch leader)
	statuses     *mongo.Collection // P2 M2: status poll per sumber, dibaca semua instance
	unprocessed  *mongo.Collection // P3: raw messages that could not be processed
	fieldReports *mongo.Collection // P3: durable Tim Lapangan inbox
	mongoURI     string
	database     string
}

const serverSelectionTimeout = 3 * time.Second

// New bikin koneksi + pastiin index ada. sengaja gak pakai schema, biar nambah field di Attributes gak perlu migrasi.
func New(ctx context.Context, logger *slog.Logger, uri, database string) (*Store, error) {
	client, err := mongo.Connect(options.Client().
		ApplyURI(uri).
		SetBSONOptions(&options.BSONOptions{DefaultDocumentM: true}).
		SetReadConcern(readconcern.Majority()).
		SetWriteConcern(writeconcern.Majority()).
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
	unprocessed := db.Collection(unprocessedCollection)
	if _, err := unprocessed.Indexes().CreateMany(ctx, unprocessedIndexes()); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("create unprocessed indexes: %w", err)
	}
	fieldReports := db.Collection(fieldReportsCollection)
	if _, err := fieldReports.Indexes().CreateMany(ctx, fieldReportIndexes()); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("create field report indexes: %w", err)
	}
	return &Store{
		client:       client,
		events:       events,
		leadership:   db.Collection(leadershipCollection),
		statuses:     db.Collection(sourceStatusCollect),
		unprocessed:  unprocessed,
		fieldReports: fieldReports,
		mongoURI:     uri,
		database:     database,
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
		// GET /hazards?hazard_type=X (urut ingested_at): tanpa index ini Mongo menelusuri index
		// ingested_at dan membuang jenis lain. Dengan 105 ribu event SENSOR_PANTAI, mencari 50 event
		// SEISMIC memeriksa ~6.800 dokumen (~400 ms) dan aggregator jenuh di uji 10k VU.
		// Ditulis dengan bantuan LLM (Claude).
		{
			Keys: bson.D{{Key: "hazard_type", Value: 1}, {Key: "ingested_at", Value: 1}, {Key: "hazard_id", Value: 1}},
			Options: options.Index().
				SetName("hazard_type_ingested_order"),
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

// latencyMonitor mencatat latensi tiap perintah Mongo di log, dan (M2) membuat span tracing per
// perintah supaya langkah storage terlihat di trace. Span ditutup lewat RequestID. Bagian span
// ditulis dengan bantuan LLM (Claude).
func latencyMonitor(logger *slog.Logger) *event.CommandMonitor {
	tracer := otel.Tracer("bnpb-aggregator/mongo")
	var spans sync.Map // RequestID -> trace.Span
	end := func(requestID int64, err error) {
		if v, ok := spans.LoadAndDelete(requestID); ok {
			span := v.(trace.Span)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			}
			span.End()
		}
	}
	return &event.CommandMonitor{
		Started: func(ctx context.Context, e *event.CommandStartedEvent) {
			// Hanya perintah yang punya induk trace (request / siklus poll), bukan heartbeat driver.
			if !trace.SpanContextFromContext(ctx).IsValid() {
				return
			}
			_, span := tracer.Start(ctx, "mongo "+e.CommandName,
				trace.WithSpanKind(trace.SpanKindClient),
				trace.WithAttributes(
					attribute.String("db.system", "mongodb"),
					attribute.String("db.name", e.DatabaseName),
					attribute.String("db.operation", e.CommandName),
				))
			spans.Store(e.RequestID, span)
		},
		Succeeded: func(ctx context.Context, e *event.CommandSucceededEvent) {
			end(e.RequestID, nil)
			logger.InfoContext(ctx, "outbound call", "target", "mongo", "command", e.CommandName, "latency_ms", millis(e.Duration))
		},
		Failed: func(ctx context.Context, e *event.CommandFailedEvent) {
			end(e.RequestID, e.Failure)
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
	Order      Order
	Cursor     *LatestCursor
	//Limit > 0 batasi ukuran snapshot (P2): Limit event TERBARU, tetap urut naik.
	//Diabaikan kalau Since diisi: banyak event bisa punya ingested_at yang sama persis
	//(satu batch), jadi motong hasil inkremental bikin event dengan timestamp kembar kelewat.
	Limit int
}

type Order string

const OrderLatest Order = "latest"

type LatestCursor struct {
	OccurredAt time.Time
	HazardID   string
}

type Page struct {
	Items            []domain.HazardEvent
	Next             *LatestCursor
	Partial          bool
	UnavailableUnits []string
}

// List balikin event yang match, urut dari yang paling lama di-ingest. hasilnya gak pernah nil, biar caller gak perlu cek.
func (s *Store) List(ctx context.Context, f Filter) ([]domain.HazardEvent, error) {
	page, err := s.ListPage(ctx, f)
	return page.Items, err
}

func (s *Store) ListPage(ctx context.Context, f Filter) (Page, error) {
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
	if f.Order == OrderLatest {
		page, err := listLatestPageFromCollection(ctx, s.events, filter, f)
		if err == nil {
			return page, nil
		}
		fallback, fallbackErr := s.listPageByShard(ctx, filter, f)
		if fallbackErr == nil {
			return fallback, nil
		}
		return Page{}, fmt.Errorf("find latest hazard events: %w (fallback: %v)", err, fallbackErr)
	}

	page, err := listPageFromCollection(ctx, s.events, filter, f)
	if err == nil {
		return page, nil
	}
	fallback, fallbackErr := s.listPageByShard(ctx, filter, f)
	if fallbackErr == nil {
		return fallback, nil
	}
	return Page{}, fmt.Errorf("find hazard events: %w (fallback: %v)", err, fallbackErr)
}

func listPageFromCollection(ctx context.Context, collection *mongo.Collection, filter bson.D, f Filter) (Page, error) {
	newestFirst := f.Limit > 0 && f.Since == nil
	opts := options.Find().SetSort(bson.D{{Key: "ingested_at", Value: 1}})
	if newestFirst {
		//ambil N terbaru (sort turun + limit), nanti dibalik lagi biar urutannya tetap naik.
		opts.SetSort(bson.D{{Key: "ingested_at", Value: -1}}).SetLimit(int64(f.Limit))
	}

	cur, err := collection.Find(ctx, filter, opts)
	if err != nil {
		return Page{}, fmt.Errorf("find hazard events: %w", err)
	}

	//sengaja di-init, biar json-nya `[]` bukan `null` kalau kosong.
	events := []domain.HazardEvent{}
	if err := cur.All(ctx, &events); err != nil {
		return Page{}, fmt.Errorf("decode hazard events: %w", err)
	}
	if newestFirst {
		slices.Reverse(events)
	}
	return Page{Items: events}, nil
}

func listLatestPageFromCollection(ctx context.Context, collection *mongo.Collection, filter bson.D, f Filter) (Page, error) {
	if f.Limit <= 0 {
		return Page{}, errors.New("latest hazard page requires a positive limit")
	}
	if f.Cursor != nil {
		filter = append(filter, bson.E{Key: "$or", Value: latestCursorPredicate(*f.Cursor)})
	}

	opts := options.Find().
		SetSort(bson.D{{Key: "occurred_at", Value: -1}, {Key: "hazard_id", Value: -1}}).
		SetLimit(int64(f.Limit + 1))
	cur, err := collection.Find(ctx, filter, opts)
	if err != nil {
		return Page{}, fmt.Errorf("find latest hazard events: %w", err)
	}

	events := []domain.HazardEvent{}
	if err := cur.All(ctx, &events); err != nil {
		return Page{}, fmt.Errorf("decode latest hazard events: %w", err)
	}
	if len(events) <= f.Limit {
		return Page{Items: events}, nil
	}

	events = events[:f.Limit]
	last := events[len(events)-1]
	return Page{
		Items: events,
		Next:  &LatestCursor{OccurredAt: last.OccurredAt, HazardID: last.HazardID},
	}, nil
}

func (s *Store) listPageByShard(ctx context.Context, filter bson.D, f Filter) (Page, error) {
	mongos, err := s.isMongos(ctx)
	if err != nil || !mongos {
		return Page{}, errors.New("partial fallback requires mongos")
	}
	shards, err := s.listShards(ctx)
	if err != nil {
		return Page{}, err
	}

	items := []domain.HazardEvent{}
	unavailable := []string{}
	hasMore := false
	for _, shard := range shards {
		client, connectErr := s.connectShard(shard.ReplicaSet, shard.Hosts)
		if connectErr != nil {
			unavailable = append(unavailable, shard.ID)
			continue
		}
		collection := client.Database(s.database).Collection(collectionName)
		var part Page
		if f.Order == OrderLatest {
			part, err = listLatestPageFromCollection(ctx, collection, filter, f)
		} else {
			part, err = listPageFromCollection(ctx, collection, filter, f)
		}
		_ = client.Disconnect(context.WithoutCancel(ctx))
		if err != nil {
			unavailable = append(unavailable, shard.ID)
			continue
		}
		hasMore = hasMore || part.Next != nil
		items = append(items, part.Items...)
	}
	if len(items) == 0 && len(unavailable) == len(shards) {
		return Page{}, errors.New("no shard is reachable")
	}
	sort.Slice(items, func(i, j int) bool {
		if f.Order == OrderLatest {
			if items[i].OccurredAt.Equal(items[j].OccurredAt) {
				return items[i].HazardID > items[j].HazardID
			}
			return items[i].OccurredAt.After(items[j].OccurredAt)
		}
		if items[i].IngestedAt.Equal(items[j].IngestedAt) {
			return items[i].HazardID < items[j].HazardID
		}
		return items[i].IngestedAt.Before(items[j].IngestedAt)
	})

	page := Page{Items: items, Partial: len(unavailable) > 0, UnavailableUnits: unavailable}
	if f.Order == OrderLatest && (len(items) > f.Limit || hasMore) {
		if len(page.Items) > f.Limit {
			page.Items = items[:f.Limit]
		}
		last := page.Items[len(page.Items)-1]
		page.Next = &LatestCursor{OccurredAt: last.OccurredAt, HazardID: last.HazardID}
	} else if f.Order != OrderLatest && f.Limit > 0 && f.Since == nil && len(items) > f.Limit {
		page.Items = items[len(items)-f.Limit:]
	}
	return page, nil
}

func latestCursorPredicate(cursor LatestCursor) bson.A {
	return bson.A{
		bson.D{{Key: "occurred_at", Value: bson.D{{Key: "$lt", Value: cursor.OccurredAt}}}},
		bson.D{
			{Key: "occurred_at", Value: cursor.OccurredAt},
			{Key: "hazard_id", Value: bson.D{{Key: "$lt", Value: cursor.HazardID}}},
		},
	}
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
