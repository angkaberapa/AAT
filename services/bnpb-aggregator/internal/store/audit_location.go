package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

// AuditReplica describes one currently reachable replica of the shard that owns an event.
// Pod is the StatefulSet pod hostname; the audit layer resolves it to a Kubernetes node.
type AuditReplica struct {
	Pod  string
	Role string
}

type AuditLocationResult struct {
	HazardID string
	Shard    string
	Replicas []AuditReplica
	Partial  bool
}

type shardInfo struct {
	ID         string
	ReplicaSet string
	Hosts      []string
}

type replicaMemberStatus struct {
	Name     string  `bson:"name"`
	StateStr string  `bson:"stateStr"`
	Health   float64 `bson:"health"`
}

// AuditLocation asks mongos which shard owns hazardID, then reads that replica set's live
// membership. It deliberately does not infer placement from the shard-key split because chunks
// can move while the balancer is running.
func (s *Store) AuditLocation(ctx context.Context, hazardID string) (*AuditLocationResult, error) {
	var found struct {
		HazardID string `bson:"hazard_id"`
	}
	if err := s.events.FindOne(ctx, bson.D{{Key: "hazard_id", Value: hazardID}}).Decode(&found); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, fmt.Errorf("find hazard for audit location: %w", err)
	}

	shard, err := s.ownerShard(ctx, hazardID)
	if err != nil {
		return nil, err
	}
	replicaSet, hosts, err := s.shardConnection(ctx, shard)
	if err != nil {
		return nil, err
	}

	client, err := s.connectShard(replicaSet, hosts)
	if err != nil {
		return nil, fmt.Errorf("connect to owner shard %s: %w", shard, err)
	}
	defer client.Disconnect(context.WithoutCancel(ctx)) //nolint:errcheck -- best-effort audit connection cleanup

	members, err := replicaStatus(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("replica status for %s: %w", shard, err)
	}

	result := &AuditLocationResult{HazardID: hazardID, Shard: shard, Replicas: []AuditReplica{}}
	for _, member := range members {
		if member.Health != 1 {
			result.Partial = true
			continue
		}
		role := ""
		switch member.StateStr {
		case "PRIMARY":
			role = "primary"
		case "SECONDARY":
			role = "replica"
		default:
			result.Partial = true
			continue
		}
		result.Replicas = append(result.Replicas, AuditReplica{Pod: podFromMongoHost(member.Name), Role: role})
	}
	if len(result.Replicas) == 0 {
		return nil, fmt.Errorf("owner shard %s has no reachable data-bearing replicas", shard)
	}
	return result, nil
}

func (s *Store) ownerShard(ctx context.Context, hazardID string) (string, error) {
	command := bson.D{
		{Key: "explain", Value: bson.D{
			{Key: "find", Value: collectionName},
			{Key: "filter", Value: bson.D{{Key: "hazard_id", Value: hazardID}}},
			{Key: "limit", Value: 1},
		}},
		{Key: "verbosity", Value: "queryPlanner"},
	}
	var explained bson.M
	if err := s.client.Database(s.database).RunCommand(ctx, command).Decode(&explained); err != nil {
		return "", fmt.Errorf("explain audit location: %w", err)
	}
	shards := map[string]struct{}{}
	collectShardNames(explained, shards)
	if len(shards) != 1 {
		return "", fmt.Errorf("expected one owner shard for %s, got %d", hazardID, len(shards))
	}
	for shard := range shards {
		return shard, nil
	}
	panic("unreachable")
}

func (s *Store) shardConnection(ctx context.Context, shardID string) (string, []string, error) {
	shards, err := s.listShards(ctx)
	if err != nil {
		return "", nil, err
	}
	for _, shard := range shards {
		if shard.ID == shardID {
			return shard.ReplicaSet, shard.Hosts, nil
		}
	}
	return "", nil, fmt.Errorf("owner shard %s not returned by listShards", shardID)
}

func (s *Store) listShards(ctx context.Context) ([]shardInfo, error) {
	var response struct {
		Shards []struct {
			ID   string `bson:"_id"`
			Host string `bson:"host"`
		} `bson:"shards"`
	}
	if err := s.client.Database("admin").RunCommand(ctx, bson.D{{Key: "listShards", Value: 1}}).Decode(&response); err != nil {
		return nil, fmt.Errorf("list shards: %w", err)
	}
	result := make([]shardInfo, 0, len(response.Shards))
	for _, shard := range response.Shards {
		set, hosts, err := parseShardHost(shard.Host)
		if err != nil {
			return nil, fmt.Errorf("parse shard %s: %w", shard.ID, err)
		}
		result = append(result, shardInfo{ID: shard.ID, ReplicaSet: set, Hosts: hosts})
	}
	return result, nil
}

func (s *Store) connectShard(replicaSet string, hosts []string) (*mongo.Client, error) {
	return mongo.Connect(options.Client().
		ApplyURI(s.mongoURI).
		SetHosts(hosts).
		SetReplicaSet(replicaSet).
		SetDirect(false).
		SetReadConcern(readconcern.Majority()).
		SetWriteConcern(writeconcern.Majority()).
		SetServerSelectionTimeout(serverSelectionTimeout))
}

func replicaStatus(ctx context.Context, client *mongo.Client) ([]replicaMemberStatus, error) {
	var status struct {
		Members []replicaMemberStatus `bson:"members"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}).Decode(&status); err != nil {
		return nil, err
	}
	return status.Members, nil
}

func collectShardNames(value any, names map[string]struct{}) {
	switch typed := value.(type) {
	case bson.M:
		for key, child := range typed {
			if key == "shardName" {
				if name, ok := child.(string); ok && name != "" {
					names[name] = struct{}{}
				}
			}
			collectShardNames(child, names)
		}
	case bson.D:
		for _, element := range typed {
			if element.Key == "shardName" {
				if name, ok := element.Value.(string); ok && name != "" {
					names[name] = struct{}{}
				}
			}
			collectShardNames(element.Value, names)
		}
	case bson.A:
		for _, child := range typed {
			collectShardNames(child, names)
		}
	case []any:
		for _, child := range typed {
			collectShardNames(child, names)
		}
	}
}

func parseShardHost(raw string) (string, []string, error) {
	set, hostList, ok := strings.Cut(raw, "/")
	if !ok || strings.TrimSpace(set) == "" || strings.TrimSpace(hostList) == "" {
		return "", nil, fmt.Errorf("invalid replica-set connection string %q", raw)
	}
	hosts := strings.Split(hostList, ",")
	for i := range hosts {
		hosts[i] = strings.TrimSpace(hosts[i])
		if hosts[i] == "" {
			return "", nil, fmt.Errorf("invalid replica-set connection string %q", raw)
		}
	}
	return strings.TrimSpace(set), hosts, nil
}

func podFromMongoHost(host string) string {
	host, _, _ = strings.Cut(host, ":")
	pod, _, _ := strings.Cut(host, ".")
	return pod
}
