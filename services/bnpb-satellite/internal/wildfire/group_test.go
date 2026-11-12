package wildfire

import (
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/domain"
)

var baseTime = time.Date(2026, 11, 12, 2, 43, 0, 0, time.UTC)

func detection(key, satellite string, lat, lon float64, at time.Time, frp float64, confidence string) domain.Detection {
	return domain.Detection{
		Key:        key,
		Satellite:  satellite,
		Latitude:   strconv.FormatFloat(lat, 'f', 5, 64),
		Longitude:  strconv.FormatFloat(lon, 'f', 5, 64),
		Confidence: confidence,
		FRP:        &frp,
		ObservedAt: at,
		Active:     true,
	}
}

func TestClusterConnectsWithinDistanceAndTime(t *testing.T) {
	result := Cluster([]domain.Detection{
		detection("N20|2026-11-12|0243|0.00000|0.00000", "N20", 0, 0, baseTime, 10, "h"),
		detection("N21|2026-11-12|0245|0.00000|0.00500", "N21", 0, 0.005, baseTime.Add(10*time.Minute), 20, "n"),
	})
	if len(result.Fires) != 1 || len(result.Singles) != 0 {
		t.Fatalf("result = %+v", result)
	}
	fire := result.Fires[0]
	if fire.DetectionCount != 2 || fire.TotalFRP != 30 || fire.MaxConfidence != "h" {
		t.Fatalf("fire = %+v", fire)
	}
	if len(fire.Satellites) != 2 || fire.Satellites[0] != "N20" || fire.Satellites[1] != "N21" {
		t.Fatalf("satellites = %+v", fire.Satellites)
	}
	if fire.ID != FireID("N20|2026-11-12|0243|0.00000|0.00000") {
		t.Fatalf("id = %q", fire.ID)
	}
	if fire.Latitude != 0 || math.Abs(fire.Longitude-0.0025) > 1e-9 {
		t.Fatalf("centroid = %f,%f", fire.Latitude, fire.Longitude)
	}
}

func TestClusterSeparatesByDistance(t *testing.T) {
	result := Cluster([]domain.Detection{
		detection("N20|2026-11-12|0243|0.00000|0.00000", "N20", 0, 0, baseTime, 10, "h"),
		detection("N20|2026-11-12|0243|0.00000|0.00500", "N20", 0, 0.005, baseTime, 10, "h"),
		detection("N20|2026-11-12|0243|0.00000|0.03000", "N20", 0, 0.03, baseTime, 10, "h"),
		detection("N20|2026-11-12|0243|0.00000|0.03500", "N20", 0, 0.035, baseTime, 10, "h"),
	})
	if len(result.Fires) != 2 || len(result.Singles) != 0 {
		t.Fatalf("result = %+v", result)
	}
}

func TestClusterSeparatesByTime(t *testing.T) {
	result := Cluster([]domain.Detection{
		detection("N20|2026-11-12|0243|0.00000|0.00000", "N20", 0, 0, baseTime, 10, "h"),
		detection("N20|2026-11-12|1300|0.00000|0.00000", "N20", 0, 0, baseTime.Add(400*time.Minute), 10, "h"),
	})
	if len(result.Fires) != 0 || len(result.Singles) != 2 {
		t.Fatalf("result = %+v", result)
	}
}

func TestClusterChains(t *testing.T) {
	result := Cluster([]domain.Detection{
		detection("A", "N20", 0, 0, baseTime, 10, "h"),
		detection("B", "N20", 0, 0.008, baseTime, 10, "h"),
		detection("C", "N21", 0, 0.016, baseTime, 10, "h"),
	})
	if len(result.Fires) != 1 || len(result.Singles) != 0 {
		t.Fatalf("result = %+v", result)
	}
	if got := len(result.Fires[0].DetectionKeys); got != 3 {
		t.Fatalf("detections = %d", got)
	}
}

func TestClusterSingleDetectionIsNotFire(t *testing.T) {
	result := Cluster([]domain.Detection{
		detection("A", "N20", 0, 0, baseTime, 10, "h"),
	})
	if len(result.Fires) != 0 || len(result.Singles) != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestFireIDStableIndependentOfInputOrder(t *testing.T) {
	first := Cluster([]domain.Detection{
		detection("A", "N20", 0, 0, baseTime, 10, "h"),
		detection("B", "N20", 0, 0.005, baseTime, 10, "h"),
	})
	second := Cluster([]domain.Detection{
		detection("B", "N20", 0, 0.005, baseTime, 10, "h"),
		detection("A", "N20", 0, 0, baseTime, 10, "h"),
	})
	if len(first.Fires) != 1 || len(second.Fires) != 1 {
		t.Fatalf("first = %+v, second = %+v", first, second)
	}
	if first.Fires[0].ID != second.Fires[0].ID {
		t.Fatalf("ids = %q and %q", first.Fires[0].ID, second.Fires[0].ID)
	}
}

func TestClusterEmpty(t *testing.T) {
	result := Cluster(nil)
	if len(result.Fires) != 0 || len(result.Singles) != 0 {
		t.Fatalf("result = %+v", result)
	}
}
