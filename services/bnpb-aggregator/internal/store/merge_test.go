package store

import (
	"testing"
	"time"
)

func TestMergeAnalyticsCombinesAndSortsShardResults(t *testing.T) {
	at := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	result := mergeAnalytics([]AnalyticsResult{
		{Total: 2, TimeSeries: []TimeCount{{Bucket: at, HazardType: "SEISMIC", Count: 2}}, ByArea: []NamedCount{{Name: "Palu", Count: 2}}},
		{Total: 3, TimeSeries: []TimeCount{{Bucket: at, HazardType: "SEISMIC", Count: 1}, {Bucket: at, HazardType: "VOLCANIC", Count: 2}}, ByArea: []NamedCount{{Name: "Palu", Count: 1}, {Name: "Sleman", Count: 2}}},
	})
	if result.Total != 5 || timeCount(result.TimeSeries, "SEISMIC") != 3 || namedCount(result.ByArea, "Palu") != 3 {
		t.Fatalf("result = %#v", result)
	}
	if len(result.TimeSeries) != 2 || result.TimeSeries[0].HazardType != "SEISMIC" || result.TimeSeries[1].HazardType != "VOLCANIC" {
		t.Fatalf("time series order = %#v", result.TimeSeries)
	}
}
