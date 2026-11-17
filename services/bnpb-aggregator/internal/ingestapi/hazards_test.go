package ingestapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

type fakeWriter struct {
	events   []domain.HazardEvent
	deleted  []string
	existing map[string]bool
}

func (f *fakeWriter) IngestHazard(_ context.Context, event domain.HazardEvent) (store.SaveResult, error) {
	f.events = append(f.events, event)
	return store.Inserted, nil
}

func (f *fakeWriter) DeleteHazard(_ context.Context, hazardID string) (bool, error) {
	f.deleted = append(f.deleted, hazardID)
	return f.existing[hazardID], nil
}

func testHandler(t *testing.T) (*http.ServeMux, *fakeWriter) {
	t.Helper()
	writer := &fakeWriter{existing: map[string]bool{}}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	mux := http.NewServeMux()
	RegisterHazards(mux, logger, writer, "coastal-token", "satellite-token")
	return mux, writer
}

func TestUpsertStoresEventAndComputesIDs(t *testing.T) {
	mux, writer := testHandler(t)
	body := `{
		"operation":"UPSERT","producer":"bnpb-satellite","producer_event_id":"satellite-fire-x-v1",
		"event":{"source":"SATELIT","source_ref_id":"fire-x","hazard_type":"WILDFIRE",
		"severity":"WASPADA","area_name":"Kalimantan Tengah","latitude":-2.5,"longitude":113.8,
		"occurred_at":"2026-11-12T02:43:00Z","attributes":{"detection_count":2}}
	}`
	request := httptest.NewRequest(http.MethodPost, "/internal/ingest/hazards", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer satellite-token")
	request.Header.Set("Idempotency-Key", "satellite-fire-x-v1")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if len(writer.events) != 1 {
		t.Fatalf("events = %d", len(writer.events))
	}
	event := writer.events[0]
	wantID := domain.NewHazardID(domain.SourceSatelit, "fire-x")
	if event.HazardID != wantID {
		t.Fatalf("hazard_id = %q, want %q", event.HazardID, wantID)
	}
	if event.IngestedAt.IsZero() {
		t.Fatal("ingested_at was not filled")
	}
}

func TestUpsertRejectsWrongToken(t *testing.T) {
	mux, _ := testHandler(t)
	request := httptest.NewRequest(http.MethodPost, "/internal/ingest/hazards", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer nope")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

func TestUpsertRejectsMismatchedIdempotencyKey(t *testing.T) {
	mux, _ := testHandler(t)
	body := `{"operation":"UPSERT","producer":"bnpb-satellite","producer_event_id":"a","event":{"source":"SATELIT","source_ref_id":"f"}}`
	request := httptest.NewRequest(http.MethodPost, "/internal/ingest/hazards", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer satellite-token")
	request.Header.Set("Idempotency-Key", "b")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	mux, writer := testHandler(t)
	body := `{"operation":"DELETE","producer":"bnpb-satellite","producer_event_id":"del-1","source":"SATELIT","source_ref_id":"fire-x"}`
	request := httptest.NewRequest(http.MethodPost, "/internal/ingest/hazards", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer satellite-token")
	request.Header.Set("Idempotency-Key", "del-1")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var payload responseBody
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Result != "not_found" {
		t.Fatalf("result = %q, want not_found", payload.Result)
	}
	if len(writer.deleted) != 1 || writer.deleted[0] != domain.NewHazardID(domain.SourceSatelit, "fire-x") {
		t.Fatalf("deleted = %v", writer.deleted)
	}
}

type responseBody struct {
	Result string `json:"result"`
}
