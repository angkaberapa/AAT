package wildfire

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/reference"
)

const (
	SourceSatelit   = "SATELIT"
	HazardWildfire  = "WILDFIRE"
	SeverityWaspada = "WASPADA"
	SeveritySiaga   = "SIAGA"
	SeverityAwas    = "AWAS"
)

func HazardID(sourceRefID string) string {
	sum := sha256.Sum256([]byte(SourceSatelit + ":" + sourceRefID))
	return "HZ-" + hex.EncodeToString(sum[:16])
}

// BuildEvent maps one fire to the canonical WILDFIRE event. Severity uses the
// exact thresholds from the M2 definition: 20 detections or 100 MW for AWAS,
// 6 detections or 30 MW for SIAGA.
func BuildEvent(fire Fire, province reference.Province) domain.HazardEvent {
	return domain.HazardEvent{
		HazardID:    HazardID(fire.ID),
		Source:      SourceSatelit,
		SourceRefID: fire.ID,
		HazardType:  HazardWildfire,
		Severity:    severity(fire.DetectionCount, fire.TotalFRP),
		AreaName:    province.Name,
		Latitude:    fire.Latitude,
		Longitude:   fire.Longitude,
		OccurredAt:  fire.FirstObservedAt.UTC(),
		Attributes: map[string]any{
			"detection_count":  fire.DetectionCount,
			"satellites":       fire.Satellites,
			"max_confidence":   fire.MaxConfidence,
			"total_frp_mw":     fire.TotalFRP,
			"detection_keys":   fire.DetectionKeys,
			"last_detected_at": fire.LastObservedAt.UTC().Format("2006-01-02T15:04:05Z"),
		},
	}
}

func severity(detectionCount int, totalFRP float64) string {
	switch {
	case detectionCount >= 20 || totalFRP >= 100:
		return SeverityAwas
	case detectionCount >= 6 || totalFRP >= 30:
		return SeveritySiaga
	default:
		return SeverityWaspada
	}
}
