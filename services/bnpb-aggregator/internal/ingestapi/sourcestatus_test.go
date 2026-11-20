package ingestapi

// Ditulis dengan bantuan LLM (Claude).

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
)

type fakeReporter struct {
	calls []string
}

func (f *fakeReporter) ReportSourceStatus(_ context.Context, source domain.Source, healthy bool, reason string, _ time.Time) error {
	state := "unhealthy:" + reason
	if healthy {
		state = "healthy"
	}
	f.calls = append(f.calls, string(source)+" "+state)
	return nil
}

func TestSourceStatusAuthorizationAndValidation(t *testing.T) {
	reporter := &fakeReporter{}
	mux := http.NewServeMux()
	RegisterSourceStatus(mux, slog.New(slog.NewTextHandler(io.Discard, nil)), reporter, "coastal-token", "satellite-token")

	post := func(token, body string) int {
		req := httptest.NewRequest(http.MethodPost, "/internal/ingest/source-status", strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	cases := []struct {
		name, token, body string
		want              int
	}{
		{"coastal ok", "coastal-token", `{"source":"SENSOR_PANTAI","status":"ok"}`, http.StatusNoContent},
		{"coastal degraded", "coastal-token", `{"source":"SENSOR_PANTAI","status":"degraded","reason_code":"circuit_open"}`, http.StatusNoContent},
		{"satellite own source", "satellite-token", `{"source":"SATELIT","status":"unavailable"}`, http.StatusNoContent},
		{"no token", "", `{"source":"SENSOR_PANTAI","status":"ok"}`, http.StatusUnauthorized},
		{"wrong token", "nope", `{"source":"SENSOR_PANTAI","status":"ok"}`, http.StatusUnauthorized},
		{"coastal token for satellite source", "coastal-token", `{"source":"SATELIT","status":"ok"}`, http.StatusForbidden},
		{"polled source not allowed", "coastal-token", `{"source":"BMKG","status":"ok"}`, http.StatusBadRequest},
		{"unknown status", "coastal-token", `{"source":"SENSOR_PANTAI","status":"great"}`, http.StatusBadRequest},
		{"bad json", "coastal-token", `{`, http.StatusBadRequest},
	}
	for _, c := range cases {
		if got := post(c.token, c.body); got != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got, c.want)
		}
	}
	want := []string{"SENSOR_PANTAI healthy", "SENSOR_PANTAI unhealthy:circuit_open", "SATELIT unhealthy:unavailable"}
	if strings.Join(reporter.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("stored %v, want %v", reporter.calls, want)
	}
}
