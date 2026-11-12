package domain

import "time"

// HazardEvent is the canonical JSON shape sent to the Aggregator. The producer
// does not send ingested_at; the Aggregator fills it with server time.
type HazardEvent struct {
	HazardID    string         `json:"hazard_id"`
	Source      string         `json:"source"`
	SourceRefID string         `json:"source_ref_id"`
	HazardType  string         `json:"hazard_type"`
	Severity    string         `json:"severity"`
	AreaName    string         `json:"area_name"`
	Latitude    float64        `json:"latitude"`
	Longitude   float64        `json:"longitude"`
	OccurredAt  time.Time      `json:"occurred_at"`
	Attributes  map[string]any `json:"attributes"`
}
