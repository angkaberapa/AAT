package coastal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/domain"
)

var (
	generatedAt = time.Date(2026, 11, 12, 10, 30, 0, 0, time.UTC)
	receivedAt  = time.Date(2026, 11, 12, 10, 30, 1, 0, time.UTC)
)

func rawObservation(overrides map[string]any) json.RawMessage {
	value := map[string]any{
		"observation_id":      "obs-1",
		"station_id":          "stn-padang-01",
		"observed_at":         "2026-11-12T10:29:30Z",
		"critical":            false,
		"quality":             "ok",
		"sea_level_anomaly_m": 0.2,
		"wave_height_m":       1.1,
	}
	for key, override := range overrides {
		if override == nil {
			delete(value, key)
			continue
		}
		value[key] = override
	}
	raw, _ := json.Marshal(value)
	return raw
}

func TestValidateValidObservation(t *testing.T) {
	observation, err := Validate(rawObservation(map[string]any{"critical": true}), generatedAt, receivedAt, 15*time.Minute)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if observation.ObservationID != "obs-1" || observation.StationID != "stn-padang-01" {
		t.Fatalf("observation = %+v", observation)
	}
	if !observation.Critical || observation.Quality != "ok" {
		t.Fatalf("observation = %+v", observation)
	}
	if observation.SeaLevelAnomalyM == nil || *observation.SeaLevelAnomalyM != 0.2 {
		t.Fatalf("anomaly = %v", observation.SeaLevelAnomalyM)
	}
	if observation.GeneratedAt != generatedAt || observation.ReceivedAt != receivedAt {
		t.Fatalf("times = %+v", observation)
	}
}

func TestValidateFailures(t *testing.T) {
	cases := []struct {
		name     string
		raw      json.RawMessage
		reason   string
		field    string
		critical bool
	}{
		{name: "missing observation id", raw: rawObservation(map[string]any{"observation_id": nil}), reason: ReasonMissingField, field: "observation_id"},
		{name: "empty station", raw: rawObservation(map[string]any{"station_id": ""}), reason: ReasonMissingField, field: "station_id"},
		{name: "wrong observed type", raw: rawObservation(map[string]any{"observed_at": 123}), reason: ReasonWrongType, field: "observed_at"},
		{name: "bad quality", raw: rawObservation(map[string]any{"quality": "suspect"}), reason: ReasonBadQuality, field: "quality"},
		{name: "unknown station", raw: rawObservation(map[string]any{"station_id": "stn-nowhere"}), reason: ReasonUnknownStation, field: "station_id"},
		{name: "stale", raw: rawObservation(map[string]any{"observed_at": "2026-11-12T10:00:00Z"}), reason: ReasonStale, field: "observed_at"},
		{name: "future", raw: rawObservation(map[string]any{"observed_at": "2026-11-12T10:40:00Z"}), reason: ReasonFutureTimestamp, field: "observed_at"},
		{name: "negative wave", raw: rawObservation(map[string]any{"wave_height_m": -1}), reason: ReasonInvalidRange, field: "wave_height_m"},
		{name: "anomaly too large", raw: rawObservation(map[string]any{"sea_level_anomaly_m": 11}), reason: ReasonInvalidRange, field: "sea_level_anomaly_m"},
		{name: "null critical", raw: rawObservation(map[string]any{"critical": nil}), reason: ReasonMissingField, field: "critical"},
		{name: "not an object", raw: json.RawMessage(`[]`), reason: ReasonInvalidJSON},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Validate(tc.raw, generatedAt, receivedAt, 15*time.Minute)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if err.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q (%v)", err.Reason, tc.reason, err)
			}
			if tc.field != "" && err.Field != tc.field {
				t.Fatalf("field = %q, want %q", err.Field, tc.field)
			}
		})
	}
}

func TestValidateMarksCriticalFailure(t *testing.T) {
	_, err := Validate(rawObservation(map[string]any{
		"critical": true,
		"quality":  "bad_quality",
	}), generatedAt, receivedAt, 15*time.Minute)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !err.Critical {
		t.Fatal("critical flag should be preserved on validation error")
	}
	if err.Reason != ReasonBadQuality {
		t.Fatalf("reason = %q", err.Reason)
	}
}

func TestMapObservation(t *testing.T) {
	observation := domainObservation(t, rawObservation(map[string]any{"critical": true}))
	station, _ := LookupStation(observation.StationID)
	event := MapObservation(observation, station)

	if event.Source != SourceSensorPantai || event.HazardType != HazardTypeCoastal {
		t.Fatalf("event = %+v", event)
	}
	if event.SourceRefID != "obs-1" || event.Severity != SeverityAwas {
		t.Fatalf("event = %+v", event)
	}
	if event.AreaName != "Sumatera Barat" {
		t.Fatalf("area = %q", event.AreaName)
	}
	if event.HazardID != expectedHazardID("obs-1") {
		t.Fatalf("hazard id = %q", event.HazardID)
	}
	if event.Attributes["station_id"] != "stn-padang-01" {
		t.Fatalf("attributes = %+v", event.Attributes)
	}
}

func TestSeverity(t *testing.T) {
	cases := []struct {
		name     string
		raw      json.RawMessage
		expected string
	}{
		{name: "normal", raw: rawObservation(nil), expected: SeverityWaspada},
		{name: "anomaly", raw: rawObservation(map[string]any{"sea_level_anomaly_m": -0.9}), expected: SeveritySiaga},
		{name: "wave", raw: rawObservation(map[string]any{"wave_height_m": 3.0}), expected: SeveritySiaga},
		{name: "critical", raw: rawObservation(map[string]any{"critical": true}), expected: SeverityAwas},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			observation := domainObservation(t, tc.raw)
			station, _ := LookupStation(observation.StationID)
			if got := MapObservation(observation, station).Severity; got != tc.expected {
				t.Fatalf("severity = %q, want %q", got, tc.expected)
			}
		})
	}
}

func domainObservation(t *testing.T, raw json.RawMessage) domain.Observation {
	t.Helper()
	observation, err := Validate(raw, generatedAt, receivedAt, 15*time.Minute)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return observation
}

func expectedHazardID(observationID string) string {
	sum := sha256.Sum256([]byte(SourceSensorPantai + ":" + observationID))
	return "HZ-" + hex.EncodeToString(sum[:16])
}
