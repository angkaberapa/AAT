package coastal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/domain"
)

const (
	ReasonMissingField    = "missing_field"
	ReasonWrongType       = "wrong_type"
	ReasonBadQuality      = "bad_quality"
	ReasonUnknownStation  = "unknown_station"
	ReasonStale           = "stale"
	ReasonFutureTimestamp = "future_timestamp"
	ReasonInvalidRange    = "invalid_range"
	ReasonInvalidJSON     = "invalid_json"
)

const futureSkew = 2 * time.Minute

type ValidationError struct {
	Reason   string
	Field    string
	Message  string
	Critical bool
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Reason + ": " + e.Message
	}
	return e.Reason + ": " + e.Field + " " + e.Message
}

// Validate parses one raw observation and applies the Sensor Pantai contract.
// A malformed item returns a ValidationError; it must never make the caller
// drop the rest of the page.
func Validate(raw json.RawMessage, generatedAt, receivedAt time.Time, maxStaleness time.Duration) (domain.Observation, *ValidationError) {
	fields, err := objectFields(raw)
	if err != nil {
		return domain.Observation{}, &ValidationError{Reason: ReasonInvalidJSON, Message: err.Error()}
	}

	observationID, validationErr := stringField(fields, "observation_id")
	if validationErr != nil {
		return domain.Observation{}, validationErr
	}
	stationID, validationErr := stringField(fields, "station_id")
	if validationErr != nil {
		return domain.Observation{}, validationErr
	}
	observedAt, validationErr := timeField(fields, "observed_at")
	if validationErr != nil {
		return domain.Observation{}, validationErr
	}
	critical, validationErr := boolField(fields, "critical")
	if validationErr != nil {
		return domain.Observation{}, validationErr
	}
	quality, validationErr := stringField(fields, "quality")
	if validationErr != nil {
		return domain.Observation{}, validationErr
	}
	anomaly, validationErr := numberField(fields, "sea_level_anomaly_m")
	if validationErr != nil {
		return domain.Observation{}, validationErr
	}
	wave, validationErr := numberField(fields, "wave_height_m")
	if validationErr != nil {
		return domain.Observation{}, validationErr
	}

	if quality != "ok" {
		return domain.Observation{}, &ValidationError{
			Reason: ReasonBadQuality, Field: "quality", Message: fmt.Sprintf("is %q, want ok", quality), Critical: critical,
		}
	}
	if _, exists := LookupStation(stationID); !exists {
		return domain.Observation{}, &ValidationError{
			Reason: ReasonUnknownStation, Field: "station_id", Message: fmt.Sprintf("%q is not in the station table", stationID), Critical: critical,
		}
	}
	if observedAt.After(generatedAt.Add(futureSkew)) {
		return domain.Observation{}, &ValidationError{
			Reason: ReasonFutureTimestamp, Field: "observed_at", Message: "is in the future relative to generated_at", Critical: critical,
		}
	}
	if maxStaleness > 0 && observedAt.Before(generatedAt.Add(-maxStaleness)) {
		return domain.Observation{}, &ValidationError{
			Reason: ReasonStale, Field: "observed_at", Message: fmt.Sprintf("is older than %s", maxStaleness), Critical: critical,
		}
	}
	if *wave < 0 || *wave > 30 {
		return domain.Observation{}, &ValidationError{
			Reason: ReasonInvalidRange, Field: "wave_height_m", Message: "must be between 0 and 30", Critical: critical,
		}
	}
	if *anomaly < -10 || *anomaly > 10 {
		return domain.Observation{}, &ValidationError{
			Reason: ReasonInvalidRange, Field: "sea_level_anomaly_m", Message: "must be between -10 and 10", Critical: critical,
		}
	}

	return domain.Observation{
		ObservationID:    observationID,
		StationID:        stationID,
		ObservedAt:       observedAt.UTC(),
		ReceivedAt:       receivedAt.UTC(),
		GeneratedAt:      generatedAt.UTC(),
		Critical:         critical,
		Quality:          quality,
		SeaLevelAnomalyM: anomaly,
		WaveHeightM:      wave,
		RawJSON:          string(raw),
	}, nil
}

func objectFields(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("empty object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	if fields == nil {
		return nil, fmt.Errorf("not a JSON object")
	}
	return fields, nil
}

func stringField(fields map[string]json.RawMessage, name string) (string, *ValidationError) {
	raw, exists := fields[name]
	if !exists || isNull(raw) {
		return "", &ValidationError{Reason: ReasonMissingField, Field: name, Message: "is required"}
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", &ValidationError{Reason: ReasonWrongType, Field: name, Message: "must be a string"}
	}
	if value == "" {
		return "", &ValidationError{Reason: ReasonMissingField, Field: name, Message: "must not be empty"}
	}
	return value, nil
}

func timeField(fields map[string]json.RawMessage, name string) (time.Time, *ValidationError) {
	raw, exists := fields[name]
	if !exists || isNull(raw) {
		return time.Time{}, &ValidationError{Reason: ReasonMissingField, Field: name, Message: "is required"}
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return time.Time{}, &ValidationError{Reason: ReasonWrongType, Field: name, Message: "must be an RFC3339 string"}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, &ValidationError{Reason: ReasonWrongType, Field: name, Message: "must be an RFC3339 timestamp"}
	}
	return parsed, nil
}

func boolField(fields map[string]json.RawMessage, name string) (bool, *ValidationError) {
	raw, exists := fields[name]
	if !exists || isNull(raw) {
		return false, &ValidationError{Reason: ReasonMissingField, Field: name, Message: "is required"}
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, &ValidationError{Reason: ReasonWrongType, Field: name, Message: "must be a boolean"}
	}
	return value, nil
}

func numberField(fields map[string]json.RawMessage, name string) (*float64, *ValidationError) {
	raw, exists := fields[name]
	if !exists || isNull(raw) {
		return nil, &ValidationError{Reason: ReasonMissingField, Field: name, Message: "is required"}
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, &ValidationError{Reason: ReasonWrongType, Field: name, Message: "must be a number"}
	}
	return &value, nil
}

func isNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}
