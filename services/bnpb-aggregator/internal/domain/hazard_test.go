package domain

import "testing"

func TestNewHazardIDIsStableAndSourceScoped(t *testing.T) {
	t.Parallel()

	first := NewHazardID(SourcePVMBG, "shared-id")
	second := NewHazardID(SourcePVMBG, "shared-id")
	bmkg := NewHazardID(SourceBMKG, "shared-id")

	if first != second {
		t.Fatalf("same source record produced different IDs: %q and %q", first, second)
	}
	if first == bmkg {
		t.Fatalf("different sources produced the same ID %q", first)
	}
}
