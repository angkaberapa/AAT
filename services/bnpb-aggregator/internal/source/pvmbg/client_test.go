package pvmbg

import (
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
	since := time.Date(2026, time.October, 3, 8, 0, 0, 0, time.UTC)
	events, err := client.Fetch(context.Background(), &since)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(events) != 1 || events[0].SourceRefID != "PVMBG-RPT-GOOD" {
		t.Fatalf("events = %+v, want only valid report", events)
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

	_, err = client.Fetch(context.Background(), nil)
	var sourceError *ingest.SourceError
	if !errors.As(err, &sourceError) || sourceError.Kind != ingest.ErrorInvalidResponse {
		t.Fatalf("Fetch() error = %v, want invalid response size error", err)
	}
}
