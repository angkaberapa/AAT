package main

import (
	"strings"
	"testing"
	"time"
)

func TestLoadJSONAcceptsArrayAndAliases(t *testing.T) {
	events, err := loadJSON(strings.NewReader(`[{"id":"eq-1","place":"Palu","mag":6.2,"depth":15,"lat":-0.9,"lng":119.8,"time":"2026-11-16T10:00:00Z","tsunami":true}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventID != "eq-1" || events[0].RegionName != "Palu" || !events[0].PotentialTsunami {
		t.Fatalf("events = %#v", events)
	}
	if events[0].OccurredAt != time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC) {
		t.Fatalf("occurred_at = %s", events[0].OccurredAt)
	}
}

func TestLoadJSONAcceptsEnvelope(t *testing.T) {
	events, err := loadJSON(strings.NewReader(`{"events":[{"event_id":"eq-2","region_name":"Aceh","magnitude":5.1,"depth_km":20,"epicenter_lat":5.5,"epicenter_lon":95.3,"occurred_at":"2026-11-16 11:00:00"}]}`))
	if err != nil || len(events) != 1 || events[0].EventID != "eq-2" {
		t.Fatalf("events=%#v err=%v", events, err)
	}
}

func TestLoadCSV(t *testing.T) {
	csv := "earthquake_id,location,magnitude,depth,latitude,longitude,origin_time\nEQ-3,Bandung,4.8,10,-6.9,107.6,2026/11/16 12:00:00\n"
	events, err := loadCSV(strings.NewReader(csv))
	if err != nil || len(events) != 1 || events[0].EventID != "EQ-3" {
		t.Fatalf("events=%#v err=%v", events, err)
	}
}

func TestLoadCatalogueRejectsMissingRequiredValues(t *testing.T) {
	_, err := loadJSON(strings.NewReader(`[{"id":"eq-1"}]`))
	if err == nil {
		t.Fatal("expected validation error")
	}
}
