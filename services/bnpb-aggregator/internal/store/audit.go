package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	stdhash "hash"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
)

type AuditFilter struct {
	Source domain.Source
	Since  *time.Time
	Limit  int
	Cursor *AuditCursor
}

type AuditCursor struct {
	IngestedAt time.Time
	HazardID   string
}

type AuditPage struct {
	Items            []domain.HazardEvent
	Next             *AuditCursor
	Partial          bool
	UnavailableUnits []string
}

type ChecksumResult struct {
	Count            int64
	Checksum         string
	AsOf             time.Time
	Partial          bool
	UnavailableUnits []string
}

// AuditEvents implements the exact event ordering from the M2 Audit API contract:
// ingested_at ASC, hazard_id ASC. Since is inclusive; Cursor is an exclusive page boundary.
func (s *Store) AuditEvents(ctx context.Context, f AuditFilter) (AuditPage, error) {
	if f.Limit <= 0 {
		return AuditPage{}, fmt.Errorf("audit event page requires a positive limit")
	}

	page, err := auditEventsFromCollection(ctx, s.events, f)
	if err == nil {
		return page, nil
	}
	fallback, fallbackErr := s.auditEventsByShard(ctx, f)
	if fallbackErr == nil {
		return fallback, nil
	}
	return AuditPage{}, fmt.Errorf("find audit events: %w (fallback: %v)", err, fallbackErr)
}

func auditEventsFromCollection(ctx context.Context, collection *mongo.Collection, f AuditFilter) (AuditPage, error) {
	filter := auditEventsFilter(f)
	opts := options.Find().
		SetSort(bson.D{{Key: "ingested_at", Value: 1}, {Key: "hazard_id", Value: 1}}).
		SetLimit(int64(f.Limit + 1))
	cur, err := collection.Find(ctx, filter, opts)
	if err != nil {
		return AuditPage{}, fmt.Errorf("find audit events: %w", err)
	}

	items := []domain.HazardEvent{}
	if err := cur.All(ctx, &items); err != nil {
		return AuditPage{}, fmt.Errorf("decode audit events: %w", err)
	}
	if len(items) <= f.Limit {
		return AuditPage{Items: items}, nil
	}

	items = items[:f.Limit]
	last := items[len(items)-1]
	return AuditPage{
		Items: items,
		Next:  &AuditCursor{IngestedAt: last.IngestedAt, HazardID: last.HazardID},
	}, nil
}

func (s *Store) auditEventsByShard(ctx context.Context, f AuditFilter) (AuditPage, error) {
	mongos, err := s.isMongos(ctx)
	if err != nil || !mongos {
		return AuditPage{}, fmt.Errorf("partial fallback requires mongos")
	}
	shards, err := s.listShards(ctx)
	if err != nil {
		return AuditPage{}, err
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
		page, queryErr := auditEventsFromCollection(ctx, client.Database(s.database).Collection(collectionName), f)
		_ = client.Disconnect(context.WithoutCancel(ctx))
		if queryErr != nil {
			unavailable = append(unavailable, shard.ID)
			continue
		}
		hasMore = hasMore || page.Next != nil
		items = append(items, page.Items...)
	}
	if len(items) == 0 && len(unavailable) == len(shards) {
		return AuditPage{}, fmt.Errorf("no shard is reachable")
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].IngestedAt.Equal(items[j].IngestedAt) {
			return items[i].HazardID < items[j].HazardID
		}
		return items[i].IngestedAt.Before(items[j].IngestedAt)
	})
	page := AuditPage{Items: items, Partial: len(unavailable) > 0, UnavailableUnits: unavailable}
	if len(items) > f.Limit || hasMore {
		if len(page.Items) > f.Limit {
			page.Items = items[:f.Limit]
		}
		last := page.Items[len(page.Items)-1]
		page.Next = &AuditCursor{IngestedAt: last.IngestedAt, HazardID: last.HazardID}
	}
	return page, nil
}

func auditEventsFilter(f AuditFilter) bson.D {
	filter := bson.D{}
	if f.Source != "" {
		filter = append(filter, bson.E{Key: "source", Value: f.Source})
	}
	if f.Since != nil {
		filter = append(filter, bson.E{
			Key:   "ingested_at",
			Value: bson.D{{Key: "$gte", Value: f.Since.UTC()}},
		})
	}
	if f.Cursor != nil {
		filter = append(filter, bson.E{Key: "$or", Value: auditCursorPredicate(*f.Cursor)})
	}
	return filter
}

func auditCursorPredicate(cursor AuditCursor) bson.A {
	return bson.A{
		bson.D{{Key: "ingested_at", Value: bson.D{{Key: "$gt", Value: cursor.IngestedAt}}}},
		bson.D{
			{Key: "ingested_at", Value: cursor.IngestedAt},
			{Key: "hazard_id", Value: bson.D{{Key: "$gt", Value: cursor.HazardID}}},
		},
	}
}

// AuditChecksum streams source_ref_id in MongoDB's simple byte order. Writing the separator
// before every value except the first guarantees there is no trailing newline.
func (s *Store) AuditChecksum(ctx context.Context, source domain.Source) (ChecksumResult, error) {
	result, err := checksumFromCollection(ctx, s.events, source)
	if err == nil {
		return result, nil
	}
	fallback, fallbackErr := s.auditChecksumByShard(ctx, source)
	if fallbackErr == nil {
		return fallback, nil
	}
	return ChecksumResult{}, fmt.Errorf("find source references for checksum: %w (fallback: %v)", err, fallbackErr)
}

func checksumFromCollection(ctx context.Context, collection *mongo.Collection, source domain.Source) (ChecksumResult, error) {
	cur, err := checksumCursor(ctx, collection, source)
	if err != nil {
		return ChecksumResult{}, err
	}
	defer cur.Close(ctx)
	digest := sha256.New()
	var count int64
	for cur.Next(ctx) {
		var item struct {
			SourceRefID string `bson:"source_ref_id"`
		}
		if err := cur.Decode(&item); err != nil {
			return ChecksumResult{}, fmt.Errorf("decode source reference for checksum: %w", err)
		}
		appendChecksumValue(digest, count, item.SourceRefID)
		count++
	}
	if err := cur.Err(); err != nil {
		return ChecksumResult{}, fmt.Errorf("read source references for checksum: %w", err)
	}
	return ChecksumResult{Count: count, Checksum: hex.EncodeToString(digest.Sum(nil)), AsOf: time.Now().UTC()}, nil
}

func checksumCursor(ctx context.Context, collection *mongo.Collection, source domain.Source) (*mongo.Cursor, error) {
	return collection.Find(ctx,
		bson.D{{Key: "source", Value: source}},
		options.Find().
			SetProjection(bson.D{{Key: "_id", Value: 0}, {Key: "source_ref_id", Value: 1}}).
			SetSort(bson.D{{Key: "source_ref_id", Value: 1}}),
	)
}

func checksumValues(ctx context.Context, collection *mongo.Collection, source domain.Source) ([]string, error) {
	cur, err := checksumCursor(ctx, collection, source)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	values := []string{}
	for cur.Next(ctx) {
		var item struct {
			SourceRefID string `bson:"source_ref_id"`
		}
		if err := cur.Decode(&item); err != nil {
			return nil, fmt.Errorf("decode source reference for checksum: %w", err)
		}
		values = append(values, item.SourceRefID)
	}
	if err := cur.Err(); err != nil {
		return nil, fmt.Errorf("read source references for checksum: %w", err)
	}
	return values, nil
}

func checksumResult(values []string, partial bool, unavailable []string) ChecksumResult {
	sort.Strings(values)
	digest := sha256.New()
	for i, value := range values {
		appendChecksumValue(digest, int64(i), value)
	}
	return ChecksumResult{
		Count:    int64(len(values)),
		Checksum: hex.EncodeToString(digest.Sum(nil)),
		AsOf:     time.Now().UTC(),
		Partial:  partial, UnavailableUnits: unavailable,
	}
}

func (s *Store) auditChecksumByShard(ctx context.Context, source domain.Source) (ChecksumResult, error) {
	mongos, err := s.isMongos(ctx)
	if err != nil || !mongos {
		return ChecksumResult{}, fmt.Errorf("partial fallback requires mongos")
	}
	shards, err := s.listShards(ctx)
	if err != nil {
		return ChecksumResult{}, err
	}
	values := []string{}
	unavailable := []string{}
	for _, shard := range shards {
		client, connectErr := s.connectShard(shard.ReplicaSet, shard.Hosts)
		if connectErr != nil {
			unavailable = append(unavailable, shard.ID)
			continue
		}
		part, queryErr := checksumValues(ctx, client.Database(s.database).Collection(collectionName), source)
		_ = client.Disconnect(context.WithoutCancel(ctx))
		if queryErr != nil {
			unavailable = append(unavailable, shard.ID)
			continue
		}
		values = append(values, part...)
	}
	if len(values) == 0 && len(unavailable) == len(shards) {
		return ChecksumResult{}, fmt.Errorf("no shard is reachable")
	}
	return checksumResult(values, len(unavailable) > 0, unavailable), nil
}

func appendChecksumValue(digest stdhash.Hash, preceding int64, value string) {
	if preceding > 0 {
		_, _ = digest.Write([]byte{'\n'})
	}
	_, _ = digest.Write([]byte(value))
}
