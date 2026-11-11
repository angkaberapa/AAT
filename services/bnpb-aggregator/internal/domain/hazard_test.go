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
func TestM2EnumsAreValidAndMapped(t *testing.T) {
	t.Parallel()

	cases := map[HazardType]Source{
		HazardTypeSeismic:  SourceBMKG,
		HazardTypeVolcanic: SourcePVMBG,
		HazardTypeWildfire: SourceSatelit,
		HazardTypeCoastal:  SourceSensorPantai,
	}
	for hazardType, source := range cases {
		if !hazardType.Valid() || !source.Valid() {
			t.Fatalf("%s/%s should be valid", hazardType, source)
		}
		if got := hazardType.Source(); got != source {
			t.Fatalf("%s.Source() = %s, want %s", hazardType, got, source)
		}
	}
	if HazardType("FLOOD").Valid() || Source("BPBD").Valid() {
		t.Fatal("unknown values must be invalid")
	}
}
