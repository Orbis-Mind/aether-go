// Package eval is Aether's free, offline evaluation framework. It runs a
// dataset of prompts against locally available models and scores them with
// deterministic heuristics — no paid API, no network beyond the local model
// runtime. Results can be written back as measured quality overlays for the
// model registry.
package eval

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
)

// Item is one benchmark prompt with optional deterministic expectations.
type Item struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Prompt   string `json:"prompt"`
	// ExpectKeywords: all-or-fraction of these should appear (case-insensitive)
	// in a good answer. ExpectRegex: the answer should match this pattern.
	// When neither is set, the item is not quality-scorable (latency/tokens/
	// success are still measured) and is reported as such — never fabricated.
	ExpectKeywords []string `json:"expect_keywords,omitempty"`
	ExpectRegex    string   `json:"expect_regex,omitempty"`
}

// Scorable reports whether the item has any deterministic expectation.
func (it Item) Scorable() bool {
	return len(it.ExpectKeywords) > 0 || it.ExpectRegex != ""
}

// Dataset is a named collection of items.
type Dataset struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	License     string `json:"license"`
	Items       []Item `json:"items"`
}

//go:embed aether-basic-v1.json
var embeddedBasic []byte

// Default returns the built-in aether-basic-v1 dataset, embedded in the binary
// so `aether evaluate` works with zero setup and fully offline.
func Default() (*Dataset, error) {
	return parse(embeddedBasic)
}

// Load reads a dataset from a JSON file.
func Load(path string) (*Dataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read dataset: %w", err)
	}
	return parse(data)
}

func parse(data []byte) (*Dataset, error) {
	var ds Dataset
	if err := json.Unmarshal(data, &ds); err != nil {
		return nil, fmt.Errorf("invalid dataset JSON: %w", err)
	}
	if len(ds.Items) == 0 {
		return nil, fmt.Errorf("dataset has no items")
	}
	return &ds, nil
}
