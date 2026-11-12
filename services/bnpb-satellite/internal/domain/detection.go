package domain

import "time"

// Detection is one raw satellite hotspot row. Latitude and longitude are kept
// as strings because the detection key must preserve the feed's precision.
type Detection struct {
	Key        string
	Satellite  string
	AcqDate    string
	AcqTime    string
	Latitude   string
	Longitude  string
	Confidence string
	FRP        *float64
	ObservedAt time.Time
	Active     bool
	RawJSON    string
}

// Fire is the current canonical projection of one connected detection set.
type Fire struct {
	FireID            string
	EventJSON         []byte
	Version           int
	FirstDetectionKey string
	LastDetectionAt   time.Time
	DetectionCount    int
	TotalFRP          float64
	Active            bool
}
