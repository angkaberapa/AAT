// Command fake-aggregator is a local stand-in for the Aggregator Internal
// Ingest API. It implements the same envelope and idempotency rules so P4/P5
// services can be developed before the real endpoint exists.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/ingest"
)

const maxBodyBytes = 1 << 20

type storedResult struct {
	PayloadHash string
	Result      ingest.IngestResult
}

type server struct {
	mu          sync.Mutex
	idempotency map[string]storedResult
	events      map[string]ingest.HazardEvent
	hashes      map[string]string
	versions    map[string]int
	statuses    map[string]ingest.SourceStatus
	unprocessed map[string]ingest.UnprocessedMessage
}

func newServer() *server {
	return &server{
		idempotency: make(map[string]storedResult),
		events:      make(map[string]ingest.HazardEvent),
		hashes:      make(map[string]string),
		versions:    make(map[string]int),
		statuses:    make(map[string]ingest.SourceStatus),
		unprocessed: make(map[string]ingest.UnprocessedMessage),
	}
}

type commandEnvelope struct {
	Operation       string              `json:"operation"`
	Producer        string              `json:"producer"`
	ProducerEventID string              `json:"producer_event_id"`
	Event           *ingest.HazardEvent `json:"event"`
	HazardID        string              `json:"hazard_id"`
	Source          string              `json:"source"`
	SourceRefID     string              `json:"source_ref_id"`
}

func main() {
	addr := flag.String("addr", ":18081", "listen address")
	flag.Parse()

	mux := http.NewServeMux()
	s := newServer()
	mux.HandleFunc("POST /internal/ingest/hazards", s.handleHazards)
	mux.HandleFunc("POST /internal/ingest/source-status", s.handleSourceStatus)
	mux.HandleFunc("POST /internal/ingest/unprocessed", s.handleUnprocessed)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /debug/state", s.handleState)

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("fake aggregator listening on %s", *addr)
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func (s *server) handleHazards(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "cannot read body")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Idempotency-Key is required")
		return
	}

	var command commandEnvelope
	if err := json.Unmarshal(body, &command); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if command.ProducerEventID == "" || command.ProducerEventID != key {
		writeError(w, http.StatusBadRequest, "bad_request", "Idempotency-Key must equal producer_event_id")
		return
	}
	if command.Producer == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "producer is required")
		return
	}
	if !validCommand(command) {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "command fields are incomplete")
		return
	}

	canonical, err := json.Marshal(command)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	hash := sha256.Sum256(canonical)
	payloadHash := hex.EncodeToString(hash[:])

	s.mu.Lock()
	if previous, ok := s.idempotency[key]; ok {
		if previous.PayloadHash != payloadHash {
			s.mu.Unlock()
			writeError(w, http.StatusConflict, "conflict", "Idempotency-Key reused with a different payload")
			return
		}
		result := previous.Result
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, result)
		return
	}

	var result ingest.IngestResult
	switch command.Operation {
	case ingest.OperationUpsert:
		result = s.applyUpsert(command)
	case ingest.OperationDelete:
		result = s.applyDelete(command)
	default:
		s.mu.Unlock()
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "operation must be UPSERT or DELETE")
		return
	}
	result.ProducerEventID = key
	s.idempotency[key] = storedResult{PayloadHash: payloadHash, Result: result}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, result)
}

func validCommand(command commandEnvelope) bool {
	switch command.Operation {
	case ingest.OperationUpsert:
		return command.Event != nil &&
			command.Event.HazardID != "" &&
			command.Event.Source != "" &&
			command.Event.SourceRefID != ""
	case ingest.OperationDelete:
		return command.HazardID != "" && command.Source != "" && command.SourceRefID != ""
	default:
		return false
	}
}

func (s *server) applyUpsert(command commandEnvelope) ingest.IngestResult {
	event := *command.Event
	raw, _ := json.Marshal(event)
	hash := sha256.Sum256(raw)
	contentHash := hex.EncodeToString(hash[:])
	eventKey := event.Source + "/" + event.SourceRefID

	result := "inserted"
	if previousHash, exists := s.hashes[eventKey]; exists {
		if previousHash == contentHash {
			result = "unchanged"
		} else {
			result = "updated"
		}
	}
	if result != "unchanged" {
		s.events[eventKey] = event
		s.hashes[eventKey] = contentHash
		s.versions[eventKey]++
	}
	return ingest.IngestResult{
		HazardID: event.HazardID,
		Result:   result,
		Version:  s.versions[eventKey],
	}
}

func (s *server) applyDelete(command commandEnvelope) ingest.IngestResult {
	eventKey := command.Source + "/" + command.SourceRefID
	if command.HazardID == "" || command.Source == "" || command.SourceRefID == "" {
		return ingest.IngestResult{Result: "validation_failed"}
	}
	if _, exists := s.events[eventKey]; !exists {
		return ingest.IngestResult{HazardID: command.HazardID, Result: "already_applied", Version: s.versions[eventKey]}
	}
	delete(s.events, eventKey)
	delete(s.hashes, eventKey)
	s.versions[eventKey]++
	return ingest.IngestResult{HazardID: command.HazardID, Result: "deleted", Version: s.versions[eventKey]}
}

func (s *server) handleSourceStatus(w http.ResponseWriter, r *http.Request) {
	var status ingest.SourceStatus
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&status); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if status.Source == "" || status.Status == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "source and status are required")
		return
	}
	s.mu.Lock()
	s.statuses[status.Source] = status
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleUnprocessed(w http.ResponseWriter, r *http.Request) {
	var message ingest.UnprocessedMessage
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&message); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if message.MessageID == "" || message.Source == "" || message.Status == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "message_id, source, and status are required")
		return
	}
	s.mu.Lock()
	s.unprocessed[message.MessageID] = message
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"events":      s.events,
		"versions":    s.versions,
		"statuses":    s.statuses,
		"unprocessed": s.unprocessed,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func init() {
	log.SetOutput(os.Stdout)
}
