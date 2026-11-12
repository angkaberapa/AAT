package wildfire

import (
	"sort"

	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/domain"
)

// Reconcile compares the fires formed by the current active detections with
// the fires already stored. It returns every current fire as an upsert and the
// existing fire IDs that no longer exist as deletes.
//
// It intentionally recomputes from the full active set. Retract events are rare
// compared with detections, and a full recompute is easier to reason about than
// incremental component splitting.
func Reconcile(active []domain.Detection, existingIDs []string) ([]Fire, []string) {
	current := Cluster(active)
	currentIDs := make(map[string]struct{}, len(current.Fires))
	for _, fire := range current.Fires {
		currentIDs[fire.ID] = struct{}{}
	}

	deletes := []string{}
	for _, id := range existingIDs {
		if _, exists := currentIDs[id]; !exists {
			deletes = append(deletes, id)
		}
	}
	sort.Strings(deletes)
	return current.Fires, deletes
}
