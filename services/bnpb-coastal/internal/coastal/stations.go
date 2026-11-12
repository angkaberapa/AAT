package coastal

// Station is the static reference needed to turn a sensor reading into a
// canonical HazardEvent. The sensor payload carries station_id but no
// coordinates, so this table supplies location and province.
type Station struct {
	ID        string
	Name      string
	Province  string
	Latitude  float64
	Longitude float64
}

// Provisional station table. Replace with the official Sensor Pantai contract
// when it is published. Unknown stations are quarantined, not guessed.
var stations = map[string]Station{
	"stn-padang-01":         {ID: "stn-padang-01", Name: "Padang", Province: "Sumatera Barat", Latitude: -0.9471, Longitude: 100.4172},
	"stn-banda-aceh-01":     {ID: "stn-banda-aceh-01", Name: "Banda Aceh", Province: "Aceh", Latitude: 5.5483, Longitude: 95.3238},
	"stn-cilacap-01":        {ID: "stn-cilacap-01", Name: "Cilacap", Province: "Jawa Tengah", Latitude: -7.7267, Longitude: 109.0090},
	"stn-pelabuhan-ratu-01": {ID: "stn-pelabuhan-ratu-01", Name: "Pelabuhan Ratu", Province: "Jawa Barat", Latitude: -6.9850, Longitude: 106.5470},
	"stn-palu-01":           {ID: "stn-palu-01", Name: "Palu", Province: "Sulawesi Tengah", Latitude: -0.8917, Longitude: 119.8707},
	"stn-kupang-01":         {ID: "stn-kupang-01", Name: "Kupang", Province: "Nusa Tenggara Timur", Latitude: -10.1772, Longitude: 123.6070},
	"stn-manado-01":         {ID: "stn-manado-01", Name: "Manado", Province: "Sulawesi Utara", Latitude: 1.4748, Longitude: 124.8421},
}

func LookupStation(id string) (Station, bool) {
	station, exists := stations[id]
	return station, exists
}
