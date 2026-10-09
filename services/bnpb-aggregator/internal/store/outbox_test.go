package store

import (
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
)

func sampleEvent() domain.HazardEvent {
	return domain.HazardEvent{
		HazardID:    "HZ-1",
		Source:      domain.SourceBMKG,
		SourceRefID: "BMKG-EQ-000001",
		HazardType:  domain.HazardTypeSeismic,
		Severity:    domain.SeveritySiaga,
		AreaName:    "Selat Sunda",
		Latitude:    -6.95,
		Longitude:   105.2,
		OccurredAt:  time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC),
		IngestedAt:  time.Date(2026, 10, 9, 10, 0, 1, 0, time.UTC),
		Attributes:  map[string]any{"magnitude": 6.8, "depth_km": 10.0, "potential_tsunami": true},
	}
}

func hash(t *testing.T, e domain.HazardEvent) string {
	t.Helper()
	h, err := ContentHash(e)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestContentHashIgnoresIngestedAt(t *testing.T) {
	refetched := sampleEvent()
	refetched.IngestedAt = refetched.IngestedAt.Add(time.Hour) // mapped again after a restart
	if hash(t, sampleEvent()) != hash(t, refetched) {
		t.Fatal("re-ingesting the same event must not count as a change")
	}
}

func TestContentHashIsStableAcrossMapOrder(t *testing.T) {
	reordered := sampleEvent()
	reordered.Attributes = map[string]any{"potential_tsunami": true, "depth_km": 10.0, "magnitude": 6.8}
	for i := 0; i < 20; i++ { // map iteration order is random; check it repeatedly
		if hash(t, sampleEvent()) != hash(t, reordered) {
			t.Fatal("attribute order must not change the hash")
		}
	}
}

func TestContentHashDetectsRealChanges(t *testing.T) {
	base := hash(t, sampleEvent())

	severity := sampleEvent()
	severity.Severity = domain.SeverityAwas // late tsunami warning raised the severity
	newField := sampleEvent()
	newField.Attributes["confidence_level"] = 0.8 // additive schema change

	for name, e := range map[string]domain.HazardEvent{"severity": severity, "new attribute": newField} {
		if hash(t, e) == base {
			t.Fatalf("%s change not detected", name)
		}
	}
}
