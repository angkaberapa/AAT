package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/mapping"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/p1tool"
)

func main() {
	endpoint := flag.String("url", "http://localhost:18000/internal/catalog/seismic-events", "Aggregator seismic catalogue endpoint")
	token := flag.String("token", os.Getenv("AUDIT_TOKEN"), "audit token (defaults to AUDIT_TOKEN)")
	count := flag.Int("count", 10000, "number of earthquakes")
	workers := flag.Int("workers", 8, "concurrent batch workers")
	batchSize := flag.Int("batch-size", 100, "events per request (1-500)")
	region := flag.String("region", "P1 Burst Region", "region_name for every generated event")
	latitude := flag.Float64("latitude", -6.2, "burst epicenter latitude")
	longitude := flag.Float64("longitude", 106.8, "burst epicenter longitude")
	magnitude := flag.Float64("magnitude", 6.7, "generated magnitude")
	depth := flag.Float64("depth-km", 10, "generated depth")
	runID := flag.String("run-id", time.Now().UTC().Format("20060102T150405.000000000"), "unique run identifier")
	flag.Parse()
	if *count < 1 || *workers < 1 || *batchSize < 1 || *batchSize > 500 {
		fatal(errors.New("count/workers must be positive and batch-size must be between 1 and 500"))
	}
	client, err := p1tool.NewClient(*endpoint, *token, 90*time.Second)
	if err != nil {
		fatal(err)
	}

	started := time.Now()
	jobs := make(chan []mapping.SeismicEvent)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var inserted, updated, unchanged atomic.Int64
	var firstErr error
	var errOnce sync.Once
	var group sync.WaitGroup
	for range *workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for batch := range jobs {
				result, err := client.Import(ctx, batch)
				if err != nil {
					errOnce.Do(func() { firstErr = err; cancel() })
					continue
				}
				inserted.Add(int64(result.Inserted))
				updated.Add(int64(result.Updated))
				unchanged.Add(int64(result.Unchanged))
			}
		}()
	}
	now := time.Now().UTC()
sendLoop:
	for start := 0; start < *count; start += *batchSize {
		end := min(start+*batchSize, *count)
		batch := make([]mapping.SeismicEvent, 0, end-start)
		for index := start; index < end; index++ {
			batch = append(batch, mapping.SeismicEvent{
				EventID:   fmt.Sprintf("P1-BURST-%s-%08d", *runID, index),
				Magnitude: *magnitude, DepthKM: *depth,
				EpicenterLat: *latitude, EpicenterLon: *longitude, RegionName: *region,
				OccurredAt:       now.Add(time.Duration(index) * time.Millisecond),
				AdditionalFields: map[string]any{"p1_burst_run": *runID, "p1_burst_index": index},
			})
		}
		select {
		case jobs <- batch:
		case <-ctx.Done():
			break sendLoop
		}
	}
	close(jobs)
	group.Wait()
	if firstErr != nil {
		fatal(firstErr)
	}
	result := map[string]any{
		"run_id": *runID, "requested": *count,
		"inserted": inserted.Load(), "updated": updated.Load(), "unchanged": unchanged.Load(),
		"duration_seconds": time.Since(started).Seconds(),
	}
	_ = json.NewEncoder(os.Stdout).Encode(result)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
