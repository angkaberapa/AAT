package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	stdhash "hash"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
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
	Items []domain.HazardEvent
	Next  *AuditCursor
}

type ChecksumResult struct {
	Count    int64
	Checksum string
	AsOf     time.Time
}

// AuditEvents implements the exact event ordering from the M2 Audit API contract:
// ingested_at ASC, hazard_id ASC. Since is inclusive; Cursor is an exclusive page boundary.
func (s *Store) AuditEvents(ctx context.Context, f AuditFilter) (AuditPage, error) {
	if f.Limit <= 0 {
		return AuditPage{}, fmt.Errorf("audit event page requires a positive limit")
	}

	filter := auditEventsFilter(f)

	opts := options.Find().
		SetSort(bson.D{{Key: "ingested_at", Value: 1}, {Key: "hazard_id", Value: 1}}).
		SetLimit(int64(f.Limit + 1))
	cur, err := s.events.Find(ctx, filter, opts)
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
	cur, err := s.events.Find(ctx,
		bson.D{{Key: "source", Value: source}},
		options.Find().
			SetProjection(bson.D{{Key: "_id", Value: 0}, {Key: "source_ref_id", Value: 1}}).
			SetSort(bson.D{{Key: "source_ref_id", Value: 1}}),
	)
	if err != nil {
		return ChecksumResult{}, fmt.Errorf("find source references for checksum: %w", err)
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

	return ChecksumResult{
		Count:    count,
		Checksum: hex.EncodeToString(digest.Sum(nil)),
		AsOf:     time.Now().UTC(),
	}, nil
}

func appendChecksumValue(digest stdhash.Hash, preceding int64, value string) {
	if preceding > 0 {
		_, _ = digest.Write([]byte{'\n'})
	}
	_, _ = digest.Write([]byte(value))
}
