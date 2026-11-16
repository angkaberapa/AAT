package analytics

import (
	_ "embed"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-consumer-dashboard/internal/core"
)

//go:embed dashboard.html
var dashboardHTML []byte

type Service struct {
	baseURL string
	token   string
	client  *http.Client
	logger  *slog.Logger
}

func New(baseURL, token string, timeout time.Duration, logger *slog.Logger) *Service {
	return &Service{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: timeout},
		logger:  logger,
	}
}

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /analytics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(dashboardHTML)
	})
	mux.HandleFunc("GET /analytics/data", s.proxyData)
}

func (s *Service) proxyData(w http.ResponseWriter, r *http.Request) {
	if s.token == "" {
		writeError(w, http.StatusServiceUnavailable, "analytics credential is not configured")
		return
	}
	upstream, err := url.Parse(s.baseURL + "/internal/analytics/data")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "analytics upstream is invalid")
		return
	}
	query := upstream.Query()
	for _, key := range []string{"from", "to", "bucket"} {
		if value := r.URL.Query().Get(key); value != "" {
			query.Set(key, value)
		}
	}
	upstream.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream.String(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot create analytics request")
		return
	}
	request.Header.Set("Authorization", "Bearer "+s.token)
	request.Header.Set("Accept", "application/json")
	if id := core.CorrelationID(r.Context()); id != "-" {
		request.Header.Set(core.CorrelationHeader, id)
	}

	started := time.Now()
	response, err := s.client.Do(request)
	if err != nil {
		s.logger.WarnContext(r.Context(), "canonical analytics unavailable", "error", err.Error())
		writeError(w, http.StatusBadGateway, "canonical analytics is unavailable")
		return
	}
	defer response.Body.Close()
	s.logger.InfoContext(r.Context(), "outbound call", "target", "bnpb-aggregator", "path", upstream.Path,
		"status", response.StatusCode, "latency_ms", float64(time.Since(started).Microseconds())/1000)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if id := response.Header.Get(core.CorrelationHeader); id != "" {
		w.Header().Set(core.CorrelationHeader, id)
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(response.Body, 8<<20))
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": "analytics_unavailable", "message": message},
	})
}
