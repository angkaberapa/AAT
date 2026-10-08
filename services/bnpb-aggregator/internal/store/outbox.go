package store

// Transactional outbox (P5) + fix R4.
//
// Every hazard document carries an `outbox` sub-document. Save writes the event data and
// the outbox marker (`pending: true`, `version` + 1) in ONE update of ONE document, which
// MongoDB applies atomically: an event can never be stored without eventually being
// published, and polling never waits on Kafka. A separate relay (internal/publisher)
// publishes pending documents and marks them published.
//
// Every Save marks the event pending, even if its content did not change. A re-fetched,
// unchanged event is therefore published again; consumers handle that because delivery
// is at-least-once anyway and they deduplicate by (hazard_id, version).

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
)

// SaveResult tells the caller what Save did.
type SaveResult string

const (
	Inserted SaveResult = "inserted"
	Updated  SaveResult = "updated" // existing event written again (re-fetch or changed content)
)

type outboxMeta struct {
	Version       int        `bson:"version"`
	Pending       bool       `bson:"pending"`
	CorrelationID string     `bson:"correlation_id"` // poll cycle that produced this version
	ChangedAt     time.Time  `bson:"changed_at"`
	PublishedAt   *time.Time `bson:"published_at,omitempty"`
}

// OutboxEntry is one hazard version waiting to be published.
type OutboxEntry struct {
	Event         domain.HazardEvent
	Version       int
	CorrelationID string
	ChangedAt     time.Time
}

type outboxDoc struct {
	domain.HazardEvent `bson:",inline"`
	Outbox             outboxMeta `bson:"outbox"`
}

func outboxIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{{
		Keys: bson.D{{Key: "outbox.pending", Value: 1}, {Key: "outbox.changed_at", Value: 1}},
		Options: options.Index().
			SetName("outbox_pending").
			SetPartialFilterExpression(bson.D{{Key: "outbox.pending", Value: true}}),
	}}
}

// Save upserts e by (source, source_ref_id) in a single atomic update:
//   - event fields are set, except ingested_at, which is only written when the document
//     is created, so it keeps the time BNPB first received the event (R4)
//   - outbox.version is incremented and outbox.pending set, so the relay publishes it
func (s *Store) Save(ctx context.Context, e domain.HazardEvent) (SaveResult, error) {
	fields, err := toDocument(e)
	if err != nil {
		return "", err
	}
	delete(fields, "ingested_at")
	fields["outbox.pending"] = true
	fields["outbox.correlation_id"] = core.CorrelationID(ctx)
	fields["outbox.changed_at"] = time.Now().UTC()

	key := bson.D{{Key: "source", Value: e.Source}, {Key: "source_ref_id", Value: e.SourceRefID}}
	update := bson.D{
		{Key: "$set", Value: fields},
		{Key: "$inc", Value: bson.D{{Key: "outbox.version", Value: 1}}},
		{Key: "$setOnInsert", Value: bson.D{{Key: "ingested_at", Value: e.IngestedAt}}},
	}
	res, err := s.events.UpdateOne(ctx, key, update, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return "", fmt.Errorf("save hazard event %s/%s: %w", e.Source, e.SourceRefID, err)
	}
	if res.UpsertedCount == 1 {
		return Inserted, nil
	}
	return Updated, nil
}

func toDocument(e domain.HazardEvent) (bson.M, error) {
	raw, err := bson.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("encode hazard event: %w", err)
	}
	var doc bson.M
	if err := bson.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("encode hazard event: %w", err)
	}
	return doc, nil
}

// Pending returns up to limit hazard versions waiting to be published, oldest change first.
func (s *Store) Pending(ctx context.Context, limit int) ([]OutboxEntry, error) {
	cur, err := s.events.Find(ctx,
		bson.D{{Key: "outbox.pending", Value: true}},
		options.Find().SetSort(bson.D{{Key: "outbox.changed_at", Value: 1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, fmt.Errorf("find pending hazard events: %w", err)
	}
	var docs []outboxDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("decode pending hazard events: %w", err)
	}
	entries := make([]OutboxEntry, 0, len(docs))
	for _, d := range docs {
		entries = append(entries, OutboxEntry{
			Event:         d.HazardEvent,
			Version:       d.Outbox.Version,
			CorrelationID: d.Outbox.CorrelationID,
			ChangedAt:     d.Outbox.ChangedAt,
		})
	}
	return entries, nil
}

// MarkPublished clears the pending flag, but only for the version that was actually
// published: if the event changed again meanwhile, it stays pending.
func (s *Store) MarkPublished(ctx context.Context, hazardID string, version int) error {
	now := time.Now().UTC()
	_, err := s.events.UpdateOne(ctx,
		bson.D{
			{Key: "hazard_id", Value: hazardID},
			{Key: "outbox.version", Value: version},
			{Key: "outbox.pending", Value: true},
		},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "outbox.pending", Value: false},
			{Key: "outbox.published_at", Value: now},
		}}})
	if err != nil {
		return fmt.Errorf("mark hazard event %s v%d published: %w", hazardID, version, err)
	}
	return nil
}
