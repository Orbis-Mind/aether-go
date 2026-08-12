package models

import (
	"sort"

	"github.com/Orbis-Mind/aether/internal/core"
)

// Profile is the enriched, provenance-aware view of a catalogue model.
type Profile struct {
	ID           string
	Provider     string
	Capabilities Capabilities
	Pricing      Pricing
	AvgLatencyMS int
	ApproxDiskGB float64

	// Overall is a single quality prior for the model across all tasks. Derived
	// from the base catalogue and marked Estimated — it is not a measurement.
	Overall QualityValue

	// QualityByTask holds per-task quality. Entries are Measured when the
	// evaluation framework has produced them, otherwise the map has no entry and
	// QualityFor falls back to Overall.
	QualityByTask map[TaskType]QualityValue
}

// Local reports whether the model runs locally at zero cost.
func (p Profile) Local() bool { return p.Capabilities.Local }

// QualityFor returns the quality for a specific task. Preference order:
// measured per-task > estimated overall. The provenance always reflects the
// actual source so callers can distinguish fact from prior.
func (p Profile) QualityFor(task TaskType) QualityValue {
	if q, ok := p.QualityByTask[task]; ok {
		return q
	}
	return p.Overall
}

// estimatedSource explains where the base quality prior comes from.
func estimatedSource(provider core.ModelProvider) string {
	if provider == core.ProviderOllama {
		return "local-model-reputation"
	}
	return "vendor-reputation"
}

// profileFor builds an enriched Profile for a catalogue key, overlaying any
// measured per-task results loaded via LoadMeasured.
func profileFor(key string, spec core.ModelSpec) Profile {
	overall := QualityValue{
		Score:      spec.QualityScore,
		Provenance: Estimated,
		Source:     estimatedSource(spec.Provider),
	}
	if spec.QualityScore == 0 {
		overall = unknownQuality()
	}

	byTask := map[TaskType]QualityValue{}
	for _, task := range AllTaskTypes() {
		if q, ok := measuredFor(key, task); ok {
			byTask[task] = q
		}
	}

	return Profile{
		ID:       spec.ID,
		Provider: string(spec.Provider),
		Capabilities: Capabilities{
			ContextWindow: spec.ContextWindow,
			SupportsTools: spec.SupportsTools,
			Local:         spec.IsLocal(),
		},
		Pricing: Pricing{
			InputPer1K:  spec.CostPerInputToken,
			OutputPer1K: spec.CostPerOutputToken,
		},
		AvgLatencyMS:  spec.AvgLatencyMS,
		ApproxDiskGB:  spec.ApproxDiskGB,
		Overall:       overall,
		QualityByTask: byTask,
	}
}

// Get returns the enriched profile for a catalogue key.
func Get(key string) (Profile, bool) {
	spec, ok := core.ModelCatalogue[key]
	if !ok {
		return Profile{}, false
	}
	return profileFor(key, spec), true
}

// All returns every profile except the mock model, sorted by key for stable
// output. Keys are returned alongside profiles.
func All() map[string]Profile {
	out := make(map[string]Profile, len(core.ModelCatalogue))
	for key, spec := range core.ModelCatalogue {
		if spec.Provider == core.ProviderMock {
			continue
		}
		out[key] = profileFor(key, spec)
	}
	return out
}

// Keys returns catalogue keys (excluding mock) sorted alphabetically.
func Keys() []string {
	keys := make([]string, 0, len(core.ModelCatalogue))
	for k, spec := range core.ModelCatalogue {
		if spec.Provider == core.ProviderMock {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
