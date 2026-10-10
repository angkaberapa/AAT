package bmkg

import (
	"context"
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

func TestFetchCorrelatesWarningAndMapsCanonicalEvent(t *testing.T) {
	t.Parallel()
	calls := 0
	httpClient := fakeHTTPClient{do: func(request *http.Request, target string) (*http.Response, error) {
		calls++
		if target != "bmkg" {
			t.Fatalf("target = %q, want bmkg", target)
		}
		if request.Header.Get("X-BMKG-Key") != "bmkg-test-key" {
			t.Fatalf("X-BMKG-Key = %q", request.Header.Get("X-BMKG-Key"))
		}
		if request.URL.Query().Get("since") != "2026-10-03T08:00:00Z" {
			t.Fatalf("since = %q", request.URL.Query().Get("since"))
		}

		switch request.URL.Path {
		case "/seismic-events":
			return response(http.StatusOK, `{"data":[{
				"event_id":"BMKG-EQ-000021",
				"magnitude":4.5,
				"depth_km":12.5,
				"epicenter_lat":-6.2,
				"epicenter_lon":106.8,
				"region_name":"Banten",
				"occurred_at":"2026-10-03T08:30:00Z",
				"potential_tsunami":true,
				"future_field":"preserved"
			}]}`), nil
		case "/tsunami-warnings":
			return response(http.StatusOK, `{"data":[{
				"warning_id":"BMKG-TW-000001",
				"related_event_id":"BMKG-EQ-000021",
				"threat_level":"Awas",
				"affected_zones":["Banten"],
				"estimated_arrival":"2026-10-03T09:00:00Z",
				"issued_at":"2026-10-03T08:31:00Z",
				"bulletin_source":"sensor-network-a"
			}]}`), nil
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
			return nil, nil
		}
	}}

	client, err := NewClient("http://bmkg.test", "bmkg-test-key", httpClient, testMapper(t))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	since := time.Date(2026, time.October, 3, 8, 0, 0, 0, time.UTC)
	events, err := client.Fetch(context.Background(), &since)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if calls != 2 || len(events) != 1 {
		t.Fatalf("calls = %d, events = %d; want 2 and 1", calls, len(events))
	}
	event := events[0]
	if event.Source != domain.SourceBMKG || event.HazardType != domain.HazardTypeSeismic {
		t.Fatalf("unexpected classification: %+v", event)
	}
	if event.Severity != domain.SeverityAwas {
		t.Fatalf("severity = %q, want %q", event.Severity, domain.SeverityAwas)
	}
	if _, exists := event.Attributes["tsunami_warning"]; !exists {
		t.Fatal("event does not include related tsunami warning")
	}
	if got := event.Attributes["future_field"]; got != "preserved" {
		t.Fatalf("unknown seismic field = %v, want forwarded value", got)
	}
	warningAttributes := event.Attributes["tsunami_warning"].(map[string]any)
	if got := warningAttributes["bulletin_source"]; got != "sensor-network-a" {
		t.Fatalf("unknown warning field = %v, want forwarded value", got)
	}
}

func TestFetchAcceptsSpecWarningWithoutIssuedAt(t *testing.T) {
	t.Parallel()
	warningCalls := 0
	httpClient := fakeHTTPClient{do: func(request *http.Request, _ string) (*http.Response, error) {
		switch request.URL.Path {
		case "/seismic-events":
			return response(http.StatusOK, `{"data":[{
				"event_id":"BMKG-EQ-NO-ISSUED-AT",
				"magnitude":4.5,
				"depth_km":12.5,
				"epicenter_lat":-6.2,
				"epicenter_lon":106.8,
				"region_name":"Banten",
				"occurred_at":"2026-10-03T08:30:00Z",
				"potential_tsunami":true
			}]}`), nil
		case "/tsunami-warnings":
			warningCalls++
			if got := request.URL.Query().Get("since"); got != "" {
				t.Fatalf("warning since = %q, want empty without source issued_at", got)
			}
			return response(http.StatusOK, `{"data":[{
				"warning_id":"BMKG-TW-NO-ISSUED-AT",
				"related_event_id":"BMKG-EQ-NO-ISSUED-AT",
				"threat_level":"Awas",
				"affected_zones":["Banten"],
				"estimated_arrival":"2026-10-03T09:00:00Z"
			}]}`), nil
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
			return nil, nil
		}
	}}

	client, err := NewClient("http://bmkg.test", "bmkg-test-key", httpClient, testMapper(t))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	events, err := client.Fetch(context.Background(), nil)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(events) != 1 || events[0].Severity != domain.SeverityAwas {
		t.Fatalf("events = %+v, want one AWAS event", events)
	}
	client.CommitFetch()

	if _, err := client.Fetch(context.Background(), nil); err != nil {
		t.Fatalf("second Fetch() error = %v", err)
	}
	if warningCalls != 2 {
		t.Fatalf("warning calls = %d, want 2", warningCalls)
	}
}

func TestFetchClassifiesSourceOutage(t *testing.T) {
	t.Parallel()
	httpClient := fakeHTTPClient{do: func(*http.Request, string) (*http.Response, error) {
		return response(http.StatusServiceUnavailable, `{"error":"outage"}`), nil
	}}
	client, err := NewClient("http://bmkg.test", "bmkg-test-key", httpClient, testMapper(t))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	_, err = client.Fetch(context.Background(), nil)
	sourceError, ok := err.(*ingest.SourceError)
	if !ok {
		t.Fatalf("Fetch() error type = %T, want *ingest.SourceError", err)
	}
	if sourceError.Kind != ingest.ErrorUnavailable || sourceError.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unexpected source error: %+v", sourceError)
	}
}

func TestFetchSkipsMalformedRecordAndCommitsObservedTimestamp(t *testing.T) {
	t.Parallel()
	seismicCalls := 0
	httpClient := fakeHTTPClient{do: func(request *http.Request, _ string) (*http.Response, error) {
		switch request.URL.Path {
		case "/seismic-events":
			seismicCalls++
			if seismicCalls == 2 {
				if got := request.URL.Query().Get("since"); got != "2026-10-03T08:30:00Z" {
					t.Fatalf("second seismic since = %q, want malformed record timestamp", got)
				}
				return response(http.StatusOK, `{"data":[]}`), nil
			}
			return response(http.StatusOK, `{"data":[
				{
					"event_id":"BMKG-EQ-BAD",
					"depth_km":10,
					"epicenter_lat":-6.2,
					"epicenter_lon":106.8,
					"region_name":"Banten",
					"occurred_at":"2026-10-03T08:30:00Z",
					"potential_tsunami":false
				},
				{
					"event_id":"BMKG-EQ-GOOD",
					"magnitude":5.5,
					"depth_km":10,
					"epicenter_lat":-6.2,
					"epicenter_lon":106.8,
					"region_name":"Banten",
					"occurred_at":"2026-10-03T08:20:00Z",
					"potential_tsunami":false
				}
			]}`), nil
		case "/tsunami-warnings":
			return response(http.StatusOK, `{"data":[]}`), nil
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
			return nil, nil
		}
	}}

	client, err := NewClient("http://bmkg.test", "bmkg-test-key", httpClient, testMapper(t))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	since := time.Date(2026, time.October, 3, 8, 0, 0, 0, time.UTC)
	events, err := client.Fetch(context.Background(), &since)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(events) != 1 || events[0].SourceRefID != "BMKG-EQ-GOOD" {
		t.Fatalf("events = %+v, want only valid record", events)
	}
	client.CommitFetch()

	if _, err := client.Fetch(context.Background(), &since); err != nil {
		t.Fatalf("second Fetch() error = %v", err)
	}
}

func TestFetchAppliesLateWarningRetriesUntilCommitAndKeepsHighestLevel(t *testing.T) {
	t.Parallel()
	eventCalls := 0
	warningCalls := 0
	httpClient := fakeHTTPClient{do: func(request *http.Request, _ string) (*http.Response, error) {
		switch request.URL.Path {
		case "/seismic-events":
			eventCalls++
			if eventCalls > 1 {
				return response(http.StatusOK, `{"data":[]}`), nil
			}
			return response(http.StatusOK, `{"data":[{
				"event_id":"BMKG-EQ-LATE",
				"magnitude":4.5,
				"depth_km":12,
				"epicenter_lat":-6.2,
				"epicenter_lon":106.8,
				"region_name":"Banten",
				"occurred_at":"2026-10-03T08:00:00Z",
				"potential_tsunami":true
			}]}`), nil
		case "/tsunami-warnings":
			warningCalls++
			switch warningCalls {
			case 1:
				return response(http.StatusOK, `{"data":[]}`), nil
			case 2, 3:
				return response(http.StatusOK, `{"data":[{
					"warning_id":"BMKG-TW-WASPADA",
					"related_event_id":"BMKG-EQ-LATE",
					"threat_level":"Waspada",
					"affected_zones":["Banten"],
					"estimated_arrival":"2026-10-03T08:30:00Z",
					"issued_at":"2026-10-03T08:01:00Z"
				}]}`), nil
			default:
				return response(http.StatusOK, `{"data":[
					{
						"warning_id":"BMKG-TW-AWAS",
						"related_event_id":"BMKG-EQ-LATE",
						"threat_level":"Awas",
						"affected_zones":["Banten"],
						"estimated_arrival":"2026-10-03T08:30:00Z",
						"issued_at":"2026-10-03T08:02:00Z"
					},
					{
						"warning_id":"BMKG-TW-SIAGA-LATER",
						"related_event_id":"BMKG-EQ-LATE",
						"threat_level":"Siaga",
						"affected_zones":["Banten"],
						"estimated_arrival":"2026-10-03T08:35:00Z",
						"issued_at":"2026-10-03T08:03:00Z"
					}
				]}`), nil
			}
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
			return nil, nil
		}
	}}

	client, err := NewClient("http://bmkg.test", "bmkg-test-key", httpClient, testMapper(t))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	initial, err := client.Fetch(context.Background(), nil)
	if err != nil || len(initial) != 1 {
		t.Fatalf("initial Fetch() events = %d, error = %v", len(initial), err)
	}
	client.CommitFetch()

	late, err := client.Fetch(context.Background(), nil)
	if err != nil || len(late) != 1 || late[0].Severity != domain.SeverityWaspada {
		t.Fatalf("late warning Fetch() = %+v, error = %v", late, err)
	}
	retried, err := client.Fetch(context.Background(), nil)
	if err != nil || len(retried) != 1 || retried[0].Severity != domain.SeverityWaspada {
		t.Fatalf("retried warning Fetch() = %+v, error = %v", retried, err)
	}
	client.CommitFetch()

	highest, err := client.Fetch(context.Background(), nil)
	if err != nil || len(highest) != 1 || highest[0].Severity != domain.SeverityAwas {
		t.Fatalf("highest warning Fetch() = %+v, error = %v", highest, err)
	}
}
