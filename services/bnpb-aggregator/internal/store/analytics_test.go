package store

import (
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestAnalyticsPipelineUsesRequestedRangeAndStableFacets(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	pipeline := analyticsPipeline(AnalyticsFilter{From: &from, To: &to, Bucket: "hour"})
	encoded, err := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: pipeline}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, expected := range []string{"$gte", "$lte", "$facet", "$dateTrunc", "hour", "TSUNAMI", "time_series", "by_severity", "by_area"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("pipeline does not contain %q: %s", expected, text)
		}
	}
}

func TestAnalyticsPipelineDefaultsToDailyBuckets(t *testing.T) {
	t.Parallel()
	encoded, err := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: analyticsPipeline(AnalyticsFilter{})}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"unit":"day"`) {
		t.Fatalf("pipeline = %s", encoded)
	}
}
