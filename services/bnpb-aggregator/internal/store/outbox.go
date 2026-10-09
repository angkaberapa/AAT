package store

// Transactional outbox (P5) + fix R4.
//
// Every hazard document carries an `outbox` sub-document. Save writes the event data and
// the outbox marker (`pending: true`, `version` + 1) in ONE update of ONE document, which
// MongoDB applies atomically: an event can never be stored without eventually being
// published, and polling never waits on Kafka. A separate relay (internal/publisher)
// publishes pending documents and marks them published.
//
// Save only writes when the canonical content changed (compared by content hash), so
// `version` means "the content changed" and a re-fetched, unchanged event is not
// published again. Re-fetches happen on every aggregator restart: the BMKG client
// rebuilds its warning-correlation cache by fetching the full history (R1), so the
// polling cursor is deliberately not restored from the store (R7 not applied).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	Inserted  SaveResult = "inserted"
	Updated   SaveResult = "updated"   // canonical content changed, e.g. severity raised by a late warning
	Unchanged SaveResult = "unchanged" // same content as stored (re-fetch, retried batch): nothing written
)

type outboxMeta struct {
	Version       int        `bson:"version"`
	ContentHash   string     `bson:"content_hash"`
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

// ContentHash identifies the canonical content of an event. ingested_at is excluded: it
// records when BNPB first received the event, not what the event says. json.Marshal sorts
// map keys, so the random iteration order of Attributes does not change the hash.
func ContentHash(e domain.HazardEvent) (string, error) {
	e.IngestedAt = time.Time{}
	raw, err := json.Marshal(e)
	if err != nil {
		return "", fmt.Errorf("hash hazard event: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// Save upserts e by (source, source_ref_id) in a single atomic operation:
//   - new event        -> inserted, outbox.version 1, pending
//   - changed content  -> event fields updated (ingested_at kept, R4), version + 1, pending
//   - same content     -> nothing written, version unchanged, not published
//
// "Same content" is detected inside the same operation: the filter only matches a stored
// document whose content_hash differs. When the hash is equal the filter matches nothing,
// so the upsert tries to insert a second document with the same (source, source_ref_id)
// and the unique index rejects it with a duplicate key error, which means Unchanged.
func (s *Store) Save(ctx context.Context, e domain.HazardEvent) (SaveResult, error) {
	hash, err := ContentHash(e)
	if err != nil {
		return "", err
	}
	fields, err := toDocument(e)
	if err != nil {
		return "", err
	}
	delete(fields, "ingested_at")
	fields["outbox.content_hash"] = hash
	fields["outbox.pending"] = true
	fields["outbox.correlation_id"] = core.CorrelationID(ctx)
	fields["outbox.changed_at"] = time.Now().UTC()

	filter := bson.D{
		{Key: "source", Value: e.Source},
		{Key: "source_ref_id", Value: e.SourceRefID},
		{Key: "outbox.content_hash", Value: bson.D{{Key: "$ne", Value: hash}}},
	}
	update := bson.D{
		{Key: "$set", Value: fields},
		{Key: "$inc", Value: bson.D{{Key: "outbox.version", Value: 1}}},
		{Key: "$setOnInsert", Value: bson.D{{Key: "ingested_at", Value: e.IngestedAt}}},
	}
	res, err := s.events.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	if mongo.IsDuplicateKeyError(err) {
		return Unchanged, nil
	}
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
