package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/mapping"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/p1tool"
)

func main() {
	file := flag.String("file", "", "historical catalogue (.json or .csv)")
	endpoint := flag.String("url", "http://localhost:18000/internal/catalog/seismic-events", "Aggregator seismic catalogue endpoint")
	token := flag.String("token", os.Getenv("AUDIT_TOKEN"), "audit token (defaults to AUDIT_TOKEN)")
	batchSize := flag.Int("batch-size", 250, "events per transaction (1-500)")
	timeout := flag.Duration("timeout", 60*time.Second, "HTTP timeout per batch")
	flag.Parse()

	if *file == "" || *batchSize < 1 || *batchSize > 500 {
		fatal(errors.New("-file is required and -batch-size must be between 1 and 500"))
	}
	events, err := loadCatalogue(*file)
	if err != nil {
		fatal(err)
	}
	client, err := p1tool.NewClient(*endpoint, *token, *timeout)
	if err != nil {
		fatal(err)
	}

	total := p1tool.Result{}
	for start := 0; start < len(events); start += *batchSize {
		end := min(start+*batchSize, len(events))
		result, err := client.Import(context.Background(), events[start:end])
		if err != nil {
			fatal(fmt.Errorf("batch %d-%d: %w", start, end-1, err))
		}
		total.Received += result.Received
		total.Inserted += result.Inserted
		total.Updated += result.Updated
		total.Unchanged += result.Unchanged
		fmt.Fprintf(os.Stderr, "imported %d/%d records\n", end, len(events))
	}
	_ = json.NewEncoder(os.Stdout).Encode(total)
}

func loadCatalogue(path string) ([]mapping.SeismicEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open catalogue: %w", err)
	}
	defer file.Close()
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return loadJSON(file)
	case ".csv":
		return loadCSV(file)
	default:
		return nil, errors.New("catalogue extension must be .json or .csv")
	}
}

func loadJSON(reader io.Reader) ([]mapping.SeismicEvent, error) {
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	var records []map[string]any
	if err := json.Unmarshal(raw, &records); err != nil {
		var envelope struct {
			Events []map[string]any `json:"events"`
		}
		if envelopeErr := json.Unmarshal(raw, &envelope); envelopeErr != nil {
			return nil, fmt.Errorf("decode JSON catalogue: %w", err)
		}
		records = envelope.Events
	}
	return normalizeRecords(records)
}

func loadCSV(reader io.Reader) ([]mapping.SeismicEvent, error) {
	csvReader := csv.NewReader(reader)
	csvReader.TrimLeadingSpace = true
	headers, err := csvReader.Read()
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	for index := range headers {
		headers[index] = normalizeKey(headers[index])
	}
	var records []map[string]any
	for rowNumber := 2; ; rowNumber++ {
		row, err := csvReader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read CSV row %d: %w", rowNumber, err)
		}
		if len(row) != len(headers) {
			return nil, fmt.Errorf("CSV row %d has %d columns, expected %d", rowNumber, len(row), len(headers))
		}
		record := make(map[string]any, len(headers))
		for index, header := range headers {
			record[header] = strings.TrimSpace(row[index])
		}
		records = append(records, record)
	}
	return normalizeRecords(records)
}

func normalizeRecords(records []map[string]any) ([]mapping.SeismicEvent, error) {
	if len(records) == 0 {
		return nil, errors.New("catalogue contains no records")
	}
	events := make([]mapping.SeismicEvent, 0, len(records))
	for index, record := range records {
		event, err := normalizeRecord(record)
		if err != nil {
			return nil, fmt.Errorf("record %d: %w", index+1, err)
		}
		events = append(events, event)
	}
	return events, nil
}

func normalizeRecord(record map[string]any) (mapping.SeismicEvent, error) {
	normalized := make(map[string]any, len(record))
	for key, value := range record {
		normalized[normalizeKey(key)] = value
	}
	id := textValue(normalized, "event_id", "earthquake_id", "id", "identifier", "kode")
	region := textValue(normalized, "region_name", "area_name", "region", "place", "location", "wilayah")
	if id == "" || region == "" {
		return mapping.SeismicEvent{}, errors.New("event id and region/place are required")
	}
	magnitude, err := numberValue(normalized, "magnitude", "mag")
	if err != nil {
		return mapping.SeismicEvent{}, err
	}
	depth, err := numberValue(normalized, "depth_km", "depth", "kedalaman")
	if err != nil {
		return mapping.SeismicEvent{}, err
	}
	latitude, err := numberValue(normalized, "epicenter_lat", "latitude", "lat", "lintang")
	if err != nil {
		return mapping.SeismicEvent{}, err
	}
	longitude, err := numberValue(normalized, "epicenter_lon", "longitude", "lon", "lng", "bujur")
	if err != nil {
		return mapping.SeismicEvent{}, err
	}
	occurred, err := timeValue(normalized, "occurred_at", "origin_time", "datetime", "date_time", "timestamp", "time")
	if err != nil {
		return mapping.SeismicEvent{}, err
	}
	potential := boolValue(normalized, "potential_tsunami", "tsunami")
	return mapping.SeismicEvent{
		EventID: id, Magnitude: magnitude, DepthKM: depth,
		EpicenterLat: latitude, EpicenterLon: longitude, RegionName: region,
		OccurredAt: occurred, PotentialTsunami: potential, AdditionalFields: map[string]any{"catalogue_import": true},
	}, nil
}

func normalizeKey(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", "_"))
}

func firstValue(record map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := record[key]; ok && fmt.Sprint(value) != "" {
			return value, true
		}
	}
	return nil, false
}

func textValue(record map[string]any, keys ...string) string {
	value, _ := firstValue(record, keys...)
	return strings.TrimSpace(fmt.Sprint(value))
}

func numberValue(record map[string]any, keys ...string) (float64, error) {
	value, ok := firstValue(record, keys...)
	if !ok {
		return 0, fmt.Errorf("%s is required", keys[0])
	}
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case json.Number:
		return typed.Float64()
	default:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(value)), 64)
		if err != nil {
			return 0, fmt.Errorf("%s must be numeric", keys[0])
		}
		return parsed, nil
	}
}

func timeValue(record map[string]any, keys ...string) (time.Time, error) {
	value, ok := firstValue(record, keys...)
	if !ok {
		return time.Time{}, fmt.Errorf("%s is required", keys[0])
	}
	raw := strings.TrimSpace(fmt.Sprint(value))
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006/01/02 15:04:05", "2006-01-02T15:04:05"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%s must be RFC3339 or YYYY-MM-DD HH:MM:SS", keys[0])
}

func boolValue(record map[string]any, keys ...string) bool {
	value, ok := firstValue(record, keys...)
	if !ok {
		return false
	}
	parsed, _ := strconv.ParseBool(strings.TrimSpace(fmt.Sprint(value)))
	return parsed
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
