package reference

import "testing"

func TestNearestProvince(t *testing.T) {
	cases := []struct {
		name      string
		latitude  float64
		longitude float64
		expected  string
	}{
		{name: "padang", latitude: -0.95, longitude: 100.35, expected: "Sumatera Barat"},
		{name: "kalimantan tengah", latitude: -2.5, longitude: 113.8, expected: "Kalimantan Tengah"},
		{name: "papua", latitude: -4.0, longitude: 138.0, expected: "Papua"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			province, err := Nearest(tc.latitude, tc.longitude)
			if err != nil {
				t.Fatalf("Nearest: %v", err)
			}
			if province.Name != tc.expected {
				t.Fatalf("province = %q, want %q", province.Name, tc.expected)
			}
		})
	}
}
