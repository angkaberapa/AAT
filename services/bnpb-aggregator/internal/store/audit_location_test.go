package store

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestCollectShardNamesFromExplainTree(t *testing.T) {
	t.Parallel()
	names := map[string]struct{}{}
	collectShardNames(bson.M{
		"queryPlanner": bson.M{
			"winningPlan": bson.M{
				"shards": bson.A{bson.M{"shardName": "shard-1"}},
			},
		},
	}, names)
	if _, ok := names["shard-1"]; !ok || len(names) != 1 {
		t.Fatalf("names = %#v", names)
	}
}

func TestParseShardHost(t *testing.T) {
	t.Parallel()
	set, hosts, err := parseShardHost("shard1-rs/a:27017,b:27017,c:27017")
	if err != nil {
		t.Fatal(err)
	}
	if set != "shard1-rs" || !reflect.DeepEqual(hosts, []string{"a:27017", "b:27017", "c:27017"}) {
		t.Fatalf("set=%q hosts=%v", set, hosts)
	}
}

func TestPodFromMongoHost(t *testing.T) {
	t.Parallel()
	if got := podFromMongoHost("mongo-shard-0-2.mongo-shard-0-headless:27017"); got != "mongo-shard-0-2" {
		t.Fatalf("pod = %q", got)
	}
}
