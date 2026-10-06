package mapping

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/reference"
)

type Mapper struct {
	volcanoes map[string]reference.Volcano
	now       func() time.Time
}

func NewMapper(volcanoes map[string]reference.Volcano) (*Mapper, error) {
	if len(volcanoes) == 0 {
		return nil, errors.New("volcano reference table is empty")
	}
	copied := make(map[string]reference.Volcano, len(volcanoes))
	for id, volcano := range volcanoes {
		copied[id] = volcano
	}
	return &Mapper{volcanoes: copied, now: time.Now}, nil
}

func (mapper *Mapper) SetClock(now func() time.Time) {
	if now != nil {
		mapper.now = now
	}
}

func (mapper *Mapper) MapSeismicEvent(
	event SeismicEvent,
	warning *TsunamiWarning,
) (domain.HazardEvent, error) {
	if err := validateSeismicEvent(event); err != nil {
		return domain.HazardEvent{}, err
	}

	severity := severityFromMagnitude(event.Magnitude)
	attributes := copyAttributes(event.AdditionalFields)
	attributes["magnitude"] = event.Magnitude
	attributes["depth_km"] = event.DepthKM
	attributes["potential_tsunami"] = event.PotentialTsunami
	if warning != nil {
		if warning.RelatedEventID != event.EventID {
			return domain.HazardEvent{}, fmt.Errorf(
				"tsunami warning %q references %q instead of %q",
				warning.WarningID,
				warning.RelatedEventID,
				event.EventID,
			)
		}
		var err error
		severity, err = severityFromThreatLevel(warning.ThreatLevel)
		if err != nil {
			return domain.HazardEvent{}, err
		}
		warningAttributes := copyAttributes(warning.AdditionalFields)
		warningAttributes["warning_id"] = warning.WarningID
		warningAttributes["threat_level"] = warning.ThreatLevel
		warningAttributes["affected_zones"] = append([]string(nil), warning.AffectedZones...)
		warningAttributes["estimated_arrival"] = warning.EstimatedArrival.UTC()
		warningAttributes["issued_at"] = warning.IssuedAt.UTC()
		attributes["tsunami_warning"] = warningAttributes
	}

	mapped := domain.HazardEvent{
		HazardID:    domain.NewHazardID(domain.SourceBMKG, event.EventID),
		Source:      domain.SourceBMKG,
		SourceRefID: event.EventID,
		HazardType:  domain.HazardTypeSeismic,
		Severity:    severity,
		AreaName:    event.RegionName,
		Latitude:    event.EpicenterLat,
		Longitude:   event.EpicenterLon,
		OccurredAt:  event.OccurredAt.UTC(),
		IngestedAt:  mapper.now().UTC(),
		Attributes:  attributes,
	}
	return mapped, mapped.Validate()
}

func (mapper *Mapper) MapVolcanicReport(report VolcanicReport) (domain.HazardEvent, error) {
	if err := validateVolcanicReport(report); err != nil {
		return domain.HazardEvent{}, err
	}
	volcano, ok := mapper.volcanoes[report.VolcanoID]
	if !ok {
		return domain.HazardEvent{}, fmt.Errorf("unknown volcano_id %q", report.VolcanoID)
	}
	severity, err := severityFromAlertLevel(report.AlertLevel)
	if err != nil {
		return domain.HazardEvent{}, err
	}

	attributes := copyAttributes(report.AdditionalFields)
	attributes["eruption_count_24h"] = report.EruptionCount24H
	attributes["ash_column_height_m"] = report.AshColumnHeightM
	if report.ConfidenceLevel != nil {
		attributes["confidence_level"] = *report.ConfidenceLevel
	}

	mapped := domain.HazardEvent{
		HazardID:    domain.NewHazardID(domain.SourcePVMBG, report.ReportID),
		Source:      domain.SourcePVMBG,
		SourceRefID: report.ReportID,
		HazardType:  domain.HazardTypeVolcanic,
		Severity:    severity,
		AreaName:    volcano.Name,
		Latitude:    volcano.Latitude,
		Longitude:   volcano.Longitude,
		OccurredAt:  report.ReportedAt.UTC(),
		IngestedAt:  mapper.now().UTC(),
		Attributes:  attributes,
	}
	return mapped, mapped.Validate()
}

func copyAttributes(source map[string]any) map[string]any {
	result := make(map[string]any, len(source)+4)
	for key, value := range source {
		result[key] = value
	}
	return result
}

func severityFromMagnitude(magnitude float64) domain.Severity {
	switch {
	case magnitude < 5:
		return domain.SeverityNormal
	case magnitude < 6.5:
		return domain.SeverityWaspada
	default:
		return domain.SeveritySiaga
	}
}

func severityFromThreatLevel(level string) (domain.Severity, error) {
	switch level {
	case "Waspada":
		return domain.SeverityWaspada, nil
	case "Siaga":
		return domain.SeveritySiaga, nil
	case "Awas":
		return domain.SeverityAwas, nil
	default:
		return "", fmt.Errorf("unknown tsunami threat_level %q", level)
	}
}

func severityFromAlertLevel(level string) (domain.Severity, error) {
	switch level {
	case "Normal":
		return domain.SeverityNormal, nil
	case "Waspada":
		return domain.SeverityWaspada, nil
	case "Siaga":
		return domain.SeveritySiaga, nil
	case "Awas":
		return domain.SeverityAwas, nil
	default:
		return "", fmt.Errorf("unknown volcanic alert_level %q", level)
	}
}

func validateSeismicEvent(event SeismicEvent) error {
	switch {
	case event.EventID == "":
		return errors.New("seismic event_id is required")
	case event.RegionName == "":
		return errors.New("seismic region_name is required")
	case event.OccurredAt.IsZero():
		return errors.New("seismic occurred_at is required")
	case math.IsNaN(event.Magnitude) || math.IsInf(event.Magnitude, 0) || event.Magnitude < 0:
		return errors.New("seismic magnitude must be a finite non-negative number")
	case math.IsNaN(event.DepthKM) || math.IsInf(event.DepthKM, 0) || event.DepthKM < 0:
		return errors.New("seismic depth_km must be a finite non-negative number")
	}
	return nil
}

func validateVolcanicReport(report VolcanicReport) error {
	switch {
	case report.ReportID == "":
		return errors.New("volcanic report_id is required")
	case report.VolcanoID == "":
		return errors.New("volcanic volcano_id is required")
	case report.ReportedAt.IsZero():
		return errors.New("volcanic reported_at is required")
	case report.EruptionCount24H < 0:
		return errors.New("volcanic eruption_count_24h must be non-negative")
	case math.IsNaN(report.AshColumnHeightM) || math.IsInf(report.AshColumnHeightM, 0) || report.AshColumnHeightM < 0:
		return errors.New("volcanic ash_column_height_m must be a finite non-negative number")
	case report.ConfidenceLevel != nil && (*report.ConfidenceLevel < 0 || *report.ConfidenceLevel > 1 || math.IsNaN(*report.ConfidenceLevel)):
		return errors.New("volcanic confidence_level must be between 0 and 1")
	}
	return nil
}
