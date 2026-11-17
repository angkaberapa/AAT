package processor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/feed"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/ingest"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/metrics"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/reference"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/sse"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/store"
	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/wildfire"
)

const (
	sourceName = "SATELIT"
	producer   = "bnpb-satellite"
)

// Streamer is the Feed Satelit client. The processor owns reconnects so the
// stream always resumes from the committed offset.
type Streamer interface {
	Stream(ctx context.Context, from string, handler func(sse.Event) error) error
}

type Config struct {
	AllowedLateness time.Duration
	ReconnectMin    time.Duration
	ReconnectMax    time.Duration
	OutboxBatch     int
	FlushInterval   time.Duration
}

type Processor struct {
	logger  *slog.Logger
	stream  Streamer
	store   *store.Store
	ingest  ingest.Client
	metrics *metrics.Metrics
	config  Config
	now     func() time.Time

	latest       time.Time
	pending      []feed.Message
	lastFlush    time.Time
	batchSize    int
	batchEvery   time.Duration
}

func New(logger *slog.Logger, stream Streamer, db *store.Store, ingestClient ingest.Client, config Config, recorder ...*metrics.Metrics) (*Processor, error) {
	if logger == nil || stream == nil || db == nil || ingestClient == nil {
		return nil, fmt.Errorf("processor dependencies are required")
	}
	if config.AllowedLateness <= 0 {
		config.AllowedLateness = 360 * time.Minute
	}
	if config.ReconnectMin <= 0 {
		config.ReconnectMin = time.Second
	}
	if config.ReconnectMax <= 0 {
		config.ReconnectMax = 30 * time.Second
	}
	if config.OutboxBatch <= 0 {
		config.OutboxBatch = 100
	}
	if config.FlushInterval <= 0 {
		config.FlushInterval = time.Second
	}
	rec := (*metrics.Metrics)(nil)
	if len(recorder) > 0 {
		rec = recorder[0]
	}
	if rec == nil {
		rec = metrics.New()
	}
	return &Processor{
		logger:     logger,
		stream:     stream,
		store:      db,
		ingest:     ingestClient,
		metrics:    rec,
		config:     config,
		now:        time.Now,
		batchSize:  200,
		batchEvery: 250 * time.Millisecond,
	}, nil
}

// Run consumes the stream until ctx is cancelled. A disconnect is not fatal:
// the next connection starts from the last committed offset.
func (p *Processor) Run(ctx context.Context) {
	if latest, err := p.store.LatestObservedAt(ctx); err != nil {
		p.logger.WarnContext(ctx, "read latest detection failed", "error", err.Error())
	} else {
		p.latest = latest
	}

	go p.flushLoop(ctx)

	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		if attempt > 0 {
			p.metrics.IncReconnect()
		}
		err := p.consume(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			p.logger.WarnContext(ctx, "satellite stream ended", "error", err.Error(), "attempt", attempt+1)
		}
		delay := reconnectDelay(attempt, p.config.ReconnectMin, p.config.ReconnectMax)
		attempt++
		if !sleep(ctx, delay) {
			return
		}
	}
}

func (p *Processor) consume(parent context.Context) error {
	ctx := core.WithCorrelationID(parent, core.NewCorrelationID())
	offset, err := p.store.Offset(ctx)
	if err != nil {
		return err
	}
	p.logger.InfoContext(ctx, "connecting to feed", "from", offset)
	err = p.stream.Stream(ctx, offset, func(event sse.Event) error {
		return p.handle(ctx, event)
	})
	if flushErr := p.flushBatch(ctx); flushErr != nil && err == nil {
		err = flushErr
	}
	return err
}

func (p *Processor) handle(ctx context.Context, event sse.Event) error {
	message, err := feed.Parse(event.Type, event.ID, event.Data)
	if err != nil {
		if errors.Is(err, feed.ErrLowConfidence) {
			p.metrics.IncEvent("detection", "skipped")
		} else {
			p.metrics.IncEvent(event.Type, "invalid")
			p.logger.WarnContext(ctx, "invalid feed event",
				"type", event.Type, "offset", message.Offset, "error", err.Error())
		}
	}
	p.pending = append(p.pending, message)
	if len(p.pending) >= p.batchSize || p.now().Sub(p.lastFlush) >= p.batchEvery {
		return p.flushBatch(ctx)
	}
	return nil
}

// flushBatch applies every buffered frame in one transaction. Clustering runs
// once per batch, so a 60x feed no longer pays a full recompute per event.
func (p *Processor) flushBatch(ctx context.Context) error {
	if len(p.pending) == 0 {
		return nil
	}
	started := p.now()
	batch := p.pending
	p.pending = nil
	p.lastFlush = started

	active, err := p.store.ActiveDetections(ctx)
	if err != nil {
		return err
	}
	existing, err := p.store.ActiveFireIDs(ctx)
	if err != nil {
		return err
	}
	current := make(map[string]domain.Detection, len(active))
	for _, detection := range active {
		current[detection.Key] = detection
	}
	mutations := make(map[string]*domain.Detection)
	offset := ""
	late := 0
	okTypes := map[string]int{}

	for _, message := range batch {
		if message.Offset != "" {
			offset = message.Offset
		}
		switch message.Kind {
		case feed.KindHeartbeat:
			okTypes["heartbeat"]++
		case feed.KindDetection, feed.KindUpdate:
			detection := message.Detection
			if message.Kind == feed.KindUpdate && detection.Key == "" {
				found, ok := current[message.Key]
				if !ok {
					stored, err := p.store.Detection(ctx, message.Key)
					if err != nil {
						return err
					}
					if stored == nil {
						okTypes["update"]++
						continue
					}
					found = *stored
				}
				if message.Confidence != nil && feed.IsLowConfidence(*message.Confidence) {
					delete(current, message.Key)
					mutations[message.Key] = nil
					okTypes["update"]++
					continue
				}
				if message.Confidence != nil {
					found.Confidence = *message.Confidence
				}
				if message.FRP != nil {
					found.FRP = message.FRP
				}
				found.Active = true
				detection = found
			}
			if detection.Key == "" {
				continue
			}
			current[detection.Key] = detection
			value := detection
			mutations[detection.Key] = &value
			okTypes[message.Type]++
			if p.isLate(detection.ObservedAt) {
				late++
			}
			p.advanceLatest(detection.ObservedAt)
		case feed.KindRetract:
			if message.Key != "" {
				delete(current, message.Key)
				mutations[message.Key] = nil
			}
			okTypes["retract"]++
		}
	}
	if offset == "" {
		if len(mutations) > 0 {
			return fmt.Errorf("batch has changes but no offset")
		}
		return nil
	}

	activeList := make([]domain.Detection, 0, len(current))
	for _, detection := range current {
		activeList = append(activeList, detection)
	}
	result := wildfire.Cluster(activeList)
	fires, deletes := wildfire.ReconcileResult(result, existing)

	if err := p.store.WithTx(ctx, func(tx *sql.Tx) error {
		for key, detection := range mutations {
			if detection == nil {
				if err := store.DeactivateDetectionTx(ctx, tx, key); err != nil {
					return err
				}
				continue
			}
			if _, err := store.UpsertDetectionTx(ctx, tx, *detection); err != nil {
				return err
			}
		}
		if err := p.writeProjection(ctx, tx, fires, deletes, offset); err != nil {
			return err
		}
		return store.SetOffsetTx(ctx, tx, offset)
	}); err != nil {
		return err
	}

	for eventType, count := range okTypes {
		for i := 0; i < count; i++ {
			p.metrics.IncEvent(eventType, "ok")
		}
	}
	if late > 0 {
		p.metrics.IncLate()
	}
	p.metrics.SetProjection(len(result.Singles), len(result.Fires))
	p.metrics.ObserveProcessing(p.now().Sub(started) / time.Duration(len(batch)))
	p.logger.InfoContext(ctx, "batch applied",
		"events", len(batch), "fires", len(result.Fires),
		"deletes", len(deletes), "offset", offset)
	return nil
}

func (p *Processor) applyDetection(ctx context.Context, message feed.Message) error {
	if message.Offset == "" {
		return fmt.Errorf("detection has no offset")
	}
	active, err := p.store.ActiveDetections(ctx)
	if err != nil {
		return err
	}
	existing, err := p.store.ActiveFireIDs(ctx)
	if err != nil {
		return err
	}

	late := p.isLate(message.Detection.ObservedAt)
	active = upsertActive(active, message.Detection)
	current := wildfire.Cluster(active)
	fires, deletes := wildfire.ReconcileResult(current, existing)

	if err := p.store.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := store.UpsertDetectionTx(ctx, tx, message.Detection); err != nil {
			return err
		}
		if err := p.writeProjection(ctx, tx, fires, deletes, message.Offset); err != nil {
			return err
		}
		return store.SetOffsetTx(ctx, tx, message.Offset)
	}); err != nil {
		return err
	}

	if late {
		p.metrics.IncLate()
		p.logger.WarnContext(ctx, "late detection applied",
			"detection_key", message.Detection.Key,
			"observed_at", message.Detection.ObservedAt.Format(time.RFC3339),
			"offset", message.Offset)
	}
	p.advanceLatest(message.Detection.ObservedAt)
	p.metrics.SetProjection(len(current.Singles), len(current.Fires))
	p.logger.DebugContext(ctx, "detection applied",
		"detection_key", message.Detection.Key, "offset", message.Offset,
		"fires", len(current.Fires), "singles", len(current.Singles))
	return nil
}

func (p *Processor) applyUpdate(ctx context.Context, message feed.Message) error {
	if message.Key == "" {
		return fmt.Errorf("update has no key")
	}
	existing, err := p.store.Detection(ctx, message.Key)
	if err != nil {
		return err
	}
	if existing == nil {
		return p.commitOffset(ctx, message.Offset)
	}
	if message.Confidence != nil && feed.IsLowConfidence(*message.Confidence) {
		return p.applyRetract(ctx, feed.Message{Kind: feed.KindRetract, Offset: message.Offset, Key: message.Key})
	}
	if message.Confidence != nil {
		existing.Confidence = *message.Confidence
	}
	if message.FRP != nil {
		existing.FRP = message.FRP
	}
	existing.Active = true
	message.Detection = *existing
	message.Kind = feed.KindDetection
	return p.applyDetection(ctx, message)
}

func (p *Processor) applyRetract(ctx context.Context, message feed.Message) error {
	if message.Offset == "" {
		return fmt.Errorf("retract has no offset")
	}
	active, err := p.store.ActiveDetections(ctx)
	if err != nil {
		return err
	}
	existing, err := p.store.ActiveFireIDs(ctx)
	if err != nil {
		return err
	}

	active = removeActive(active, message.Key)
	current := wildfire.Cluster(active)
	fires, deletes := wildfire.ReconcileResult(current, existing)

	if err := p.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := store.DeactivateDetectionTx(ctx, tx, message.Key); err != nil {
			return err
		}
		if err := p.writeProjection(ctx, tx, fires, deletes, message.Offset); err != nil {
			return err
		}
		return store.SetOffsetTx(ctx, tx, message.Offset)
	}); err != nil {
		return err
	}

	p.metrics.SetProjection(len(current.Singles), len(current.Fires))
	p.logger.InfoContext(ctx, "retract applied",
		"detection_key", message.Key, "offset", message.Offset,
		"fires", len(current.Fires), "deletes", len(deletes))
	return nil
}

func (p *Processor) writeProjection(ctx context.Context, tx *sql.Tx, fires []wildfire.Fire, deletes []string, offset string) error {
	for _, fire := range fires {
		province, err := reference.Nearest(fire.Latitude, fire.Longitude)
		if err != nil {
			return err
		}
		event := wildfire.BuildEvent(fire, province)
		raw, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("marshal hazard event: %w", err)
		}
		version, err := store.UpsertFireTx(ctx, tx, domain.Fire{
			FireID:            fire.ID,
			EventJSON:         raw,
			FirstDetectionKey: fire.DetectionKeys[0],
			LastDetectionAt:   fire.LastObservedAt,
			DetectionCount:    fire.DetectionCount,
			TotalFRP:          fire.TotalFRP,
			Active:            true,
		})
		if err != nil {
			return err
		}
		command := ingest.UpsertHazardCommand{
			Operation:       ingest.OperationUpsert,
			Producer:        producer,
			ProducerEventID: fmt.Sprintf("satellite-fire-%s-v%d", fire.ID, version),
			SourcePosition:  ingest.SourcePosition{Type: "sse_offset", Value: offset},
			Event:           toIngestEvent(event),
		}
		payload, err := json.Marshal(command)
		if err != nil {
			return fmt.Errorf("marshal upsert command: %w", err)
		}
		if _, err := store.EnqueueOutboxTx(ctx, tx, store.OutboxItem{
			ProducerEventID: command.ProducerEventID,
			Operation:       command.Operation,
			SourcePosition:  offset,
			Payload:         payload,
		}); err != nil {
			return err
		}
		p.metrics.IncFireChange("upsert")
	}

	for _, fireID := range deletes {
		version, err := store.DeleteFireTx(ctx, tx, fireID)
		if err != nil {
			return err
		}
		command := ingest.DeleteHazardCommand{
			Operation:       ingest.OperationDelete,
			Producer:        producer,
			ProducerEventID: fmt.Sprintf("satellite-delete-%s-v%d", fireID, version),
			SourcePosition:  ingest.SourcePosition{Type: "sse_offset", Value: offset},
			HazardID:        wildfire.HazardID(fireID),
			Source:          sourceName,
			SourceRefID:     fireID,
		}
		payload, err := json.Marshal(command)
		if err != nil {
			return fmt.Errorf("marshal delete command: %w", err)
		}
		if _, err := store.EnqueueOutboxTx(ctx, tx, store.OutboxItem{
			ProducerEventID: command.ProducerEventID,
			Operation:       command.Operation,
			SourcePosition:  offset,
			Payload:         payload,
		}); err != nil {
			return err
		}
		p.metrics.IncFireChange("delete")
	}
	return nil
}

func (p *Processor) flushLoop(ctx context.Context) {
	p.flushOutbox(ctx)
	ticker := time.NewTicker(p.config.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.flushOutbox(ctx)
		}
	}
}

func (p *Processor) flushOutbox(ctx context.Context) {
	items, err := p.store.PendingOutbox(ctx, p.config.OutboxBatch)
	if err != nil {
		p.logger.ErrorContext(ctx, "list outbox failed", "error", err.Error())
		return
	}
	for _, item := range items {
		err := p.sendOutboxItem(ctx, item)
		if err == nil {
			if markErr := p.store.MarkOutboxSent(ctx, item.ID); markErr != nil {
				p.logger.ErrorContext(ctx, "mark outbox sent failed", "error", markErr.Error())
			}
			continue
		}
		if terminalDataError(err) {
			if parkErr := p.store.ParkOutbox(ctx, item.ID, err.Error()); parkErr != nil {
				p.logger.ErrorContext(ctx, "park outbox failed", "error", parkErr.Error())
				continue
			}
			p.logger.ErrorContext(ctx, "outbox item parked",
				"producer_event_id", item.ProducerEventID, "error", err.Error())
			continue
		}
		if markErr := p.store.MarkOutboxFailed(ctx, item.ID, err.Error(), backoff(item.Attempts)); markErr != nil {
			p.logger.ErrorContext(ctx, "mark outbox failed", "error", markErr.Error())
		}
		p.logger.WarnContext(ctx, "outbox item deferred",
			"producer_event_id", item.ProducerEventID, "attempt", item.Attempts+1,
			"error", err.Error())
	}
	if stats, err := p.store.Stats(ctx); err == nil {
		p.metrics.SetOutboxPending(stats.OutboxPending)
	}
}

func (p *Processor) sendOutboxItem(ctx context.Context, item store.OutboxItem) error {
	switch item.Operation {
	case ingest.OperationUpsert:
		var command ingest.UpsertHazardCommand
		if err := json.Unmarshal(item.Payload, &command); err != nil {
			return fmt.Errorf("decode upsert command: %w", err)
		}
		_, err := p.ingest.UpsertHazard(ctx, command)
		return err
	case ingest.OperationDelete:
		var command ingest.DeleteHazardCommand
		if err := json.Unmarshal(item.Payload, &command); err != nil {
			return fmt.Errorf("decode delete command: %w", err)
		}
		_, err := p.ingest.DeleteHazard(ctx, command)
		return err
	default:
		return fmt.Errorf("unknown outbox operation %q", item.Operation)
	}
}

func (p *Processor) commitOffset(ctx context.Context, offset string) error {
	if offset == "" {
		return nil
	}
	return p.store.WithTx(ctx, func(tx *sql.Tx) error {
		return store.SetOffsetTx(ctx, tx, offset)
	})
}

func (p *Processor) isLate(observedAt time.Time) bool {
	if p.latest.IsZero() {
		return false
	}
	return observedAt.Before(p.latest.Add(-p.config.AllowedLateness))
}

func (p *Processor) advanceLatest(observedAt time.Time) {
	if observedAt.After(p.latest) {
		p.latest = observedAt
	}
}

func upsertActive(active []domain.Detection, detection domain.Detection) []domain.Detection {
	for i, current := range active {
		if current.Key == detection.Key {
			active[i] = detection
			return active
		}
	}
	return append(active, detection)
}

func removeActive(active []domain.Detection, key string) []domain.Detection {
	kept := active[:0]
	for _, detection := range active {
		if detection.Key != key {
			kept = append(kept, detection)
		}
	}
	return kept
}

func toIngestEvent(event domain.HazardEvent) ingest.HazardEvent {
	return ingest.HazardEvent{
		HazardID:    event.HazardID,
		Source:      event.Source,
		SourceRefID: event.SourceRefID,
		HazardType:  event.HazardType,
		Severity:    event.Severity,
		AreaName:    event.AreaName,
		Latitude:    event.Latitude,
		Longitude:   event.Longitude,
		OccurredAt:  event.OccurredAt,
		Attributes:  event.Attributes,
	}
}

func terminalDataError(err error) bool {
	var apiErr *ingest.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case 400, 409, 422:
		return true
	default:
		return false
	}
}

func reconnectDelay(attempt int, min, max time.Duration) time.Duration {
	delay := min
	for i := 0; i < attempt && delay < max; i++ {
		delay *= 2
	}
	if delay > max {
		delay = max
	}
	return delay
}

func backoff(attempts int) time.Time {
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 5 {
		attempts = 5
	}
	base := time.Duration(1<<attempts) * time.Second
	return time.Now().Add(base).UTC()
}

func sleep(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
