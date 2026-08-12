// Package policy enforces an agent's policy before any model is selected. It
// turns the full model set into a candidate set, excluding anything that
// violates cost, quality, latency, privacy, or capability constraints. The
// local-only guarantee is enforced here: when privacy is local (or prefer_local
// is set) no cloud provider can ever survive filtering.
package policy

import (
	"fmt"
	"sort"

	"github.com/Orbis-Mind/aether/internal/classifier"
	"github.com/Orbis-Mind/aether/internal/core"
	"github.com/Orbis-Mind/aether/internal/models"
)

// qualityThresholds maps the coarse Quality tier to a minimum model quality.
var qualityThresholds = map[core.QualityLevel]float64{
	core.QualityDraft:    0.0,
	core.QualityBalanced: 0.65,
	core.QualityHigh:     0.80,
	core.QualityExpert:   0.93,
}

// latencyLimits maps the coarse Latency profile to a max acceptable latency.
var latencyLimits = map[core.LatencyProfile]int{
	core.LatencyRealtime: 1_000,
	core.LatencyFast:     3_000,
	core.LatencyBalanced: 10_000,
	core.LatencyBatch:    999_999,
}

// complexityQuality maps task complexity to a floor quality — harder tasks need
// stronger models regardless of the coarse quality tier.
var complexityQuality = map[core.TaskComplexity]float64{
	core.ComplexityTrivial:  0.0,
	core.ComplexitySimple:   0.55,
	core.ComplexityModerate: 0.70,
	core.ComplexityComplex:  0.82,
	core.ComplexityExpert:   0.92,
}

// Candidate is a model that survived (or failed) policy filtering.
type Candidate struct {
	Key     string
	Profile models.Profile
	// Quality is the quality used for this task (measured per-task if available,
	// else estimated overall).
	Quality models.QualityValue
	// EstCost is the estimated USD cost at the analysed token sizes.
	EstCost float64
	// Eligible is false when RejectReason explains why it was excluded.
	Eligible     bool
	RejectReason string
}

// Requirements is the resolved, numeric form of a policy for a given task.
type Requirements struct {
	MinQuality  float64
	MaxLatency  int
	MaxCost     float64
	LocalOnly   bool
	NeedTools   bool
	InputTokens int
	OutTokens   int
}

// Resolve turns a policy + task analysis into concrete numeric requirements.
func Resolve(p core.Policy, a classifier.TaskAnalysis, inTok, outTok int) Requirements {
	minQ := qualityThresholds[p.Quality]
	if p.QualityMinimum > minQ {
		minQ = p.QualityMinimum
	}
	if cq := complexityQuality[a.Complexity()]; cq > minQ {
		minQ = cq
	}

	maxLat := latencyLimits[p.Latency]
	if p.LatencyMaxP95 > 0 && p.LatencyMaxP95 < maxLat {
		maxLat = p.LatencyMaxP95
	}
	if maxLat == 0 {
		maxLat = latencyLimits[core.LatencyBalanced]
	}

	return Requirements{
		MinQuality:  minQ,
		MaxLatency:  maxLat,
		MaxCost:     p.MaxCostPerRun,
		LocalOnly:   p.LocalOnly(),
		NeedTools:   a.RequiresTools,
		InputTokens: inTok,
		OutTokens:   outTok,
	}
}

// Engine applies a policy to the model registry.
type Engine struct{}

// New returns a policy engine.
func New() *Engine { return &Engine{} }

// localEnabled reports whether local models are in play for this policy. Local
// models are opt-in by default because pinning one the user hasn't pulled would
// fail at run time; a local signal (local-only privacy, local_first, the local
// strategy, or an ollama allowlist) turns them on. Runtime discovery can also
// enable them by proving what's installed.
func localEnabled(p core.Policy) bool {
	if p.LocalOnly() || p.LocalFirst || p.Strategy == core.StrategyLocal {
		return true
	}
	for _, ap := range p.AllowedProviders {
		if ap == string(core.ProviderOllama) {
			return true
		}
	}
	return false
}

func providerAllowed(p core.Policy, providerName string, local bool) (bool, string) {
	// Hard local-only guarantee: cloud can never pass.
	if p.LocalOnly() && !local {
		return false, "excluded by local-only privacy policy"
	}
	if local && !localEnabled(p) {
		return false, "local models are opt-in (set local_first, privacy: local, or --strategy local)"
	}
	if len(p.AllowedProviders) > 0 {
		for _, ap := range p.AllowedProviders {
			if ap == providerName {
				return true, ""
			}
		}
		return false, "provider not in allowed_providers"
	}
	return true, ""
}

// Evaluate filters every non-mock model against the resolved requirements,
// returning all candidates annotated with eligibility and reject reasons (for
// transparent explanations). The task type selects measured-vs-estimated
// quality per model.
func (e *Engine) Evaluate(p core.Policy, task models.TaskType, req Requirements) []Candidate {
	var out []Candidate
	for _, key := range models.Keys() {
		prof, _ := models.Get(key)
		q := prof.QualityFor(task)
		cand := Candidate{
			Key:      key,
			Profile:  prof,
			Quality:  q,
			EstCost:  prof.Pricing.CostEstimate(req.InputTokens, req.OutTokens),
			Eligible: true,
		}

		if ok, reason := providerAllowed(p, prof.Provider, prof.Local()); !ok {
			cand.Eligible, cand.RejectReason = false, reason
			out = append(out, cand)
			continue
		}
		if q.Score < req.MinQuality {
			cand.Eligible = false
			cand.RejectReason = fmt.Sprintf("quality %.2f < required %.2f", q.Score, req.MinQuality)
			out = append(out, cand)
			continue
		}
		if prof.AvgLatencyMS > req.MaxLatency {
			cand.Eligible = false
			cand.RejectReason = fmt.Sprintf("latency %dms > max %dms", prof.AvgLatencyMS, req.MaxLatency)
			out = append(out, cand)
			continue
		}
		if cand.EstCost > req.MaxCost {
			cand.Eligible = false
			cand.RejectReason = fmt.Sprintf("cost %s > budget %s", core.FormatCost(cand.EstCost), core.FormatCost(req.MaxCost))
			out = append(out, cand)
			continue
		}
		if prof.Capabilities.ContextWindow < req.InputTokens+req.OutTokens {
			cand.Eligible = false
			cand.RejectReason = "context window too small"
			out = append(out, cand)
			continue
		}
		if req.NeedTools && !prof.Capabilities.SupportsTools {
			cand.Eligible = false
			cand.RejectReason = "task needs tools; model has none"
			out = append(out, cand)
			continue
		}
		out = append(out, cand)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Eligible returns only the eligible candidates from an Evaluate result.
func Eligible(cands []Candidate) []Candidate {
	var out []Candidate
	for _, c := range cands {
		if c.Eligible {
			out = append(out, c)
		}
	}
	return out
}
