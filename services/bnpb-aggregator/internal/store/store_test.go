package store

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestEventIndexesSupportShardRoutingAndStableOrdering(t *testing.T) {
	t.Parallel()

	indexes := eventIndexes()
	wantKeys := []bson.D{
		{{Key: "hazard_id", Value: 1}},
		{{Key: "source", Value: 1}, {Key: "source_ref_id", Value: 1}},
		{{Key: "ingested_at", Value: 1}, {Key: "hazard_id", Value: 1}},
		{{Key: "source", Value: 1}, {Key: "ingested_at", Value: 1}, {Key: "hazard_id", Value: 1}},
		{{Key: "occurred_at", Value: -1}, {Key: "hazard_id", Value: -1}},
	}

	if len(indexes) != len(wantKeys) {
		t.Fatalf("eventIndexes() returned %d indexes, want %d", len(indexes), len(wantKeys))
	}
	for i, want := range wantKeys {
		got, ok := indexes[i].Keys.(bson.D)
		if !ok {
			t.Fatalf("index %d keys have type %T, want bson.D", i, indexes[i].Keys)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("index %d keys = %#v, want %#v", i, got, want)
		}
	}
	identityOptions := resolveIndexOptions(t, indexes[0].Options)
	if identityOptions.Unique == nil || !*identityOptions.Unique {
		t.Fatal("hazard_id index must enforce the authoritative event identity")
	}
	naturalKeyOptions := resolveIndexOptions(t, indexes[1].Options)
	if naturalKeyOptions.Unique != nil && *naturalKeyOptions.Unique {
		t.Fatal("source/source_ref_id index must not claim unsupported global uniqueness")
	}
}

func resolveIndexOptions(t *testing.T, builder *options.IndexOptionsBuilder) options.IndexOptions {
	t.Helper()
	var resolved options.IndexOptions
	if builder == nil {
		return resolved
	}
	for _, apply := range builder.List() {
		if err := apply(&resolved); err != nil {
			t.Fatalf("resolve index options: %v", err)
		}
	}
	return resolved
}
