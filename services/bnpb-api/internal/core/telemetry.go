package core

// Observabilitas M2: metrik Prometheus dan tracing OpenTelemetry untuk setiap service BNPB.
// Ditulis dengan bantuan LLM (Claude).
//
//   - NewMux mendaftarkan GET /metrics (metrik HTTP masuk/keluar + runtime Go, ditambah registry
//     milik service lewat AddMetricsGatherer) dan menyalakan tracing.
//   - Tracing hanya aktif bila OTEL_EXPORTER_OTLP_ENDPOINT di-set (di Kubernetes: Jaeger). Sampler
//     mengikuti OTEL_TRACES_SAMPLER / OTEL_TRACES_SAMPLER_ARG (bawaan: semua request di-trace).
//   - Konteks trace diteruskan antar service lewat header W3C `traceparent`, berdampingan dengan
//     X-Correlation-ID. trace_id juga dicetak di setiap baris log (lihat log.go).

import (
	"context"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Metrics = registry Prometheus milik service ini. Metrik khusus service boleh didaftarkan di sini.
var Metrics = prometheus.NewRegistry()

var (
	httpServerRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_server_requests_total",
		Help: "Request HTTP yang diterima, per method, route, dan kode status.",
	}, []string{"method", "route", "code"})
	httpServerDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_server_request_duration_seconds",
		Help:    "Latensi request HTTP yang diterima.",
		Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"method", "route"})
	httpServerInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "http_server_requests_in_flight",
		Help: "Request HTTP yang sedang diproses.",
	})
	httpClientRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_client_requests_total",
		Help: "Panggilan HTTP keluar per target dan kode status (\"error\" = gagal tanpa respons).",
	}, []string{"target", "code"})
	httpClientDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_client_request_duration_seconds",
		Help:    "Latensi panggilan HTTP keluar per target.",
		Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"target"})
)

func init() {
	Metrics.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		httpServerRequests, httpServerDuration, httpServerInFlight,
		httpClientRequests, httpClientDuration,
	)
}

var (
	extraMu        sync.Mutex
	extraGatherers []prometheus.Gatherer
)

// AddMetricsGatherer ikut menyajikan registry lain (mis. metrik pipeline service) di /metrics.
func AddMetricsGatherer(g prometheus.Gatherer) {
	if g == nil {
		return
	}
	extraMu.Lock()
	defer extraMu.Unlock()
	extraGatherers = append(extraGatherers, g)
}

// allGatherers menggabungkan registry core dengan registry tambahan saat di-scrape (registry
// tambahan bisa didaftarkan setelah NewMux dipanggil).
type allGatherers struct{}

func (allGatherers) Gather() ([]*dto.MetricFamily, error) {
	extraMu.Lock()
	gs := append(prometheus.Gatherers{Metrics}, extraGatherers...)
	extraMu.Unlock()
	return gs.Gather()
}

func metricsHandler() http.Handler {
	return promhttp.HandlerFor(allGatherers{}, promhttp.HandlerOpts{})
}

var (
	tracingOnce     sync.Once
	tracingShutdown = func(context.Context) error { return nil }
)

// initTracing memasang propagator W3C dan, bila endpoint OTLP dikonfigurasi, exporter trace.
func initTracing(service string) {
	tracingOnce.Do(func() {
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{}, propagation.Baggage{}))
		if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
			return
		}
		exporter, err := otlptracehttp.New(context.Background())
		if err != nil {
			return
		}
		res := resource.NewSchemaless(
			attribute.String("service.name", service),
			attribute.String("service.instance.id", os.Getenv("POD_NAME")),
		)
		tp := sdktrace.NewTracerProvider(
			// Antrean dibatasi: saat beban tinggi span dibuang, bukan memori yang membengkak.
			sdktrace.WithBatcher(exporter, sdktrace.WithMaxQueueSize(4096), sdktrace.WithBatchTimeout(2*time.Second)),
			sdktrace.WithResource(res),
		)
		otel.SetTracerProvider(tp)
		tracingShutdown = tp.Shutdown
	})
}

// shutdownTracing mengirim sisa span sebelum proses berhenti.
func shutdownTracing() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tracingShutdown(ctx)
}
