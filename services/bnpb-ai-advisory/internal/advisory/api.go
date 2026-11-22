package advisory

import (
	"net/http"
	"strconv"

	"github.com/angkaberapa/AAT/services/bnpb-ai-advisory/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-ai-advisory/internal/summarizer"
)

// API exposes derived advisories and an optional LLM situation summary over
// HTTP. Both endpoints are read-only and separate from /hazards, so a slow or
// dead model cannot affect the alert path.
type API struct {
	store      *Store
	threshold  float64
	recent     *RecentEvents
	summary    *summarizer.Summarizer
	summaryMax int
}

func NewAPI(store *Store, threshold float64, recent *RecentEvents, summary *summarizer.Summarizer, summaryMax int) *API {
	if summaryMax <= 0 {
		summaryMax = 20
	}
	return &API{
		store:      store,
		threshold:  threshold,
		recent:     recent,
		summary:    summary,
		summaryMax: summaryMax,
	}
}

func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /advisories", func(w http.ResponseWriter, r *http.Request) {
		limit := 20
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				core.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be a positive integer"})
				return
			}
			limit = n
		}
		items := a.store.List(limit)
		core.WriteJSON(w, http.StatusOK, map[string]any{
			"data": items,
			"meta": map[string]any{
				"count":     len(items),
				"method":    "online_gaussian",
				"threshold": a.threshold,
				"kind":      "derived_advisory",
			},
		})
	})

	mux.HandleFunc("GET /summary", func(w http.ResponseWriter, r *http.Request) {
		recent := a.recent.List(a.summaryMax)
		events := make([]summarizer.Event, 0, len(recent))
		for _, event := range recent {
			events = append(events, summarizer.Event{
				Source:     event.Source,
				HazardType: event.HazardType,
				Severity:   event.Severity,
				AreaName:   event.AreaName,
				OccurredAt: event.OccurredAt,
			})
		}
		advisories := a.store.List(5)
		result := a.summary.Summarize(r.Context(), events, advisories)
		core.WriteJSON(w, http.StatusOK, map[string]any{
			"data": result,
			"meta": map[string]any{
				"event_count":    len(events),
				"advisory_count": len(advisories),
				"kind":           "derived_summary",
			},
		})
	})
}
