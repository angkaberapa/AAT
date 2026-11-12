package wildfire

import (
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/reference"
)

func TestBuildEvent(t *testing.T) {
	fire := Fire{
		ID:              "fire-abc",
		DetectionKeys:   []string{"A", "B"},
		Satellites:      []string{"N20", "N21"},
		DetectionCount:  2,
		TotalFRP:        25,
		MaxConfidence:   "h",
		FirstObservedAt: time.Date(2026, 11, 12, 2, 43, 0, 0, time.UTC),
		LastObservedAt:  time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC),
		Latitude:        -2.5,
		Longitude:       113.8,
	}
	event := BuildEvent(fire, reference.Province{Name: "Kalimantan Tengah"})
	if event.HazardID != HazardID("fire-abc") {
		t.Fatalf("hazard id = %q", event.HazardID)
	}
	if event.Source != SourceSatelit || event.HazardType != HazardWildfire {
		t.Fatalf("event = %+v", event)
	}
	if event.Severity != SeverityWaspada {
		t.Fatalf("severity = %q", event.Severity)
	}
	if event.AreaName != "Kalimantan Tengah" || event.Latitude != -2.5 || event.Longitude != 113.8 {
		t.Fatalf("event = %+v", event)
	}
	if event.Attributes["detection_count"] != 2 || event.Attributes["total_frp_mw"] != 25.0 {
		t.Fatalf("attributes = %+v", event.Attributes)
	}
}

func TestSeverityThresholds(t *testing.T) {
	cases := []struct {
		name     string
		count    int
		frp      float64
		expected string
	}{
		{name: "waspada", count: 2, frp: 10, expected: SeverityWaspada},
		{name: "siaga count", count: 6, frp: 10, expected: SeveritySiaga},
		{name: "siaga frp", count: 2, frp: 30, expected: SeveritySiaga},
		{name: "awas count", count: 20, frp: 10, expected: SeverityAwas},
		{name: "awas frp", count: 2, frp: 100, expected: SeverityAwas},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := severity(tc.count, tc.frp); got != tc.expected {
				t.Fatalf("severity = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestHazardIDStable(t *testing.T) {
	if HazardID("fire-abc") != HazardID("fire-abc") {
		t.Fatal("hazard id must be stable")
	}
	if HazardID("fire-abc") == HazardID("fire-def") {
		t.Fatal("different fires must have different ids")
	}
}
