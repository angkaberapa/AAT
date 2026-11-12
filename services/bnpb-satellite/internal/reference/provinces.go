package reference

import (
	"fmt"
	"math"
)

// Province is a static centroid used to label a wildfire with the nearest
// province. The table is deliberately local to the service; replace it with the
// official BNPB reference when available.
type Province struct {
	Name      string
	Latitude  float64
	Longitude float64
}

var provinces = []Province{
	{Name: "Aceh", Latitude: 5.5483, Longitude: 95.3238},
	{Name: "Sumatera Utara", Latitude: 2.0000, Longitude: 99.0000},
	{Name: "Sumatera Barat", Latitude: -0.9471, Longitude: 100.4172},
	{Name: "Riau", Latitude: 0.5000, Longitude: 101.5000},
	{Name: "Jambi", Latitude: -1.6000, Longitude: 103.6000},
	{Name: "Sumatera Selatan", Latitude: -3.0000, Longitude: 104.0000},
	{Name: "Bengkulu", Latitude: -3.8000, Longitude: 102.3000},
	{Name: "Lampung", Latitude: -4.5000, Longitude: 105.0000},
	{Name: "Kepulauan Bangka Belitung", Latitude: -2.1000, Longitude: 106.1000},
	{Name: "Kepulauan Riau", Latitude: 0.9000, Longitude: 104.5000},
	{Name: "DKI Jakarta", Latitude: -6.2000, Longitude: 106.8000},
	{Name: "Jawa Barat", Latitude: -6.9000, Longitude: 107.6000},
	{Name: "Jawa Tengah", Latitude: -7.0000, Longitude: 110.0000},
	{Name: "DI Yogyakarta", Latitude: -7.8000, Longitude: 110.4000},
	{Name: "Jawa Timur", Latitude: -7.5000, Longitude: 112.5000},
	{Name: "Banten", Latitude: -6.5000, Longitude: 106.2000},
	{Name: "Bali", Latitude: -8.4000, Longitude: 115.2000},
	{Name: "Nusa Tenggara Barat", Latitude: -8.6000, Longitude: 116.5000},
	{Name: "Nusa Tenggara Timur", Latitude: -10.2000, Longitude: 123.6000},
	{Name: "Kalimantan Barat", Latitude: -0.5000, Longitude: 109.5000},
	{Name: "Kalimantan Tengah", Latitude: -2.0000, Longitude: 113.5000},
	{Name: "Kalimantan Selatan", Latitude: -3.0000, Longitude: 115.0000},
	{Name: "Kalimantan Timur", Latitude: 0.5000, Longitude: 116.5000},
	{Name: "Kalimantan Utara", Latitude: 3.0000, Longitude: 116.5000},
	{Name: "Sulawesi Utara", Latitude: 1.5000, Longitude: 124.8000},
	{Name: "Sulawesi Tengah", Latitude: -1.0000, Longitude: 120.0000},
	{Name: "Sulawesi Selatan", Latitude: -4.0000, Longitude: 120.0000},
	{Name: "Sulawesi Tenggara", Latitude: -4.0000, Longitude: 122.0000},
	{Name: "Gorontalo", Latitude: 0.5000, Longitude: 122.0000},
	{Name: "Sulawesi Barat", Latitude: -2.5000, Longitude: 119.0000},
	{Name: "Maluku", Latitude: -3.0000, Longitude: 129.0000},
	{Name: "Maluku Utara", Latitude: 0.5000, Longitude: 127.5000},
	{Name: "Papua", Latitude: -4.0000, Longitude: 138.0000},
	{Name: "Papua Barat", Latitude: -1.5000, Longitude: 133.0000},
	{Name: "Papua Selatan", Latitude: -8.0000, Longitude: 139.0000},
	{Name: "Papua Tengah", Latitude: -3.5000, Longitude: 136.0000},
	{Name: "Papua Pegunungan", Latitude: -4.0000, Longitude: 138.5000},
	{Name: "Papua Barat Daya", Latitude: -0.5000, Longitude: 132.0000},
}

func Nearest(latitude, longitude float64) (Province, error) {
	if len(provinces) == 0 {
		return Province{}, fmt.Errorf("province reference table is empty")
	}
	best := provinces[0]
	bestDistance := haversineKM(latitude, longitude, best.Latitude, best.Longitude)
	for _, province := range provinces[1:] {
		distance := haversineKM(latitude, longitude, province.Latitude, province.Longitude)
		if distance < bestDistance {
			best, bestDistance = province, distance
		}
	}
	return best, nil
}

func haversineKM(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKM = 6371.0088
	phi1 := lat1 * math.Pi / 180
	phi2 := lat2 * math.Pi / 180
	dPhi := (lat2 - lat1) * math.Pi / 180
	dLambda := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dPhi/2)*math.Sin(dPhi/2) +
		math.Cos(phi1)*math.Cos(phi2)*math.Sin(dLambda/2)*math.Sin(dLambda/2)
	return 2 * earthRadiusKM * math.Asin(math.Sqrt(a))
}
