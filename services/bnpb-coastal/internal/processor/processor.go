package processor

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/coastal"
	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/ingest"
	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/metrics"
	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/sensor"
	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/store"
)

type SensorClient interface {
	FetchPage(ctx context.Context, cursor string, limit int) (sensor.Page, error)
}

type Config struct {
	PollInterval time.Duration
	PageLimit    int
	MaxStaleness time.Duration
	CircuitState func() int
}

type Processor struct {
	logger  *slog.Logger
	sensor  SensorClient
	store   *store.Store
	ingest  ingest.Client
	metrics *metrics.Metrics
	config  Config
	now     func() time.Time
}

func New(logger *slog.Logger, sensorClient SensorClient, store *store.Store, ingestClient ingest.Client, config Config, recorder ...*metrics.Metrics) (*Processor, error) {
	if logger == nil || sensorClient == nil || store == nil || ingestClient == nil {
		return nil, fmt.Errorf("processor dependencies are required")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 2 * time.Second
	}
	if config.PageLimit < 1 || config.PageLimit > 1000 {
		config.PageLimit = 500
	}
	rec := (*metrics.Metrics)(nil)
	if len(recorder) > 0 {
		rec = recorder[0]
	}
	if rec == nil {
		rec = metrics.New()
	}
	return &Processor{
		logger:  logger,
		sensor:  sensorClient,
		store:   store,
		ingest:  ingestClient,
		metrics: rec,
		config:  config,
		now:     time.Now,
	}, nil
}

func (p *Processor) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		started := time.Now()
		p.pollOnce(ctx)
		wait := p.config.PollInterval - time.Since(started)
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (p *Processor) pollOnce(parent context.Context) {
	ctx := core.WithCorrelationID(parent, core.NewCorrelationID())
	logger := p.logger.With("cycle", core.CorrelationID(ctx))
	started := time.Now()
	p.setCircuitState()

	cursor, err := p.store.Cursor(ctx)
	if err != nil {
		p.metrics.ObserveRequest("error", time.Since(started))
		logger.ErrorContext(ctx, "read cursor failed", "error", err.Error())
		return
	}

	page, err := p.sensor.FetchPage(ctx, cursor, p.config.PageLimit)
	if err != nil {
		p.metrics.ObserveRequest("error", time.Since(started))
		logger.WarnContext(ctx, "sensor fetch failed", "error", err.Error(), "cursor", cursor)
		p.reportStatus(ctx, "degraded", cursor, err)
		p.setCircuitState()
		return
	}
	p.metrics.ObserveRequest("ok", time.Since(started))

	receivedAt := p.now().UTC()
	nextCursor := page.NextCursor
	if nextCursor == "" {
		nextCursor = cursor
	}

	valid := 0
	quarantined := 0
	if err := p.store.WithTx(ctx, func(tx *sql.Tx) error {
		for _, raw := range page.Items {
			observation, validationErr := coastal.Validate(raw, page.GeneratedAt, receivedAt, p.config.MaxStaleness)
			if validationErr != nil {
				quarantined++
				p.metrics.IncInvalid(validationErr.Reason)
				if err := p.enqueueQuarantine(ctx, tx, raw, nextCursor, receivedAt, validationErr); err != nil {
					return err
				}
				continue
			}
			if observation.Critical {
				latency := receivedAt.Sub(observation.ObservedAt).Seconds()
				if latency < 0 {
					latency = 0
				}
				p.metrics.ObserveCriticalLatency(latency)
			}

			station, _ := coastal.LookupStation(observation.StationID)
			event := coastal.MapObservation(observation, station)
			command := ingest.UpsertHazardCommand{
				Operation:       ingest.OperationUpsert,
				Producer:        "bnpb-coastal",
				ProducerEventID: "coastal-observation-" + observation.ObservationID,
				SourcePosition:  ingest.SourcePosition{Type: "coastal_cursor", Value: nextCursor},
				Event:           event,
			}
			payload, err := json.Marshal(command)
			if err != nil {
				return fmt.Errorf("marshal upsert command: %w", err)
			}
			if _, err := store.InsertObservationTx(ctx, tx, observation); err != nil {
				return err
			}
			if _, err := store.EnqueueOutboxTx(ctx, tx, store.OutboxItem{
				ProducerEventID: command.ProducerEventID,
				Operation:       command.Operation,
				SourcePosition:  nextCursor,
				Payload:         payload,
			}); err != nil {
				return err
			}
			if err := store.UpdateCacheTx(ctx, tx, observation); err != nil {
				return err
			}
			valid++
		}
		return nil
	}); err != nil {
		logger.ErrorContext(ctx, "store page failed", "error", err.Error())
		p.reportStatus(ctx, "degraded", cursor, err)
		return
	}

	p.flushOutbox(ctx, logger)
	p.flushQuarantine(ctx, logger)

	unsentOutbox, _ := p.store.HasUnsentOutbox(ctx)
	unsentQuarantine, _ := p.store.HasUnsentQuarantine(ctx)
	if unsentOutbox || unsentQuarantine {
		p.reportStatusReason(ctx, "degraded", "ingest_outbox_pending", nextCursor)
	} else {
		lastEventAt, _ := p.store.LastEventAt(ctx)
		p.reportSuccess(ctx, lastEventAt, page.GeneratedAt)
	}
	p.commitCursorIfDrained(ctx, logger, nextCursor)

	if stats, err := p.store.Stats(ctx); err == nil {
		p.metrics.SetQueues(stats.OutboxPending, stats.QuarantinePending)
	}
	if lastEventAt, err := p.store.LastEventAt(ctx); err == nil && lastEventAt != nil {
		lag := page.GeneratedAt.Sub(*lastEventAt).Seconds()
		if lag < 0 {
			lag = 0
		}
		p.metrics.SetCursorLag(lag)
		// The sensor runs on virtual time in practice, so cache age is relative
		// to generated_at, not the wall clock.
		p.metrics.SetCacheAge(lag)
	}
	p.setCircuitState()

	logger.InfoContext(ctx, "sensor page processed",
		"items", len(page.Items), "valid", valid, "quarantined", quarantined, "cursor", nextCursor)
}

func (p *Processor) enqueueQuarantine(ctx context.Context, tx *sql.Tx, raw json.RawMessage, cursor string, receivedAt time.Time, validationErr *coastal.ValidationError) error {
	messageID := observationMessageID(raw)
	reason := "contract_violation: " + validationErr.Error()
	if validationErr.Critical {
		reason = "critical_invalid: " + validationErr.Error()
	}
	_, err := store.EnqueueQuarantineTx(ctx, tx, store.QuarantineItem{
		MessageID:        messageID,
		SourcePosition:   cursor,
		ReceivedAt:       receivedAt,
		Reason:           reason,
		Retryable:        false,
		Status:           ingest.StatusParked,
		RawPayloadBase64: base64.StdEncoding.EncodeToString(raw),
		ContentType:      "application/json",
	})
	return err
}

func (p *Processor) flushOutbox(ctx context.Context, logger *slog.Logger) {
	for batch := 0; batch < 10; batch++ {
		items, err := p.store.PendingOutbox(ctx, 100)
		if err != nil {
			logger.ErrorContext(ctx, "list outbox failed", "error", err.Error())
			return
		}
		if len(items) == 0 {
			return
		}
		for _, item := range items {
			err := p.sendOutboxItem(ctx, item)
			if err == nil {
				if markErr := p.store.MarkOutboxSent(ctx, item.ID); markErr != nil {
					logger.ErrorContext(ctx, "mark outbox sent failed", "error", markErr.Error())
				}
				continue
			}
			next := backoff(item.Attempts)
			if markErr := p.store.MarkOutboxFailed(ctx, item.ID, err.Error(), next); markErr != nil {
				logger.ErrorContext(ctx, "mark outbox failed failed", "error", markErr.Error())
			}
			logger.WarnContext(ctx, "ingest outbox item failed",
				"producer_event_id", item.ProducerEventID, "attempt", item.Attempts+1,
				"retry_in", next.Sub(time.Now()).Round(time.Second).String(), "error", err.Error())
			if !ingest.IsRetryable(err) {
				// Terminal 4xx: keep the row for inspection but do not hammer the
				// Aggregator. The next slice will forward it to unprocessed.
				return
			}
		}
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

func (p *Processor) flushQuarantine(ctx context.Context, logger *slog.Logger) {
	items, err := p.store.PendingQuarantine(ctx, 100)
	if err != nil {
		logger.ErrorContext(ctx, "list quarantine failed", "error", err.Error())
		return
	}
	for _, item := range items {
		err := p.ingest.StoreUnprocessed(ctx, ingest.UnprocessedMessage{
			MessageID:        item.MessageID,
			Source:           "SENSOR_PANTAI",
			SourcePosition:   ingest.SourcePosition{Type: "coastal_cursor", Value: item.SourcePosition},
			ReceivedAt:       item.ReceivedAt,
			Reason:           item.Reason,
			Retryable:        item.Retryable,
			Status:           item.Status,
			RawPayloadBase64: item.RawPayloadBase64,
			ContentType:      item.ContentType,
		})
		if err == nil {
			if markErr := p.store.MarkQuarantineSent(ctx, item.MessageID); markErr != nil {
				logger.ErrorContext(ctx, "mark quarantine sent failed", "error", markErr.Error())
			}
			continue
		}
		next := backoff(item.Attempts)
		if markErr := p.store.MarkQuarantineFailed(ctx, item.MessageID, err.Error(), next); markErr != nil {
			logger.ErrorContext(ctx, "mark quarantine failed failed", "error", markErr.Error())
		}
		logger.WarnContext(ctx, "unprocessed forward failed", "message_id", item.MessageID, "error", err.Error())
		if !ingest.IsRetryable(err) {
			return
		}
	}
}

func (p *Processor) commitCursorIfDrained(ctx context.Context, logger *slog.Logger, cursor string) {
	if cursor == "" {
		return
	}
	unsentOutbox, err := p.store.HasUnsentOutbox(ctx)
	if err != nil || unsentOutbox {
		return
	}
	unsentQuarantine, err := p.store.HasUnsentQuarantine(ctx)
	if err != nil || unsentQuarantine {
		return
	}
	if err := p.store.WithTx(ctx, func(tx *sql.Tx) error {
		return store.SetCursorTx(ctx, tx, cursor)
	}); err != nil {
		logger.ErrorContext(ctx, "commit cursor failed", "error", err.Error())
	}
}

func (p *Processor) setCircuitState() {
	if p.config.CircuitState != nil {
		p.metrics.SetCircuitState(p.config.CircuitState())
	}
}

func (p *Processor) reportSuccess(ctx context.Context, lastEventAt *time.Time, generatedAt time.Time) {
	var lag *float64
	if lastEventAt != nil {
		value := generatedAt.Sub(*lastEventAt).Seconds()
		if value < 0 {
			value = 0
		}
		lag = &value
	}
	if err := p.ingest.ReportSourceStatus(ctx, ingest.SourceStatus{
		Source:      "SENSOR_PANTAI",
		Status:      "ok",
		LastEventAt: lastEventAt,
		LagSeconds:  lag,
	}); err != nil {
		p.logger.WarnContext(ctx, "report source status failed", "error", err.Error())
	}
}

func (p *Processor) reportStatus(ctx context.Context, status, cursor string, cause error) {
	reason := "upstream_unavailable"
	if errors.Is(cause, sensor.ErrCircuitOpen) {
		reason = "circuit_open"
	}
	var sensorErr *sensor.APIError
	if errors.As(cause, &sensorErr) {
		switch sensorErr.StatusCode {
		case 429:
			reason = "upstream_timeout"
		case 503:
			reason = "upstream_unavailable"
		}
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		reason = "upstream_timeout"
	}
	p.reportStatusReason(ctx, status, reason, cursor)
}

func (p *Processor) reportStatusReason(ctx context.Context, status, reason, cursor string) {
	if err := p.ingest.ReportSourceStatus(ctx, ingest.SourceStatus{
		Source:     "SENSOR_PANTAI",
		Status:     status,
		ReasonCode: reason,
		Details:    map[string]any{"cursor": cursor},
	}); err != nil {
		p.logger.WarnContext(ctx, "report source status failed", "error", err.Error())
	}
}

func observationMessageID(raw json.RawMessage) string {
	var envelope struct {
		ObservationID string `json:"observation_id"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.ObservationID != "" {
		return "coastal-observation-" + envelope.ObservationID
	}
	sum := sha256.Sum256(raw)
	return "coastal-invalid-" + hex.EncodeToString(sum[:8])
}

func backoff(attempts int) time.Time {
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 5 {
		attempts = 5
	}
	base := time.Duration(1<<attempts) * time.Second
	jitter := time.Duration(time.Now().UnixNano()%500) * time.Millisecond
	return time.Now().Add(base + jitter).UTC()
}
