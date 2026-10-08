// Package notify alerts the field team when a hazard reaches SIAGA or AWAS
// (P5 consumer #2). Sending is simulated: a notification is a structured log line
// plus an entry in GET /notifications.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-consumer-notifier/internal/consumer"
	"github.com/angkaberapa/AAT/services/bnpb-consumer-notifier/internal/core"
)

const keepRecent = 50

var severityRank = map[string]int{"NORMAL": 1, "WASPADA": 2, "SIAGA": 3, "AWAS": 4}

// Notification is one alert sent to the field team.
type Notification struct {
	EventID    string    `json:"event_id"`
	HazardID   string    `json:"hazard_id"`
	HazardType string    `json:"hazard_type"`
	Severity   string    `json:"severity"`
	AreaName   string    `json:"area_name"`
	Message    string    `json:"message"`
	SentAt     time.Time `json:"sent_at"`
}

type hazardState struct {
	Version          int    `json:"version"`
	NotifiedSeverity string `json:"notified_severity,omitempty"`
}

type state struct {
	Hazards    map[string]hazardState `json:"hazards"`
	Recent     []Notification         `json:"recent"` // newest first
	Processed  int                    `json:"processed"`
	Sent       int                    `json:"notifications_sent"`
	Duplicates int                    `json:"duplicates_ignored"`
}

type Notifier struct {
	mu     sync.RWMutex
	state  state
	file   consumer.FileState
	logger *slog.Logger
	now    func() time.Time
}

func New(file consumer.FileState, logger *slog.Logger) (*Notifier, error) {
	n := &Notifier{state: state{Hazards: map[string]hazardState{}}, file: file, logger: logger, now: time.Now}
	if err := file.Load(&n.state); err != nil {
		return nil, err
	}
	if n.state.Hazards == nil {
		n.state.Hazards = map[string]hazardState{}
	}
	return n, nil
}

// Handle notifies when severity is at least SIAGA and higher than what was already
// notified for this hazard (so SIAGA -> AWAS alerts again). Versions that are not
// newer than the stored one are redeliveries and never alert twice.
func (n *Notifier) Handle(ctx context.Context, msg consumer.Message) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	h := msg.Hazard
	prev := n.state.Hazards[h.HazardID]
	if msg.Version <= prev.Version {
		n.state.Duplicates++
		n.logger.DebugContext(ctx, "duplicate ignored", "event_id", msg.EventID, "stored_version", prev.Version)
		return nil
	}
	n.state.Processed++
	next := hazardState{Version: msg.Version, NotifiedSeverity: prev.NotifiedSeverity}

	if severityRank[h.Severity] >= severityRank["SIAGA"] && severityRank[h.Severity] > severityRank[prev.NotifiedSeverity] {
		note := Notification{
			EventID:    msg.EventID,
			HazardID:   h.HazardID,
			HazardType: h.HazardType,
			Severity:   h.Severity,
			AreaName:   h.AreaName,
			Message:    fmt.Sprintf("[%s] %s di %s", h.Severity, h.HazardType, h.AreaName),
			SentAt:     n.now().UTC(),
		}
		n.state.Recent = append([]Notification{note}, n.state.Recent...)
		if len(n.state.Recent) > keepRecent {
			n.state.Recent = n.state.Recent[:keepRecent]
		}
		n.state.Sent++
		next.NotifiedSeverity = h.Severity
		n.logger.InfoContext(ctx, "notification sent",
			"channel", "tim-lapangan", "event_id", msg.EventID,
			"severity", h.Severity, "hazard_type", h.HazardType, "area", h.AreaName)
	}
	n.state.Hazards[h.HazardID] = next
	return nil
}

func (n *Notifier) Flush(context.Context) error {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.file.Save(n.state)
}

func (n *Notifier) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /notifications", func(w http.ResponseWriter, _ *http.Request) {
		n.mu.RLock()
		defer n.mu.RUnlock()
		core.WriteJSON(w, http.StatusOK, map[string]any{
			"recent": n.state.Recent,
			"stats": map[string]int{
				"processed":          n.state.Processed,
				"notifications_sent": n.state.Sent,
				"duplicates_ignored": n.state.Duplicates,
			},
		})
	})
}
