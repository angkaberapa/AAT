package mapping

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/reference"
)

var (
	fixedOccurred = time.Date(2026, time.October, 3, 8, 0, 0, 0, time.UTC)
	fixedIngested = time.Date(2026, time.October, 3, 8, 0, 2, 0, time.UTC)
)

func testMapper(t *testing.T) *Mapper {
	t.Helper()
	mapper, err := NewMapper(map[string]reference.Volcano{
		"MERAPI": {Name: "Gunung Merapi", Latitude: -7.5407, Longitude: 110.4457},
	})
	if err != nil {
		t.Fatalf("NewMapper() error = %v", err)
	}
	mapper.SetClock(func() time.Time { return fixedIngested })
	return mapper
}

func TestMapVolcanicReportSupportsSchemaV1AndV2(t *testing.T) {
	t.Parallel()
	mapper := testMapper(t)

	var versionOne VolcanicReport
	if err := json.Unmarshal([]byte(`{
		"report_id":"PVMBG-RPT-000021",
		"volcano_id":"MERAPI",
		"alert_level":"Siaga",
		"eruption_count_24h":7,
		"ash_column_height_m":1500.0,
		"reported_at":"2026-10-03T08:00:00Z",
		"future_additive_field":{"preserved":true}
	}`), &versionOne); err != nil {
		t.Fatalf("decode schema v1: %v", err)
	}

	confidence := 0.82
	versionTwo := versionOne
	versionTwo.ConfidenceLevel = &confidence

	mappedV1, err := mapper.MapVolcanicReport(versionOne)
	if err != nil {
		t.Fatalf("MapVolcanicReport(v1) error = %v", err)
	}
	mappedV2, err := mapper.MapVolcanicReport(versionTwo)
	if err != nil {
		t.Fatalf("MapVolcanicReport(v2) error = %v", err)
	}

	if _, exists := mappedV1.Attributes["confidence_level"]; exists {
		t.Fatal("schema v1 unexpectedly contains confidence_level")
	}
	if got := mappedV2.Attributes["confidence_level"]; got != confidence {
		t.Fatalf("schema v2 confidence_level = %v, want %v", got, confidence)
	}
	future, exists := mappedV1.Attributes["future_additive_field"].(map[string]any)
	if !exists || future["preserved"] != true {
		t.Fatalf("unknown additive field was not forwarded: %#v", mappedV1.Attributes["future_additive_field"])
	}
	if mappedV1.HazardID != mappedV2.HazardID {
		t.Fatalf("schema evolution changed stable hazard ID: %q != %q", mappedV1.HazardID, mappedV2.HazardID)
	}
	if mappedV2.Source != domain.SourcePVMBG || mappedV2.HazardType != domain.HazardTypeVolcanic {
		t.Fatalf("unexpected canonical classification: %+v", mappedV2)
	}
	if mappedV2.Severity != domain.SeveritySiaga || mappedV2.AreaName != "Gunung Merapi" {
		t.Fatalf("unexpected volcanic mapping: %+v", mappedV2)
	}
}

func TestMapSeismicMagnitudeSeverityBoundaries(t *testing.T) {
	t.Parallel()
	mapper := testMapper(t)
	tests := []struct {
		name      string
		magnitude float64
		want      domain.Severity
	}{
		{name: "below five", magnitude: 4.9, want: domain.SeverityNormal},
		{name: "five", magnitude: 5.0, want: domain.SeverityWaspada},
		{name: "below six point five", magnitude: 6.4, want: domain.SeverityWaspada},
		{name: "six point five", magnitude: 6.5, want: domain.SeveritySiaga},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			event, err := mapper.MapSeismicEvent(SeismicEvent{
				EventID:      "BMKG-EQ-000001",
				Magnitude:    test.magnitude,
				DepthKM:      10,
				EpicenterLat: -6.2,
				EpicenterLon: 106.8,
				RegionName:   "Jakarta",
				OccurredAt:   fixedOccurred,
			}, nil)
			if err != nil {
				t.Fatalf("MapSeismicEvent() error = %v", err)
			}
			if event.Severity != test.want {
				t.Fatalf("severity = %q, want %q", event.Severity, test.want)
			}
		})
	}
}

func TestTsunamiWarningOverridesMagnitudeSeverity(t *testing.T) {
	t.Parallel()
	mapper := testMapper(t)
	event, err := mapper.MapSeismicEvent(
		SeismicEvent{
			EventID:          "BMKG-EQ-000002",
			Magnitude:        4.5,
			DepthKM:          15,
			EpicenterLat:     -7,
			EpicenterLon:     110,
			RegionName:       "Jawa",
			OccurredAt:       fixedOccurred,
			PotentialTsunami: true,
		},
		&TsunamiWarning{
			WarningID:        "BMKG-TW-000001",
			RelatedEventID:   "BMKG-EQ-000002",
			ThreatLevel:      "Awas",
			AffectedZones:    []string{"Banten"},
			EstimatedArrival: fixedOccurred.Add(20 * time.Minute),
			IssuedAt:         fixedOccurred.Add(time.Minute),
			AdditionalFields: map[string]any{"bulletin_source": "sensor-network-a"},
		},
	)
	if err != nil {
		t.Fatalf("MapSeismicEvent() error = %v", err)
	}
	if event.Severity != domain.SeverityAwas {
		t.Fatalf("severity = %q, want %q", event.Severity, domain.SeverityAwas)
	}
	if _, exists := event.Attributes["tsunami_warning"]; !exists {
		t.Fatal("mapped event does not contain tsunami warning attributes")
	}
	warningAttributes := event.Attributes["tsunami_warning"].(map[string]any)
	if warningAttributes["bulletin_source"] != "sensor-network-a" {
		t.Fatalf("unknown warning field was not forwarded: %#v", warningAttributes)
	}
}

func TestMapVolcanicReportRejectsUnknownReference(t *testing.T) {
	t.Parallel()
	mapper := testMapper(t)
	_, err := mapper.MapVolcanicReport(VolcanicReport{
		ReportID:   "PVMBG-RPT-999999",
		VolcanoID:  "UNKNOWN",
		AlertLevel: "Normal",
		ReportedAt: fixedOccurred,
	})
	if err == nil {
		t.Fatal("MapVolcanicReport() error = nil, want unknown volcano error")
	}
}
