package advisory

import (
	"context"
	"log/slog"

	"github.com/angkaberapa/AAT/services/bnpb-ai-advisory/internal/consumer"
	"github.com/angkaberapa/AAT/services/bnpb-ai-advisory/internal/detector"
)

// Processor adapts the Kafka consumer to the detector and the in-memory store.
// Handle never writes to the canonical store and never blocks the producer.
type Processor struct {
	detector *detector.Detector
	store    *Store
	recent   *RecentEvents
	logger   *slog.Logger
}

func NewProcessor(d *detector.Detector, store *Store, recent *RecentEvents, logger *slog.Logger) *Processor {
	return &Processor{detector: d, store: store, recent: recent, logger: logger}
}

func (p *Processor) Handle(ctx context.Context, msg consumer.Message) error {
	p.recent.Add(Event{
		Source:     msg.Hazard.Source,
		HazardType: msg.Hazard.HazardType,
		Severity:   msg.Hazard.Severity,
		AreaName:   msg.Hazard.AreaName,
		OccurredAt: msg.Hazard.OccurredAt,
	})

	advisory, ok := p.detector.Observe(detector.Hazard{
		HazardID:   msg.Hazard.HazardID,
		Source:     msg.Hazard.Source,
		HazardType: msg.Hazard.HazardType,
		Severity:   msg.Hazard.Severity,
		AreaName:   msg.Hazard.AreaName,
		OccurredAt: msg.Hazard.OccurredAt,
	})
	metricEvaluated.WithLabelValues("ok").Inc()
	if !ok {
		return nil
	}

	p.store.Add(advisory)
	metricGenerated.WithLabelValues(advisory.Level).Inc()
	metricScore.Observe(advisory.Score)
	p.logger.WarnContext(ctx, "anomaly advisory",
		"advisory_id", advisory.ID,
		"source", advisory.Source,
		"hazard_type", advisory.HazardType,
		"area", advisory.AreaName,
		"level", advisory.Level,
		"score", advisory.Score,
		"reason", advisory.Reason,
	)
	return nil
}

func (p *Processor) Flush(context.Context) error { return nil }
