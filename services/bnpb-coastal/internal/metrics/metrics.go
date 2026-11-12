// Package metrics exposes the coastal service metrics required by the
// internal ingest contract, section 9.2.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry          *prometheus.Registry
	requests          *prometheus.CounterVec
	requestDuration   *prometheus.HistogramVec
	cursorLag         prometheus.Gauge
	invalid           *prometheus.CounterVec
	quarantinePending prometheus.Gauge
	outboxPending     prometheus.Gauge
	circuitState      prometheus.Gauge
	cacheAge          prometheus.Gauge
	criticalLatency   prometheus.Histogram
}

func New() *Metrics {
	registry := prometheus.NewRegistry()
	m := &Metrics{
		registry: registry,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "coastal_request_total",
			Help: "Sensor requests by result status.",
		}, []string{"status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "coastal_request_duration_seconds",
			Help:    "Sensor request duration.",
			Buckets: prometheus.DefBuckets,
		}, []string{"status"}),
		cursorLag: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "coastal_cursor_lag",
			Help: "Seconds between generated_at and the latest observation.",
		}),
		invalid: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "coastal_invalid_observation_total",
			Help: "Invalid observations by reason.",
		}, []string{"reason"}),
		quarantinePending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "coastal_quarantine_pending",
			Help: "Quarantine records not yet accepted by the Aggregator.",
		}),
		outboxPending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "coastal_ingest_outbox_pending",
			Help: "Ingest commands not yet accepted by the Aggregator.",
		}),
		circuitState: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "coastal_circuit_breaker_state",
			Help: "Circuit breaker state: 0 closed, 1 half-open, 2 open.",
		}),
		cacheAge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "coastal_cache_age_seconds",
			Help: "Age of the newest cached observation.",
		}),
		criticalLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "coastal_critical_event_latency_seconds",
			Help:    "Latency from observed_at to processing for critical events.",
			Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60},
		}),
	}
	registry.MustRegister(
		m.requests,
		m.requestDuration,
		m.cursorLag,
		m.invalid,
		m.quarantinePending,
		m.outboxPending,
		m.circuitState,
		m.cacheAge,
		m.criticalLatency,
	)
	return m
}

func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) ObserveRequest(status string, duration time.Duration) {
	if m == nil {
		return
	}
	m.requests.WithLabelValues(status).Inc()
	m.requestDuration.WithLabelValues(status).Observe(duration.Seconds())
}

func (m *Metrics) IncInvalid(reason string) {
	if m == nil {
		return
	}
	m.invalid.WithLabelValues(reason).Inc()
}

func (m *Metrics) SetQueues(outbox, quarantine int) {
	if m == nil {
		return
	}
	m.outboxPending.Set(float64(outbox))
	m.quarantinePending.Set(float64(quarantine))
}

func (m *Metrics) SetCircuitState(state int) {
	if m == nil {
		return
	}
	m.circuitState.Set(float64(state))
}

func (m *Metrics) SetCacheAge(seconds float64) {
	if m == nil {
		return
	}
	m.cacheAge.Set(seconds)
}

func (m *Metrics) ObserveCriticalLatency(seconds float64) {
	if m == nil {
		return
	}
	m.criticalLatency.Observe(seconds)
}

func (m *Metrics) SetCursorLag(seconds float64) {
	if m == nil {
		return
	}
	m.cursorLag.Set(seconds)
}
