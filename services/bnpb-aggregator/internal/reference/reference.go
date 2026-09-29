// Package reference holds BNPB's static volcano table (volcano_id -> name and
// coordinates), embedded into the binary. The IDs must match what pvmbg-mock emits.
package reference

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

type Volcano struct {
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

//go:embed volcanoes.json
var volcanoesJSON []byte

// Volcanoes parses the embedded table.
func Volcanoes() (map[string]Volcano, error) {
	var doc struct {
		Volcanoes map[string]Volcano `json:"volcanoes"`
	}
	if err := json.Unmarshal(volcanoesJSON, &doc); err != nil {
		return nil, fmt.Errorf("parse volcanoes.json: %w", err)
	}
	return doc.Volcanoes, nil
}
