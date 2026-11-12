package domain

import "time"

// Observation is one reading from the Sensor Pantai feed. Numeric fields are
// pointers so a missing value is distinguishable from a legitimate zero.
type Observation struct {
	ObservationID    string
	StationID        string
	ObservedAt       time.Time
	ReceivedAt       time.Time
	GeneratedAt      time.Time
	Critical         bool
	Quality          string
	SeaLevelAnomalyM *float64
	WaveHeightM      *float64
	RawJSON          string
}
