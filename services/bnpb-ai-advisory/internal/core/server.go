package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// NewMux returns a ServeMux with GET /health and GET /metrics already registered, and turns on
// tracing for this service (see telemetry.go).
func NewMux(service string) *http.ServeMux {
	initTracing(service)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": service})
	})
	mux.Handle("GET /metrics", metricsHandler())
	return mux
}

// WriteJSON writes v as a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Serve runs handler on port, wrapped with correlation ID handling and request
// logging, until ctx is cancelled; then it shuts down gracefully.
func Serve(ctx context.Context, logger *slog.Logger, port int, handler http.Handler) error {
	srv := &http.Server{
		Addr: ":" + strconv.Itoa(port),
		// otelhttp membuat span server (melanjutkan traceparent dari pemanggil); probe & scrape
		// tidak di-trace supaya tidak memenuhi Jaeger.
		Handler: otelhttp.NewHandler(withObservability(logger, handler), "http.server",
			otelhttp.WithFilter(func(r *http.Request) bool { return !isProbe(r.URL.Path) })),
		ReadHeaderTimeout: 5 * time.Second,
		// Tanpa ini koneksi keep-alive yang diam tidak pernah ditutup: tiap koneksi memegang
		// goroutine + buffer, dan 10k client yang jarang memanggil (mis. ambil token) membuat pod
		// OOMKilled (P2 M2 uji 10k VU).
		IdleTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	logger.InfoContext(ctx, "service started", "port", port)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	logger.Info("service stopping")
	defer shutdownTracing()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// withObservability accepts or creates a correlation ID, echoes it in the response
// header, and logs every request (except /health) with its latency.
func withObservability(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(CorrelationHeader)
		if id == "" {
			id = NewCorrelationID()
		}
		ctx := WithCorrelationID(r.Context(), id)
		w.Header().Set(CorrelationHeader, id)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		req := r.WithContext(ctx)
		probe := isProbe(r.URL.Path)
		if !probe {
			httpServerInFlight.Inc()
		}
		next.ServeHTTP(rec, req)
		elapsed := time.Since(start)

		if probe {
			return // probes are noisy
		}
		httpServerInFlight.Dec()
		// ServeMux mengisi req.Pattern (mis. "GET /hazards/{id}"): label route berkardinalitas rendah.
		route := req.Pattern
		if route == "" {
			route = "unmatched"
		}
		httpServerRequests.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		httpServerDuration.WithLabelValues(r.Method, route).Observe(elapsed.Seconds())
		span := trace.SpanFromContext(ctx)
		span.SetName(route)
		span.SetAttributes(attribute.String("correlation_id", id))

		logger.InfoContext(ctx, "request handled",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"latency_ms", millis(elapsed),
		)
	})
}

func isProbe(path string) bool {
	return path == "/health" || path == "/metrics"
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func millis(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

// RunHealthcheckIfRequested implements `<binary> healthcheck`, used by the Docker
// HEALTHCHECK because the runtime image has no shell or curl. It exits the process.
func RunHealthcheckIfRequested() {
	if len(os.Args) < 2 || os.Args[1] != "healthcheck" {
		return
	}
	port := os.Getenv("PORT")
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/health", port))
	if err != nil || resp.StatusCode != http.StatusOK {
		os.Exit(1)
	}
	os.Exit(0)
}
