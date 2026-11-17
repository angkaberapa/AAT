package store

import (
	"context"
	"fmt"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type AnalyticsFilter struct {
	From   *time.Time
	To     *time.Time
	Bucket string
}

type TimeCount struct {
	Bucket     time.Time `json:"bucket" bson:"bucket"`
	HazardType string    `json:"hazard_type" bson:"hazard_type"`
	Count      int64     `json:"count" bson:"count"`
}

type NamedCount struct {
	Name  string `json:"name" bson:"name"`
	Count int64  `json:"count" bson:"count"`
}

type AnalyticsResult struct {
	Total            int64
	TimeSeries       []TimeCount
	BySeverity       []NamedCount
	ByArea           []NamedCount
	Partial          bool
	Degraded         bool
	UnavailableUnits []string
}

func (s *Store) Analytics(ctx context.Context, filter AnalyticsFilter) (AnalyticsResult, error) {
	result, err := analyticsCollection(ctx, s.events, filter)
	if err == nil {
		mongos, topologyErr := s.isMongos(ctx)
		if topologyErr == nil && mongos {
			result.UnavailableUnits = s.degradedReplicaUnits(ctx)
			result.Degraded = len(result.UnavailableUnits) > 0
		}
		return result, nil
	}
	partial, fallbackErr := s.analyticsByShard(ctx, filter)
	if fallbackErr == nil {
		return partial, nil
	}
	return AnalyticsResult{}, fmt.Errorf("aggregate canonical analytics: %w (fallback: %v)", err, fallbackErr)
}

func analyticsCollection(ctx context.Context, collection *mongo.Collection, filter AnalyticsFilter) (AnalyticsResult, error) {
	cursor, err := collection.Aggregate(ctx, analyticsPipeline(filter))
	if err != nil {
		return AnalyticsResult{}, err
	}
	defer cursor.Close(ctx)

	var rows []struct {
		Totals []struct {
			Count int64 `bson:"count"`
		} `bson:"totals"`
		TimeSeries []TimeCount  `bson:"time_series"`
		BySeverity []NamedCount `bson:"by_severity"`
		ByArea     []NamedCount `bson:"by_area"`
	}
	if err := cursor.All(ctx, &rows); err != nil {
		return AnalyticsResult{}, err
	}
	result := AnalyticsResult{
		TimeSeries: []TimeCount{},
		BySeverity: []NamedCount{},
		ByArea:     []NamedCount{},
	}
	if len(rows) == 0 {
		return result, nil
	}
	result.TimeSeries = rows[0].TimeSeries
	result.BySeverity = rows[0].BySeverity
	result.ByArea = rows[0].ByArea
	if len(rows[0].Totals) > 0 {
		result.Total = rows[0].Totals[0].Count
	}
	return result, nil
}

func (s *Store) analyticsByShard(ctx context.Context, filter AnalyticsFilter) (AnalyticsResult, error) {
	mongos, err := s.isMongos(ctx)
	if err != nil || !mongos {
		return AnalyticsResult{}, fmt.Errorf("partial fallback requires mongos")
	}
	shards, err := s.listShards(ctx)
	if err != nil {
		return AnalyticsResult{}, err
	}
	parts := make([]AnalyticsResult, 0, len(shards))
	unavailable := []string{}
	for _, shard := range shards {
		client, err := s.connectShard(shard.ReplicaSet, shard.Hosts)
		if err != nil {
			unavailable = append(unavailable, shard.ID)
			continue
		}
		part, queryErr := analyticsCollection(ctx, client.Database(s.database).Collection(collectionName), filter)
		_ = client.Disconnect(context.WithoutCancel(ctx))
		if queryErr != nil {
			unavailable = append(unavailable, shard.ID)
			continue
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return AnalyticsResult{}, fmt.Errorf("no shard is reachable")
	}
	result := mergeAnalytics(parts)
	result.Partial = len(unavailable) > 0
	result.Degraded = result.Partial
	result.UnavailableUnits = unavailable
	return result, nil
}

func (s *Store) isMongos(ctx context.Context) (bool, error) {
	var hello struct {
		Message string `bson:"msg"`
	}
	if err := s.client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		return false, err
	}
	return hello.Message == "isdbgrid", nil
}

func (s *Store) degradedReplicaUnits(ctx context.Context) []string {
	shards, err := s.listShards(ctx)
	if err != nil {
		return []string{"topology-metadata"}
	}
	units := []string{}
	for _, shard := range shards {
		client, err := s.connectShard(shard.ReplicaSet, shard.Hosts)
		if err != nil {
			units = append(units, shard.ID)
			continue
		}
		members, statusErr := replicaStatus(ctx, client)
		_ = client.Disconnect(context.WithoutCancel(ctx))
		if statusErr != nil {
			units = append(units, shard.ID)
			continue
		}
		for _, member := range members {
			if member.Health != 1 || (member.StateStr != "PRIMARY" && member.StateStr != "SECONDARY") {
				units = append(units, shard.ID+"/"+podFromMongoHost(member.Name))
			}
		}
	}
	sort.Strings(units)
	return units
}

func mergeAnalytics(parts []AnalyticsResult) AnalyticsResult {
	result := AnalyticsResult{TimeSeries: []TimeCount{}, BySeverity: []NamedCount{}, ByArea: []NamedCount{}}
	times := map[string]TimeCount{}
	severity := map[string]int64{}
	areas := map[string]int64{}
	for _, part := range parts {
		result.Total += part.Total
		for _, item := range part.TimeSeries {
			key := item.Bucket.UTC().Format(time.RFC3339Nano) + "|" + item.HazardType
			current := times[key]
			current.Bucket, current.HazardType, current.Count = item.Bucket, item.HazardType, current.Count+item.Count
			times[key] = current
		}
		for _, item := range part.BySeverity {
			severity[item.Name] += item.Count
		}
		for _, item := range part.ByArea {
			areas[item.Name] += item.Count
		}
	}
	for _, item := range times {
		result.TimeSeries = append(result.TimeSeries, item)
	}
	for name, count := range severity {
		result.BySeverity = append(result.BySeverity, NamedCount{Name: name, Count: count})
	}
	for name, count := range areas {
		result.ByArea = append(result.ByArea, NamedCount{Name: name, Count: count})
	}
	sort.Slice(result.TimeSeries, func(i, j int) bool {
		if result.TimeSeries[i].Bucket.Equal(result.TimeSeries[j].Bucket) {
			return result.TimeSeries[i].HazardType < result.TimeSeries[j].HazardType
		}
		return result.TimeSeries[i].Bucket.Before(result.TimeSeries[j].Bucket)
	})
	sort.Slice(result.BySeverity, func(i, j int) bool { return namedCountLess(result.BySeverity[i], result.BySeverity[j]) })
	sort.Slice(result.ByArea, func(i, j int) bool { return namedCountLess(result.ByArea[i], result.ByArea[j]) })
	return result
}

func namedCountLess(left, right NamedCount) bool {
	if left.Count == right.Count {
		return left.Name < right.Name
	}
	return left.Count > right.Count
}

func analyticsPipeline(filter AnalyticsFilter) bson.A {
	match := bson.D{}
	rangeFilter := bson.D{}
	if filter.From != nil {
		rangeFilter = append(rangeFilter, bson.E{Key: "$gte", Value: filter.From.UTC()})
	}
	if filter.To != nil {
		rangeFilter = append(rangeFilter, bson.E{Key: "$lte", Value: filter.To.UTC()})
	}
	if len(rangeFilter) > 0 {
		match = append(match, bson.E{Key: "occurred_at", Value: rangeFilter})
	}

	// M1 represents a tsunami warning inside its related SEISMIC HazardEvent. For analytics the
	// event is classified as TSUNAMI when that warning is present, otherwise as its canonical type.
	analyticsType := bson.D{{Key: "$cond", Value: bson.A{
		bson.D{{Key: "$and", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{"$hazard_type", "SEISMIC"}}},
			bson.D{{Key: "$or", Value: bson.A{
				bson.D{{Key: "$ne", Value: bson.A{bson.D{{Key: "$type", Value: "$attributes.tsunami_warning"}}, "missing"}}},
				bson.D{{Key: "$eq", Value: bson.A{"$attributes.potential_tsunami", true}}},
			}}},
		}}},
		"TSUNAMI",
		"$hazard_type",
	}}}

	bucket := filter.Bucket
	if bucket != "hour" {
		bucket = "day"
	}
	return bson.A{
		bson.D{{Key: "$match", Value: match}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "_analytics_type", Value: analyticsType}}}},
		bson.D{{Key: "$facet", Value: bson.D{
			{Key: "totals", Value: bson.A{
				bson.D{{Key: "$count", Value: "count"}},
			}},
			{Key: "time_series", Value: bson.A{
				bson.D{{Key: "$group", Value: bson.D{
					{Key: "_id", Value: bson.D{
						{Key: "bucket", Value: bson.D{{Key: "$dateTrunc", Value: bson.D{
							{Key: "date", Value: "$occurred_at"},
							{Key: "unit", Value: bucket},
							{Key: "timezone", Value: "UTC"},
						}}}},
						{Key: "hazard_type", Value: "$_analytics_type"},
					}},
					{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
				}}},
				bson.D{{Key: "$project", Value: bson.D{
					{Key: "_id", Value: 0},
					{Key: "bucket", Value: "$_id.bucket"},
					{Key: "hazard_type", Value: "$_id.hazard_type"},
					{Key: "count", Value: 1},
				}}},
				bson.D{{Key: "$sort", Value: bson.D{{Key: "bucket", Value: 1}, {Key: "hazard_type", Value: 1}}}},
			}},
			{Key: "by_severity", Value: namedCountFacet("$severity")},
			{Key: "by_area", Value: namedCountFacet("$area_name")},
		}}},
	}
}

func namedCountFacet(field string) bson.A {
	return bson.A{
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: field},
			{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
		}}},
		bson.D{{Key: "$project", Value: bson.D{
			{Key: "_id", Value: 0},
			{Key: "name", Value: "$_id"},
			{Key: "count", Value: 1},
		}}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "count", Value: -1}, {Key: "name", Value: 1}}}},
	}
}
