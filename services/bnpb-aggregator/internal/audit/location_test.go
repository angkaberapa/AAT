package audit

import (
	"context"
	"errors"
	"testing"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

type fakeLocationReader struct {
	result *store.AuditLocationResult
}

func (f fakeLocationReader) AuditLocation(context.Context, string) (*store.AuditLocationResult, error) {
	return f.result, nil
}

type mapNodeLookup map[string]string

func (m mapNodeLookup) NodeForPod(_ context.Context, pod string) (string, error) {
	if node, ok := m[pod]; ok {
		return node, nil
	}
	return "", errors.New("pod missing")
}

func TestLocationServiceResolvesNodesAndMarksMissingReplicaPartial(t *testing.T) {
	t.Parallel()

	service := &locationService{
		reader: fakeLocationReader{result: &store.AuditLocationResult{
			HazardID: "HZ-1",
			Shard:    "shard-0",
			Replicas: []store.AuditReplica{
				{Pod: "mongo-shard-0-2", Role: "replica"},
				{Pod: "mongo-shard-0-0", Role: "primary"},
				{Pod: "mongo-shard-0-1", Role: "replica"},
			},
		}},
		nodes: mapNodeLookup{
			"mongo-shard-0-0": "node-a",
			"mongo-shard-0-1": "node-b",
		},
	}
	got, err := service.Locate(context.Background(), "HZ-1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Partial || len(got.Entries) != 2 {
		t.Fatalf("location = %#v", got)
	}
	if got.Entries[0] != (LocationEntry{Node: "node-a", Unit: "shard-0", Role: "primary"}) {
		t.Fatalf("primary = %#v", got.Entries[0])
	}
	if got.Entries[1] != (LocationEntry{Node: "node-b", Unit: "shard-0-r1", Role: "replica"}) {
		t.Fatalf("replica = %#v", got.Entries[1])
	}
}
