package summarizer

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-ai-advisory/internal/detector"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSummarizeUsesLLM(t *testing.T) {
	var path, auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		auth = r.Header.Get("Authorization")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body["model"] != "test-model" {
			t.Errorf("model = %v", body["model"])
		}
		thinking, _ := body["thinking"].(map[string]any)
		if thinking["type"] != "disabled" {
			t.Errorf("thinking = %v", body["thinking"])
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Ringkasan dari LLM."}}]}`))
	}))
	defer server.Close()

	s := New(Config{BaseURL: server.URL, APIKey: "secret", Model: "test-model", Thinking: "disabled", Timeout: time.Second}, discardLogger())
	result := s.Summarize(context.Background(),
		[]Event{{Source: "BMKG", HazardType: "EARTHQUAKE", Severity: "AWAS", AreaName: "Palu"}},
		[]detector.Advisory{{Source: "BMKG", HazardType: "EARTHQUAKE", AreaName: "Palu", Level: "critical", Score: 8}})

	if result.Source != "llm" || result.Summary != "Ringkasan dari LLM." {
		t.Fatalf("result = %+v", result)
	}
	if path != "/chat/completions" {
		t.Fatalf("path = %q", path)
	}
	if auth != "Bearer secret" {
		t.Fatalf("authorization = %q", auth)
	}
}

func TestSummarizeFallsBackOnLLMError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream down", http.StatusBadGateway)
	}))
	defer server.Close()

	s := New(Config{BaseURL: server.URL, APIKey: "secret", Model: "test-model", Timeout: time.Second}, discardLogger())
	result := s.Summarize(context.Background(), nil, nil)

	if result.Source != "fallback" || result.Reason != "llm_error" {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Summary, "tidak terpengaruh") {
		t.Fatalf("fallback summary = %q", result.Summary)
	}
}

func TestSummarizeFallsBackWhenNotConfigured(t *testing.T) {
	s := New(Config{}, discardLogger())
	result := s.Summarize(context.Background(), nil, nil)

	if result.Source != "fallback" || result.Reason != "llm_not_configured" {
		t.Fatalf("result = %+v", result)
	}
}
