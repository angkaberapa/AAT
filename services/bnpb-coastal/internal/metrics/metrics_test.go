package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandlerExposesContractMetrics(t *testing.T) {
	m := New()
	m.ObserveRequest("ok", 10*time.Millisecond)
	m.IncInvalid("bad_quality")
	m.SetQueues(2, 3)
	m.SetCircuitState(2)
	m.SetCacheAge(4.5)
	m.ObserveCriticalLatency(1.2)
	m.SetCursorLag(7.5)

	recorder := httptest.NewRecorder()
	m.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()

	for _, name := range []string{
		"coastal_request_total",
		"coastal_request_duration_seconds",
		"coastal_cursor_lag",
		"coastal_invalid_observation_total",
		"coastal_quarantine_pending",
		"coastal_ingest_outbox_pending",
		"coastal_circuit_breaker_state",
		"coastal_cache_age_seconds",
		"coastal_critical_event_latency_seconds",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("metrics output missing %s", name)
		}
	}
}
