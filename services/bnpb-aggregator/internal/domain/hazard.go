package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

type Source string

const (
	SourceBMKG  Source = "BMKG"
	SourcePVMBG Source = "PVMBG"
	SourceSatelit Source = "SATELIT"       
	SourceSensorPantai Source = "SENSOR_PANTAI"
)

var Sources = []Source{SourceBMKG, SourcePVMBG, SourceSatelit, SourceSensorPantai}

func (s Source) Valid() bool {
	for _, known := range Sources {
		if s == known {
			return true
		}
	}
	return false
}

type HazardType string

const (
	HazardTypeSeismic  HazardType = "SEISMIC"
	HazardTypeVolcanic HazardType = "VOLCANIC"
	HazardTypeWildfire HazardType = "WILDFIRE"
	HazardTypeCoastal  HazardType = "COASTAL"
)

var sourceOf = map[HazardType]Source{
	HazardTypeSeismic:  SourceBMKG,
	HazardTypeVolcanic: SourcePVMBG,
	HazardTypeWildfire: SourceSatelit,
	HazardTypeCoastal:  SourceSensorPantai,
}

var HazardTypes = []HazardType{HazardTypeSeismic, HazardTypeVolcanic, HazardTypeWildfire, HazardTypeCoastal}

func (h HazardType) Valid() bool {
	_, ok := sourceOf[h]
	return ok
}

func (h HazardType) Source() Source {
	return sourceOf[h]
}

type Severity string

const (
	SeverityNormal  Severity = "NORMAL"
	SeverityWaspada Severity = "WASPADA"
	SeveritySiaga   Severity = "SIAGA"
	SeverityAwas    Severity = "AWAS"
)

type HazardEvent struct {
	HazardID    string         `json:"hazard_id" bson:"hazard_id"`
	Source      Source         `json:"source" bson:"source"`
	SourceRefID string         `json:"source_ref_id" bson:"source_ref_id"`
	HazardType  HazardType     `json:"hazard_type" bson:"hazard_type"`
	Severity    Severity       `json:"severity" bson:"severity"`
	AreaName    string         `json:"area_name" bson:"area_name"`
	Latitude    float64        `json:"latitude" bson:"latitude"`
	Longitude   float64        `json:"longitude" bson:"longitude"`
	OccurredAt  time.Time      `json:"occurred_at" bson:"occurred_at"`
	IngestedAt  time.Time      `json:"ingested_at" bson:"ingested_at"`
	Attributes  map[string]any `json:"attributes" bson:"attributes"`
}

func NewHazardID(source Source, sourceRefID string) string {
	sum := sha256.Sum256([]byte(string(source) + ":" + sourceRefID))
	return "HZ-" + hex.EncodeToString(sum[:16])
}

func (event HazardEvent) Validate() error {
	var errs []error
	if event.HazardID == "" {
		errs = append(errs, errors.New("hazard_id is required"))
	}
	if !event.Source.Valid() {
		errs = append(errs, fmt.Errorf("unknown source %q", event.Source))
	}
	if event.SourceRefID == "" {
		errs = append(errs, errors.New("source_ref_id is required"))
	}
	if !event.HazardType.Valid() {
		errs = append(errs, fmt.Errorf("unknown hazard_type %q", event.HazardType))
	}
	if !validSeverity(event.Severity) {
		errs = append(errs, fmt.Errorf("unknown severity %q", event.Severity))
	}
	if event.AreaName == "" {
		errs = append(errs, errors.New("area_name is required"))
	}
	if event.OccurredAt.IsZero() {
		errs = append(errs, errors.New("occurred_at is required"))
	}
	if event.IngestedAt.IsZero() {
		errs = append(errs, errors.New("ingested_at is required"))
	}
	if event.Attributes == nil {
		errs = append(errs, errors.New("attributes is required"))
	}
	return errors.Join(errs...)
}

func validSeverity(severity Severity) bool {
	switch severity {
	case SeverityNormal, SeverityWaspada, SeveritySiaga, SeverityAwas:
		return true
	default:
		return false
	}
}
