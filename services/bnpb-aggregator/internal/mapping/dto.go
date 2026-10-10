package mapping

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

type Envelope[T any] struct {
	Data []T `json:"data"`
}

type SeismicEvent struct {
	EventID          string         `json:"event_id"`
	Magnitude        float64        `json:"magnitude"`
	DepthKM          float64        `json:"depth_km"`
	EpicenterLat     float64        `json:"epicenter_lat"`
	EpicenterLon     float64        `json:"epicenter_lon"`
	RegionName       string         `json:"region_name"`
	OccurredAt       time.Time      `json:"occurred_at"`
	PotentialTsunami bool           `json:"potential_tsunami"`
	AdditionalFields map[string]any `json:"-"`
}

type TsunamiWarning struct {
	WarningID        string         `json:"warning_id"`
	RelatedEventID   string         `json:"related_event_id"`
	ThreatLevel      string         `json:"threat_level"`
	AffectedZones    []string       `json:"affected_zones"`
	EstimatedArrival time.Time      `json:"estimated_arrival"`
	IssuedAt         time.Time      `json:"issued_at"`
	AdditionalFields map[string]any `json:"-"`
}

type VolcanicReport struct {
	ReportID         string         `json:"report_id"`
	VolcanoID        string         `json:"volcano_id"`
	AlertLevel       string         `json:"alert_level"`
	EruptionCount24H int            `json:"eruption_count_24h"`
	AshColumnHeightM float64        `json:"ash_column_height_m"`
	ReportedAt       time.Time      `json:"reported_at"`
	ConfidenceLevel  *float64       `json:"confidence_level,omitempty"`
	AdditionalFields map[string]any `json:"-"`
}

func (event *SeismicEvent) UnmarshalJSON(data []byte) error {
	type plain SeismicEvent
	var decoded plain
	extra, err := decodeWithUnknownFields(data, &decoded, map[string]struct{}{
		"event_id": {}, "magnitude": {}, "depth_km": {},
		"epicenter_lat": {}, "epicenter_lon": {}, "region_name": {},
		"occurred_at": {}, "potential_tsunami": {},
	}, "event_id", "magnitude", "depth_km", "epicenter_lat", "epicenter_lon",
		"region_name", "occurred_at", "potential_tsunami")
	if err != nil {
		return err
	}
	*event = SeismicEvent(decoded)
	event.AdditionalFields = extra
	return nil
}

func (warning *TsunamiWarning) UnmarshalJSON(data []byte) error {
	type plain TsunamiWarning
	var decoded plain
	extra, err := decodeWithUnknownFields(data, &decoded, map[string]struct{}{
		"warning_id": {}, "related_event_id": {}, "threat_level": {},
		"affected_zones": {}, "estimated_arrival": {}, "issued_at": {},
	}, "warning_id", "related_event_id", "threat_level", "affected_zones",
		"estimated_arrival")
	if err != nil {
		return err
	}
	*warning = TsunamiWarning(decoded)
	warning.AdditionalFields = extra
	return nil
}

func (report *VolcanicReport) UnmarshalJSON(data []byte) error {
	type plain VolcanicReport
	var decoded plain
	extra, err := decodeWithUnknownFields(data, &decoded, map[string]struct{}{
		"report_id": {}, "volcano_id": {}, "alert_level": {},
		"eruption_count_24h": {}, "ash_column_height_m": {},
		"reported_at": {}, "confidence_level": {},
	}, "report_id", "volcano_id", "alert_level", "eruption_count_24h",
		"ash_column_height_m", "reported_at")
	if err != nil {
		return err
	}
	*report = VolcanicReport(decoded)
	report.AdditionalFields = extra
	return nil
}

func decodeWithUnknownFields(
	data []byte,
	target any,
	known map[string]struct{},
	required ...string,
) (map[string]any, error) {
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawFields); err != nil {
		return nil, err
	}
	for _, field := range required {
		raw, exists := rawFields[field]
		if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, fmt.Errorf("required field %q is missing", field)
		}
	}
	if err := json.Unmarshal(data, target); err != nil {
		return nil, err
	}

	fields := make(map[string]any, len(rawFields))
	for field, raw := range rawFields {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("decode field %q: %w", field, err)
		}
		fields[field] = value
	}
	for field := range known {
		delete(fields, field)
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return fields, nil
}
