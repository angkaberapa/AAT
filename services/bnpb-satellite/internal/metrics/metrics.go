// Package metrics exposes the Feed Satelit pipeline metrics used by the M2
// observability dashboard.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry       *prometheus.Registry
	events         *prometheus.CounterVec
	lateDetections prometheus.Counter
	singles        prometheus.Gauge
	activeFires    prometheus.Gauge
	outboxPending  prometheus.Gauge
	ingestLag      prometheus.Gauge
	latency        prometheus.Histogram
	fireChanges    *prometheus.CounterVec
	reconnects     prometheus.Counter
}

func New() *Metrics {
	registry := prometheus.NewRegistry()
	m := &Metrics{
		registry: registry,
		events: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "satellite_events_total",
			Help: "Feed events by type and result.",
		}, []string{"type", "result"}),
		lateDetections: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "satellite_late_detections_total",
			Help: "Detections older than the allowed lateness window.",
		}),
		singles: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "satellite_single_detections",
			Help: "Active detections that are not part of any fire.",
		}),
		activeFires: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "satellite_active_fires",
			Help: "Fires in the current projection.",
		}),
		outboxPending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "satellite_ingest_outbox_pending",
			Help: "Ingest commands not yet accepted by the Aggregator.",
		}),
		ingestLag: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "satellite_ingest_lag_seconds",
			Help: "Seconds since the last feed event was applied.",
		}),
		latency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "satellite_event_processing_seconds",
			Help:    "Time from receiving one SSE frame to committing its effects.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
		}),
		fireChanges: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "satellite_fire_changes_total",
			Help: "Fire projections written by operation.",
		}, []string{"operation"}),
		reconnects: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "satellite_stream_reconnects_total",
			Help: "Stream connection attempts after the first one.",
		}),
	}
	registry.MustRegister(
		m.events, m.lateDetections, m.singles, m.activeFires, m.outboxPending,
		m.ingestLag, m.latency, m.fireChanges, m.reconnects,
	)
	return m
}

// Gatherer = registry metrik pipeline, digabung ke GET /metrics milik core (M2 observabilitas).
func (m *Metrics) Gatherer() prometheus.Gatherer {
	if m == nil {
		return nil
	}
	return m.registry
}

func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) IncEvent(eventType, result string) {
	if m == nil {
		return
	}
	m.events.WithLabelValues(eventType, result).Inc()
}

func (m *Metrics) IncLate() {
	if m == nil {
		return
	}
	m.lateDetections.Inc()
}

func (m *Metrics) SetProjection(singles, fires int) {
	if m == nil {
		return
	}
	m.singles.Set(float64(singles))
	m.activeFires.Set(float64(fires))
}

func (m *Metrics) SetOutboxPending(count int) {
	if m == nil {
		return
	}
	m.outboxPending.Set(float64(count))
}

func (m *Metrics) SetIngestLag(seconds float64) {
	if m == nil {
		return
	}
	m.ingestLag.Set(seconds)
}

func (m *Metrics) ObserveProcessing(duration time.Duration) {
	if m == nil {
		return
	}
	m.latency.Observe(duration.Seconds())
}

func (m *Metrics) IncFireChange(operation string) {
	if m == nil {
		return
	}
	m.fireChanges.WithLabelValues(operation).Inc()
}

func (m *Metrics) IncReconnect() {
	if m == nil {
		return
	}
	m.reconnects.Inc()
}
