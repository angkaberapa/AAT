package store

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
)

func TestAuditEventsFilterUsesInclusiveSince(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	got := auditEventsFilter(AuditFilter{Source: domain.SourceBMKG, Since: &since})
	want := bson.D{
		{Key: "source", Value: domain.SourceBMKG},
		{Key: "ingested_at", Value: bson.D{{Key: "$gte", Value: since}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("auditEventsFilter() = %#v, want %#v", got, want)
	}
}

func TestAuditCursorPredicateUsesExclusiveStableBoundary(t *testing.T) {
	t.Parallel()

	when := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	got := auditCursorPredicate(AuditCursor{IngestedAt: when, HazardID: "HZ-8000"})
	want := bson.A{
		bson.D{{Key: "ingested_at", Value: bson.D{{Key: "$gt", Value: when}}}},
		bson.D{
			{Key: "ingested_at", Value: when},
			{Key: "hazard_id", Value: bson.D{{Key: "$gt", Value: "HZ-8000"}}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("auditCursorPredicate() = %#v, want %#v", got, want)
	}
}

func TestAppendChecksumValueHasNewlinesOnlyBetweenValues(t *testing.T) {
	t.Parallel()

	digest := sha256.New()
	values := []string{"BMKG-001", "BMKG-002", "BMKG-010"}
	for i, value := range values {
		appendChecksumValue(digest, int64(i), value)
	}
	got := hex.EncodeToString(digest.Sum(nil))
	wantBytes := sha256.Sum256([]byte("BMKG-001\nBMKG-002\nBMKG-010"))
	want := hex.EncodeToString(wantBytes[:])
	if got != want {
		t.Fatalf("checksum = %s, want %s", got, want)
	}
}

func TestEmptyChecksumIsSHA256OfEmptyInput(t *testing.T) {
	t.Parallel()

	digest := sha256.New()
	got := hex.EncodeToString(digest.Sum(nil))
	wantBytes := sha256.Sum256(nil)
	want := hex.EncodeToString(wantBytes[:])
	if got != want {
		t.Fatalf("empty checksum = %s, want %s", got, want)
	}
}
