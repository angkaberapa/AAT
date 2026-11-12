package coastal

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/ingest"
)

const (
	SourceSensorPantai = "SENSOR_PANTAI"
	HazardTypeCoastal  = "COASTAL"

	SeverityWaspada = "WASPADA"
	SeveritySiaga   = "SIAGA"
	SeverityAwas    = "AWAS"
)

// HazardID must match the Aggregator formula:
// HZ- + first 16 bytes of SHA-256("source:source_ref_id").
func HazardID(observationID string) string {
	sum := sha256.Sum256([]byte(SourceSensorPantai + ":" + observationID))
	return "HZ-" + hex.EncodeToString(sum[:16])
}

// MapObservation turns a validated observation into the canonical COASTAL
// HazardEvent. Severity is provisional until the official sensor contract is
// available: critical maps to AWAS, large anomaly/wave maps to SIAGA.
func MapObservation(observation domain.Observation, station Station) ingest.HazardEvent {
	return ingest.HazardEvent{
		HazardID:    HazardID(observation.ObservationID),
		Source:      SourceSensorPantai,
		SourceRefID: observation.ObservationID,
		HazardType:  HazardTypeCoastal,
		Severity:    severity(observation),
		AreaName:    station.Province,
		Latitude:    station.Latitude,
		Longitude:   station.Longitude,
		OccurredAt:  observation.ObservedAt.UTC(),
		Attributes: map[string]any{
			"station_id":          observation.StationID,
			"quality":             observation.Quality,
			"sea_level_anomaly_m": valueOrNil(observation.SeaLevelAnomalyM),
			"wave_height_m":       valueOrNil(observation.WaveHeightM),
			"critical":            observation.Critical,
			"generated_at":        observation.GeneratedAt.UTC().Format(time.RFC3339Nano),
		},
	}
}

func severity(observation domain.Observation) string {
	if observation.Critical {
		return SeverityAwas
	}
	anomaly := 0.0
	if observation.SeaLevelAnomalyM != nil {
		anomaly = math.Abs(*observation.SeaLevelAnomalyM)
	}
	wave := 0.0
	if observation.WaveHeightM != nil {
		wave = *observation.WaveHeightM
	}
	if anomaly >= 0.8 || wave >= 2.5 {
		return SeveritySiaga
	}
	return SeverityWaspada
}

func valueOrNil(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}
