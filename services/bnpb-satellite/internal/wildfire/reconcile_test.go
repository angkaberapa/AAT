package wildfire

import (
	"testing"

	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/domain"
)

func TestReconcileDeletesMissingFire(t *testing.T) {
	active := []domain.Detection{
		detection("A", "N20", 0, 0, baseTime, 10, "h"),
		detection("B", "N20", 0, 0.005, baseTime, 10, "h"),
	}
	fires, deletes := Reconcile(active, []string{"fire-old"})
	if len(fires) != 1 {
		t.Fatalf("fires = %+v", fires)
	}
	if len(deletes) != 1 || deletes[0] != "fire-old" {
		t.Fatalf("deletes = %+v", deletes)
	}
}

func TestReconcileKeepsExistingFire(t *testing.T) {
	active := []domain.Detection{
		detection("A", "N20", 0, 0, baseTime, 10, "h"),
		detection("B", "N20", 0, 0.005, baseTime, 10, "h"),
	}
	fires, _ := Reconcile(active, nil)
	fireID := fires[0].ID
	_, deletes := Reconcile(active, []string{fireID})
	if len(deletes) != 0 {
		t.Fatalf("deletes = %+v", deletes)
	}
}

func TestReconcileDeletesAfterSplit(t *testing.T) {
	active := []domain.Detection{
		detection("A", "N20", 0, 0, baseTime, 10, "h"),
		detection("B", "N20", 0, 0.008, baseTime, 10, "h"),
		detection("C", "N20", 0, 0.016, baseTime, 10, "h"),
	}
	fires, _ := Reconcile(active, nil)
	if len(fires) != 1 {
		t.Fatalf("fires = %+v", fires)
	}
	// B is retracted, so A and C are no longer connected and both become singles.
	remaining := []domain.Detection{active[0], active[2]}
	_, deletes := Reconcile(remaining, []string{fires[0].ID})
	if len(deletes) != 1 || deletes[0] != fires[0].ID {
		t.Fatalf("deletes = %+v", deletes)
	}
}

func TestReconcileNoActiveDetections(t *testing.T) {
	fires, deletes := Reconcile(nil, []string{"fire-a", "fire-b"})
	if len(fires) != 0 {
		t.Fatalf("fires = %+v", fires)
	}
	if len(deletes) != 2 || deletes[0] != "fire-a" || deletes[1] != "fire-b" {
		t.Fatalf("deletes = %+v", deletes)
	}
}
