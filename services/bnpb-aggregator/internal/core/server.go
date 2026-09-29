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
)

// NewMux returns a ServeMux with GET /health already registered.
func NewMux(service string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": service})
	})
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
		Addr:              ":" + strconv.Itoa(port),
		Handler:           withObservability(logger, handler),
		ReadHeaderTimeout: 5 * time.Second,
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
		next.ServeHTTP(rec, r.WithContext(ctx))

		if r.URL.Path == "/health" {
			return // probes are noisy
		}
		logger.InfoContext(ctx, "request handled",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"latency_ms", millis(time.Since(start)),
		)
	})
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
