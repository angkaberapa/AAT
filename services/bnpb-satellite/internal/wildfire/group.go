// Package wildfire turns satellite detections into connected fires using
// event time and a 1 km distance threshold.
package wildfire

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/domain"
)

const (
	maxDistanceKM = 1.0
	maxTimeGap    = 360 * time.Minute
	cellSize      = 0.01 // degrees, about 1.1 km at the equator
	cellRadius    = 2
)

type Fire struct {
	ID              string
	Detections      []domain.Detection
	DetectionKeys   []string
	Satellites      []string
	DetectionCount  int
	TotalFRP        float64
	MaxConfidence   string
	FirstObservedAt time.Time
	LastObservedAt  time.Time
	Latitude        float64
	Longitude       float64
}

type Result struct {
	Fires   []Fire
	Singles []domain.Detection
}

// Cluster groups detections that are within 1 km and 360 minutes of each
// other, including chained connections. The output is deterministic: fires
// are sorted by their stable ID and detections by detection key.
func Cluster(detections []domain.Detection) Result {
	items := append([]domain.Detection(nil), detections...)
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })

	parent := make([]int, len(items))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}

	type point struct {
		lat, lon float64
		ok       bool
	}
	points := make([]point, len(items))
	grid := make(map[[2]int][]int)
	for i, detection := range items {
		lat, latOK := parseCoordinate(detection.Latitude)
		lon, lonOK := parseCoordinate(detection.Longitude)
		points[i] = point{lat: lat, lon: lon, ok: latOK && lonOK}
		if points[i].ok {
			cell := [2]int{int(math.Floor(lat / cellSize)), int(math.Floor(lon / cellSize))}
			grid[cell] = append(grid[cell], i)
		}
	}

	for i := range items {
		if !points[i].ok {
			continue
		}
		cell := [2]int{int(math.Floor(points[i].lat / cellSize)), int(math.Floor(points[i].lon / cellSize))}
		for dLat := -cellRadius; dLat <= cellRadius; dLat++ {
			for dLon := -cellRadius; dLon <= cellRadius; dLon++ {
				for _, j := range grid[[2]int{cell[0] + dLat, cell[1] + dLon}] {
					if j == i || !connected(items[i], items[j], points[i].lat, points[i].lon, points[j].lat, points[j].lon) {
						continue
					}
					union(i, j)
				}
			}
		}
	}

	groups := make(map[int][]domain.Detection)
	for i, detection := range items {
		root := find(i)
		groups[root] = append(groups[root], detection)
	}

	result := Result{Fires: []Fire{}, Singles: []domain.Detection{}}
	for _, group := range groups {
		if len(group) < 2 {
			result.Singles = append(result.Singles, group[0])
			continue
		}
		result.Fires = append(result.Fires, buildFire(group))
	}
	sort.Slice(result.Fires, func(i, j int) bool { return result.Fires[i].ID < result.Fires[j].ID })
	sort.Slice(result.Singles, func(i, j int) bool { return result.Singles[i].Key < result.Singles[j].Key })
	return result
}

func connected(a, b domain.Detection, aLat, aLon, bLat, bLon float64) bool {
	diff := a.ObservedAt.Sub(b.ObservedAt)
	if diff < 0 {
		diff = -diff
	}
	if diff > maxTimeGap {
		return false
	}
	return haversineKM(aLat, aLon, bLat, bLon) <= maxDistanceKM
}

func buildFire(detections []domain.Detection) Fire {
	sort.Slice(detections, func(i, j int) bool { return detections[i].Key < detections[j].Key })
	fire := Fire{
		ID:              FireID(detections[0].Key),
		Detections:      detections,
		DetectionCount:  len(detections),
		FirstObservedAt: detections[0].ObservedAt,
		LastObservedAt:  detections[0].ObservedAt,
	}

	satellites := map[string]struct{}{}
	var latSum, lonSum float64
	var points int
	for _, detection := range detections {
		fire.DetectionKeys = append(fire.DetectionKeys, detection.Key)
		satellites[detection.Satellite] = struct{}{}
		if detection.FRP != nil {
			fire.TotalFRP += *detection.FRP
		}
		if confidenceRank(detection.Confidence) > confidenceRank(fire.MaxConfidence) {
			fire.MaxConfidence = detection.Confidence
		}
		if detection.ObservedAt.Before(fire.FirstObservedAt) {
			fire.FirstObservedAt = detection.ObservedAt
		}
		if detection.ObservedAt.After(fire.LastObservedAt) {
			fire.LastObservedAt = detection.ObservedAt
		}
		lat, latOK := parseCoordinate(detection.Latitude)
		lon, lonOK := parseCoordinate(detection.Longitude)
		if latOK && lonOK {
			latSum += lat
			lonSum += lon
			points++
		}
	}
	for satellite := range satellites {
		fire.Satellites = append(fire.Satellites, satellite)
	}
	sort.Strings(fire.Satellites)
	if points > 0 {
		fire.Latitude = latSum / float64(points)
		fire.Longitude = lonSum / float64(points)
	}
	return fire
}

// FireID must be stable across replays. It is derived from the smallest
// detection key in the connected component.
func FireID(minDetectionKey string) string {
	sum := sha256.Sum256([]byte(minDetectionKey))
	return "fire-" + hex.EncodeToString(sum[:8])
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

func parseCoordinate(value string) (float64, bool) {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func confidenceRank(value string) int {
	switch value {
	case "h", "high":
		return 3
	case "n", "nominal":
		return 2
	case "l", "low":
		return 1
	default:
		return 0
	}
}
