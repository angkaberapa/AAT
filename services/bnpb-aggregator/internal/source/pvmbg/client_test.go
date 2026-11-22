package pvmbg

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/ingest"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/mapping"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/reference"
)

type fakeHTTPClient struct {
	do func(*http.Request, string) (*http.Response, error)
}

type fakeFailureSink struct {
	failures []ingest.RawFailure
}

func (sink *fakeFailureSink) StoreRawFailure(_ context.Context, failure ingest.RawFailure) error {
	sink.failures = append(sink.failures, failure)
	return nil
}

func (client fakeHTTPClient) Do(request *http.Request, target string) (*http.Response, error) {
	return client.do(request, target)
}

func testMapper(t *testing.T) *mapping.Mapper {
	t.Helper()
	mapper, err := mapping.NewMapper(map[string]reference.Volcano{
		"MERAPI": {Name: "Gunung Merapi", Latitude: -7.5407, Longitude: 110.4457},
	})
	if err != nil {
		t.Fatalf("mapping.NewMapper() error = %v", err)
	}
	mapper.SetClock(func() time.Time {
		return time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC)
	})
	return mapper
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestFetchHandlesRuntimeSchemaSwitchWithoutRecreatingClient(t *testing.T) {
	t.Parallel()

	calls := 0
	httpClient := fakeHTTPClient{do: func(request *http.Request, target string) (*http.Response, error) {
		calls++
		if target != "pvmbg" {
			t.Fatalf("target = %q, want pvmbg", target)
		}
		if request.URL.Path != "/volcanic-reports" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if request.URL.Query().Get("since") != "2026-10-03T08:00:00Z" {
			t.Fatalf("since = %q", request.URL.Query().Get("since"))
		}
		if request.Header.Get("Authorization") != "Bearer pvmbg.test-token" {
			t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
		}

		confidence := ""
		if calls == 2 {
			confidence = `,"confidence_level":0.82`
		}
		return response(http.StatusOK, `{"data":[{
			"report_id":"PVMBG-RPT-000021",
			"volcano_id":"MERAPI",
			"alert_level":"Siaga",
			"eruption_count_24h":7,
			"ash_column_height_m":1500,
			"reported_at":"2026-10-03T08:30:00Z",
			"future_additive_field":"preserved"`+confidence+`
		}]}`), nil
	}}

	client, err := NewClient("http://pvmbg.test", "pvmbg.test-token", httpClient, testMapper(t))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	since := time.Date(2026, time.October, 3, 8, 0, 0, 0, time.UTC)

	versionOne, err := client.Fetch(context.Background(), &since)
	if err != nil {
		t.Fatalf("Fetch(schema v1) error = %v", err)
	}
	versionTwo, err := client.Fetch(context.Background(), &since)
	if err != nil {
		t.Fatalf("Fetch(schema v2) error = %v", err)
	}

	if calls != 2 {
		t.Fatalf("HTTP calls = %d, want 2", calls)
	}
	if _, exists := versionOne[0].Attributes["confidence_level"]; exists {
		t.Fatal("schema v1 unexpectedly contains confidence_level")
	}
	if got := versionTwo[0].Attributes["confidence_level"]; got != 0.82 {
		t.Fatalf("schema v2 confidence_level = %v, want 0.82", got)
	}
	if got := versionOne[0].Attributes["future_additive_field"]; got != "preserved" {
		t.Fatalf("schema v1 unknown field = %v, want forwarded value", got)
	}
	if got := versionTwo[0].Attributes["future_additive_field"]; got != "preserved" {
		t.Fatalf("schema v2 unknown field = %v, want forwarded value", got)
	}
	if versionTwo[0].Source != domain.SourcePVMBG {
		t.Fatalf("source = %q", versionTwo[0].Source)
	}
}

func TestFetchClassifiesRejectedCredential(t *testing.T) {
	t.Parallel()
	httpClient := fakeHTTPClient{do: func(*http.Request, string) (*http.Response, error) {
		return response(http.StatusForbidden, `{"detail":"invalid token"}`), nil
	}}
	client, err := NewClient("http://pvmbg.test", "wrong", httpClient, testMapper(t))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	_, err = client.Fetch(context.Background(), nil)
	sourceError, ok := err.(*ingest.SourceError)
	if !ok {
		t.Fatalf("Fetch() error type = %T, want *ingest.SourceError", err)
	}
	if sourceError.Kind != ingest.ErrorUnauthorized || sourceError.StatusCode != http.StatusForbidden {
		t.Fatalf("unexpected source error: %+v", sourceError)
	}
}

func TestFetchSkipsMalformedReportAndCommitsObservedTimestamp(t *testing.T) {
	t.Parallel()
	calls := 0
	httpClient := fakeHTTPClient{do: func(request *http.Request, _ string) (*http.Response, error) {
		calls++
		if calls == 2 {
			if got := request.URL.Query().Get("since"); got != "2026-10-03T08:30:00Z" {
				t.Fatalf("second since = %q, want malformed report timestamp", got)
			}
			return response(http.StatusOK, `{"data":[]}`), nil
		}
		return response(http.StatusOK, `{"data":[
			{
				"report_id":"PVMBG-RPT-BAD",
				"volcano_id":"MERAPI",
				"alert_level":"Siaga",
				"ash_column_height_m":1500,
				"reported_at":"2026-10-03T08:30:00Z"
			},
			{
				"report_id":"PVMBG-RPT-GOOD",
				"volcano_id":"MERAPI",
				"alert_level":"Siaga",
				"eruption_count_24h":7,
				"ash_column_height_m":1500,
				"reported_at":"2026-10-03T08:20:00Z"
			}
		]}`), nil
	}}

	client, err := NewClient("http://pvmbg.test", "pvmbg.test-token", httpClient, testMapper(t))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	failures := &fakeFailureSink{}
	client.SetFailureSink(failures)
	since := time.Date(2026, time.October, 3, 8, 0, 0, 0, time.UTC)
	events, err := client.Fetch(context.Background(), &since)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(events) != 1 || events[0].SourceRefID != "PVMBG-RPT-GOOD" {
		t.Fatalf("events = %+v, want only valid report", events)
	}
	if len(failures.failures) != 1 || failures.failures[0].SourceRefID != "PVMBG-RPT-BAD" {
		t.Fatalf("parked failures = %+v, want malformed report intact", failures.failures)
	}
	client.CommitFetch()

	if _, err := client.Fetch(context.Background(), &since); err != nil {
		t.Fatalf("second Fetch() error = %v", err)
	}
}

func TestFetchRejectsOversizedResponse(t *testing.T) {
	t.Parallel()
	httpClient := fakeHTTPClient{do: func(*http.Request, string) (*http.Response, error) {
		return response(http.StatusOK, strings.Repeat(" ", maxResponseBytes+1)), nil
	}}
	client, err := NewClient("http://pvmbg.test", "pvmbg.test-token", httpClient, testMapper(t))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	failures := &fakeFailureSink{}
	client.SetFailureSink(failures)

	_, err = client.Fetch(context.Background(), nil)
	var sourceError *ingest.SourceError
	if !errors.As(err, &sourceError) || sourceError.Kind != ingest.ErrorInvalidResponse {
		t.Fatalf("Fetch() error = %v, want invalid response size error", err)
	}
	if len(failures.failures) != 1 || !strings.HasPrefix(failures.failures[0].Reason, "payload_too_large:") {
		t.Fatalf("failures = %+v, want intact oversized response", failures.failures)
	}
	if len(failures.failures[0].RawPayload) != maxResponseBytes+1 {
		t.Fatalf("raw payload bytes = %d, want %d", len(failures.failures[0].RawPayload), maxResponseBytes+1)
	}
}

func TestFetchDistinguishesTruncatedAndDeepJSON(t *testing.T) {
	tests := []struct {
		name       string
		body       []byte
		reasonHead string
	}{
		{name: "truncated", body: []byte(`{"data":[{"report_id":"truncated"}`), reasonHead: "invalid_json:"},
		{name: "deep", body: append(append([]byte(`{"data":[`), bytes.Repeat([]byte(`{"nested":`), maxJSONDepth+1)...), append([]byte("null"), append(bytes.Repeat([]byte("}"), maxJSONDepth+1), []byte("]}")...)...)...), reasonHead: "json_too_deep:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpClient := fakeHTTPClient{do: func(*http.Request, string) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(tt.body)), Header: make(http.Header)}, nil
			}}
			client, err := NewClient("http://pvmbg.test", "pvmbg.test-token", httpClient, testMapper(t))
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			failures := &fakeFailureSink{}
			client.SetFailureSink(failures)
			_, err = client.Fetch(context.Background(), nil)
			if err == nil || len(failures.failures) != 1 || !strings.HasPrefix(failures.failures[0].Reason, tt.reasonHead) {
				t.Fatalf("Fetch() error=%v failures=%+v, want %s", err, failures.failures, tt.reasonHead)
			}
		})
	}
}

func TestDecodeReportRequiresExplicitCompatibilityUpgrade(t *testing.T) {
	base := `{"report_id":"PVMBG-RPT-1","volcano_id":"MERAPI","level":"Siaga","eruption_count_24h":"7","ash_column_height_m":1500,"reported_at":"2026-10-03T08:30:00Z"}`
	if _, err := DecodeReport([]byte(base), 2); err == nil {
		t.Fatal("schema v4 decoded before compatibility upgrade")
	}
	report, err := DecodeReport([]byte(base), 4)
	if err != nil {
		t.Fatalf("DecodeReport(v4 compatibility) error = %v", err)
	}
	if report.AlertLevel != "Siaga" || report.EruptionCount24H != 7 {
		t.Fatalf("normalized report = %+v", report)
	}

	missing := `{"report_id":"PVMBG-RPT-2","volcano_id":"MERAPI","alert_level":"Waspada","ash_column_height_m":0,"reported_at":"2026-10-03T08:31:00Z"}`
	if _, err := DecodeReport([]byte(missing), 4); err == nil {
		t.Fatal("missing required field decoded before v5 compatibility")
	}
	report, err = DecodeReport([]byte(missing), 5)
	if err != nil || report.EruptionCount24H != 0 {
		t.Fatalf("DecodeReport(v5 compatibility) report=%+v err=%v", report, err)
	}
}
