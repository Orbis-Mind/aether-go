package models

import (
	"encoding/json"
	"os"
	"sync"
)

// Provenance records where a quality number came from. We never present an
// estimate or a guess as a real measurement.
type Provenance string

const (
	// Measured: produced by running the model against a dataset via the Aether
	// evaluation framework on this machine.
	Measured Provenance = "measured"
	// Estimated: derived from public reputation / vendor docs / relative sizing.
	// A reasonable prior, explicitly not a measurement.
	Estimated Provenance = "estimated"
	// Unknown: no basis for a number. Never fabricated.
	Unknown Provenance = "unknown"
)

// QualityValue is a quality score with its provenance attached, so every
// consumer can tell measured fact from estimate.
type QualityValue struct {
	Score      float64    `json:"score"`
	Provenance Provenance `json:"provenance"`
	// Source is a short human-readable note, e.g. "aether-basic-v1" for measured
	// values or "vendor-reputation" for estimates.
	Source string `json:"source,omitempty"`
}

// Unknown quality: a zero score explicitly flagged as having no basis.
func unknownQuality() QualityValue {
	return QualityValue{Score: 0, Provenance: Unknown}
}

// MeasuredRecord is one measured quality result for a (model, task) pair,
// written by the evaluation framework and read back to overlay the registry.
type MeasuredRecord struct {
	Model      string   `json:"model"`
	Task       TaskType `json:"task"`
	Score      float64  `json:"score"`
	Samples    int      `json:"samples"`
	Dataset    string   `json:"dataset"`
	MeasuredAt string   `json:"measured_at"`
}

// measuredStore holds measured overlays loaded from disk, keyed by model then
// task. It is safe for concurrent reads after loading.
type measuredStore struct {
	mu      sync.RWMutex
	byModel map[string]map[TaskType]QualityValue
}

var measured = &measuredStore{byModel: map[string]map[TaskType]QualityValue{}}

// LoadMeasured overlays measured benchmark results from a JSON file (an array
// of MeasuredRecord). Missing file is not an error — the registry simply falls
// back to estimated/unknown values. Returns the number of records applied.
func LoadMeasured(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var records []MeasuredRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return 0, err
	}
	measured.mu.Lock()
	defer measured.mu.Unlock()
	for _, r := range records {
		if measured.byModel[r.Model] == nil {
			measured.byModel[r.Model] = map[TaskType]QualityValue{}
		}
		measured.byModel[r.Model][r.Task] = QualityValue{
			Score:      r.Score,
			Provenance: Measured,
			Source:     r.Dataset,
		}
	}
	return len(records), nil
}

func measuredFor(model string, task TaskType) (QualityValue, bool) {
	measured.mu.RLock()
	defer measured.mu.RUnlock()
	if m, ok := measured.byModel[model]; ok {
		if q, ok := m[task]; ok {
			return q, true
		}
	}
	return QualityValue{}, false
}
