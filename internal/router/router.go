package router

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Orbis-Mind/aether/internal/core"
)

// ─────────────────────────────────────────────
// Thresholds
// ─────────────────────────────────────────────

var qualityThresholds = map[core.QualityLevel]float64{
	core.QualityDraft:    0.0,
	core.QualityBalanced: 0.65,
	core.QualityHigh:     0.80,
	core.QualityExpert:   0.93,
}

var latencyLimits = map[core.LatencyProfile]int{
	core.LatencyRealtime: 1_000,
	core.LatencyFast:     3_000,
	core.LatencyBalanced: 10_000,
	core.LatencyBatch:    999_999,
}

var complexityQuality = map[core.TaskComplexity]float64{
	core.ComplexityTrivial:  0.0,
	core.ComplexitySimple:   0.55,
	core.ComplexityModerate: 0.70,
	core.ComplexityComplex:  0.82,
	core.ComplexityExpert:   0.92,
}

// Scoring weights
const (
	wQuality = 0.50
	wCost    = 0.30
	wLatency = 0.10
	wStorage = 0.10 // favors smaller local models — all local models cost $0,
	// so disk footprint is what differentiates them ("less storage").
)

// ─────────────────────────────────────────────
// Router
// ─────────────────────────────────────────────

type Router struct{}

func New() *Router {
	return &Router{}
}

type scoredModel struct {
	key   string
	score float64
	cost  float64
}

func (r *Router) Select(
	complexity core.TaskComplexity,
	policy core.Policy,
	inputTokens, outputTokens int,
) (string, error) {
	candidates := r.filter(complexity, policy, inputTokens, outputTokens)
	if len(candidates) == 0 {
		return "", fmt.Errorf(
			"no model satisfies constraints (quality=%s, budget=%s, latency=%s) — "+
				"try relaxing your policy",
			policy.Quality, core.FormatCost(policy.MaxCostPerRun), policy.Latency,
		)
	}
	scored := r.score(candidates, inputTokens, outputTokens)
	sort.Slice(scored, func(i, j int) bool {
		if math.Abs(scored[i].score-scored[j].score) < 0.001 {
			return scored[i].cost < scored[j].cost
		}
		return scored[i].score > scored[j].score
	})
	return scored[0].key, nil
}

// providerEligible decides whether a model's provider is allowed for routing.
//   - An explicit allowed_providers list always wins when present.
//   - prefer_local restricts routing to local (Ollama) models only.
//   - By default, local models are opt-in (they need Ollama running + a pulled
//     model), so hosted APIs are used unless the user asks for local.
func providerEligible(spec core.ModelSpec, policy core.Policy) bool {
	if len(policy.AllowedProviders) > 0 {
		for _, p := range policy.AllowedProviders {
			if strings.EqualFold(p, string(spec.Provider)) {
				return true
			}
		}
		return false
	}
	if policy.PreferLocal {
		return spec.IsLocal()
	}
	return !spec.IsLocal()
}

func (r *Router) filter(
	complexity core.TaskComplexity,
	policy core.Policy,
	inputTokens, outputTokens int,
) []string {
	minQuality := math.Max(
		qualityThresholds[policy.Quality],
		complexityQuality[complexity],
	)
	maxLatency := latencyLimits[policy.Latency]

	var valid []string
	for key, spec := range core.ModelCatalogue {
		if spec.Provider == core.ProviderMock {
			continue
		}
		if !providerEligible(spec, policy) {
			continue
		}
		if spec.QualityScore < minQuality {
			continue
		}
		if spec.AvgLatencyMS > maxLatency {
			continue
		}
		if spec.CostEstimate(inputTokens, outputTokens) > policy.MaxCostPerRun {
			continue
		}
		if spec.ContextWindow < inputTokens+outputTokens {
			continue
		}
		valid = append(valid, key)
	}
	return valid
}

func (r *Router) score(keys []string, inputTokens, outputTokens int) []scoredModel {
	maxCost := 0.0
	maxLatency := 0.0
	maxDisk := 0.0
	for _, k := range keys {
		m := core.ModelCatalogue[k]
		c := m.CostEstimate(inputTokens, outputTokens)
		if c > maxCost {
			maxCost = c
		}
		lat := float64(m.AvgLatencyMS)
		if lat > maxLatency {
			maxLatency = lat
		}
		if m.ApproxDiskGB > maxDisk {
			maxDisk = m.ApproxDiskGB
		}
	}
	if maxCost == 0 {
		maxCost = 1
	}
	if maxLatency == 0 {
		maxLatency = 1
	}
	if maxDisk == 0 {
		maxDisk = 1
	}

	var result []scoredModel
	for _, k := range keys {
		m := core.ModelCatalogue[k]
		c := m.CostEstimate(inputTokens, outputTokens)
		s := wQuality*m.QualityScore -
			wCost*(c/maxCost) -
			wLatency*(float64(m.AvgLatencyMS)/maxLatency) -
			wStorage*(m.ApproxDiskGB/maxDisk)
		result = append(result, scoredModel{key: k, score: s, cost: c})
	}
	return result
}

// Explain returns a human-readable routing table
func (r *Router) Explain(
	complexity core.TaskComplexity,
	policy core.Policy,
	inputTokens, outputTokens int,
) string {
	candidates := r.filter(complexity, policy, inputTokens, outputTokens)
	candidateSet := make(map[string]bool)
	for _, c := range candidates {
		candidateSet[c] = true
	}

	var sb strings.Builder
	sb.WriteString("Model Routing Analysis\n")
	sb.WriteString(strings.Repeat("=", 68) + "\n")
	sb.WriteString(fmt.Sprintf(
		"Complexity: %-10s Quality: %-10s Latency: %-10s Budget: %s\n",
		complexity, policy.Quality, policy.Latency,
		core.FormatCost(policy.MaxCostPerRun),
	))
	sb.WriteString(strings.Repeat("-", 68) + "\n")
	sb.WriteString(fmt.Sprintf(
		"%-25s %8s %10s %10s %10s\n",
		"Model", "Quality", "Cost $", "Latency", "Eligible",
	))
	sb.WriteString(strings.Repeat("-", 68) + "\n")

	// Sort keys for consistent output
	keys := make([]string, 0, len(core.ModelCatalogue))
	for k := range core.ModelCatalogue {
		if core.ModelCatalogue[k].Provider != core.ProviderMock {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	for _, key := range keys {
		spec := core.ModelCatalogue[key]
		cost := spec.CostEstimate(inputTokens, outputTokens)
		eligible := "✗"
		if candidateSet[key] {
			eligible = "✓"
		}
		sb.WriteString(fmt.Sprintf(
			"%-25s %8.2f %10.5f %8dms %10s\n",
			key, spec.QualityScore, cost, spec.AvgLatencyMS, eligible,
		))
	}

	if len(candidates) > 0 {
		winner, _ := r.Select(complexity, policy, inputTokens, outputTokens)
		sb.WriteString(strings.Repeat("-", 68) + "\n")
		sb.WriteString(fmt.Sprintf("Selected: %s\n", winner))
	}

	return sb.String()
}
