package router

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Orbis-Mind/aether/internal/classifier"
	"github.com/Orbis-Mind/aether/internal/core"
	"github.com/Orbis-Mind/aether/internal/models"
	"github.com/Orbis-Mind/aether/internal/policy"
)

// Decision is the full, explainable output of the routing pipeline:
//
//	Task Analysis → Policy Filtering → Candidates → Capability/Quality/Cost/
//	Latency scoring → Final Selection
//
// It carries every candidate (with reject reasons) so `aether test`/`inspect`
// can show exactly why a model was or wasn't chosen.
type Decision struct {
	Analysis     classifier.TaskAnalysis
	Strategy     core.Strategy
	Requirements policy.Requirements
	Candidates   []policy.Candidate
	Chosen       string
	ChosenCand   policy.Candidate
	Reason       string
}

// Pipeline is the intelligent router. It composes the policy engine with a
// strategy-specific scoring function.
type Pipeline struct {
	engine *policy.Engine
}

// NewPipeline builds the routing pipeline.
func NewPipeline() *Pipeline { return &Pipeline{engine: policy.New()} }

// Route runs the full pipeline. `available` optionally restricts local models to
// those actually installed (from discovery); nil means availability is unknown
// and is not used as a filter.
func (pl *Pipeline) Route(
	analysis classifier.TaskAnalysis,
	pol core.Policy,
	strategy core.Strategy,
	inTok, outTok int,
	available map[string]bool,
) (*Decision, error) {
	if strategy == "" {
		strategy = core.StrategyAuto
	}
	// The "local" strategy is sugar for a local-only privacy constraint.
	effectivePol := pol
	if strategy == core.StrategyLocal {
		effectivePol.Privacy = core.PrivacyLocal
	}

	req := policy.Resolve(effectivePol, analysis, inTok, outTok)
	cands := pl.engine.Evaluate(effectivePol, analysis.Type, req)

	// Availability filter for local models: if we know what's installed, a local
	// model that isn't pulled cannot be chosen.
	if available != nil {
		for i := range cands {
			c := &cands[i]
			if c.Eligible && c.Profile.Local() && !available[c.Key] {
				c.Eligible = false
				c.RejectReason = "local model not installed (pull it with `ollama pull`)"
			}
		}
	}

	eligible := policy.Eligible(cands)
	dec := &Decision{
		Analysis:     analysis,
		Strategy:     strategy,
		Requirements: req,
		Candidates:   cands,
	}
	if len(eligible) == 0 {
		return dec, fmt.Errorf(
			"no model satisfies the policy for a %s task (min-quality=%.2f, budget=%s, max-latency=%dms%s) — relax the policy or pull a stronger local model",
			analysis.Type, req.MinQuality, core.FormatCost(req.MaxCost), req.MaxLatency, localOnlyNote(effectivePol),
		)
	}

	chosen := selectByStrategy(eligible, strategy, effectivePol, analysis.Type)
	dec.Chosen = chosen.Key
	dec.ChosenCand = chosen
	dec.Reason = explainChoice(chosen, strategy, effectivePol, analysis.Type)
	return dec, nil
}

func localOnlyNote(p core.Policy) string {
	if p.LocalOnly() {
		return ", local-only"
	}
	return ""
}

// selectByStrategy picks the winning candidate for the objective. local_first
// (without full local-only) is honoured by preferring an eligible local model
// when one exists, then applying the strategy within that subset.
func selectByStrategy(elig []policy.Candidate, strategy core.Strategy, pol core.Policy, task models.TaskType) policy.Candidate {
	pool := elig
	if pol.LocalFirst && !pol.LocalOnly() {
		if locals := onlyLocal(elig); len(locals) > 0 {
			pool = locals
		}
	}

	switch strategy {
	case core.StrategyCheapest:
		return best(pool, func(a, b policy.Candidate) bool {
			if a.EstCost != b.EstCost {
				return a.EstCost < b.EstCost
			}
			return a.Quality.Score > b.Quality.Score
		})
	case core.StrategyFastest:
		return best(pool, func(a, b policy.Candidate) bool {
			if a.Profile.AvgLatencyMS != b.Profile.AvgLatencyMS {
				return a.Profile.AvgLatencyMS < b.Profile.AvgLatencyMS
			}
			return a.Quality.Score > b.Quality.Score
		})
	case core.StrategyBest:
		return best(pool, func(a, b policy.Candidate) bool {
			if a.Quality.Score != b.Quality.Score {
				return a.Quality.Score > b.Quality.Score
			}
			return a.EstCost < b.EstCost
		})
	default: // auto, balanced, local
		return best(pool, func(a, b policy.Candidate) bool {
			return weightedScore(a) > weightedScore(b)
		})
	}
}

// weightedScore balances quality (favoured), cost and latency. Local models
// have zero cost, so among locals this reduces to quality-then-speed.
func weightedScore(c policy.Candidate) float64 {
	// Normalisation constants chosen so the terms are comparable; cost is scaled
	// against a $0.05 reference run, latency against 5s.
	costPenalty := c.EstCost / 0.05
	latPenalty := float64(c.Profile.AvgLatencyMS) / 5000.0
	return 0.60*c.Quality.Score - 0.25*costPenalty - 0.15*latPenalty
}

func onlyLocal(cands []policy.Candidate) []policy.Candidate {
	var out []policy.Candidate
	for _, c := range cands {
		if c.Profile.Local() {
			out = append(out, c)
		}
	}
	return out
}

func best(cands []policy.Candidate, less func(a, b policy.Candidate) bool) policy.Candidate {
	winner := cands[0]
	for _, c := range cands[1:] {
		if less(c, winner) {
			winner = c
		}
	}
	return winner
}

func explainChoice(c policy.Candidate, strategy core.Strategy, pol core.Policy, task models.TaskType) string {
	q := c.Quality
	prov := string(q.Provenance)
	base := fmt.Sprintf("strategy=%s, task=%s: chose %s (quality %.2f [%s]", strategy, task, c.Key, q.Score, prov)
	if c.Profile.Local() {
		base += ", local $0"
	} else {
		base += fmt.Sprintf(", est cost %s", core.FormatCost(c.EstCost))
	}
	base += fmt.Sprintf(", ~%dms)", c.Profile.AvgLatencyMS)
	if pol.LocalFirst && c.Profile.Local() {
		base += " — local-first satisfied"
	}
	return base
}

// ExplainDecision renders a human-readable routing table for a Decision.
func ExplainDecision(dec *Decision) string {
	var sb strings.Builder
	a := dec.Analysis
	sb.WriteString("Intelligent Routing Analysis\n")
	sb.WriteString(strings.Repeat("=", 78) + "\n")
	sb.WriteString(fmt.Sprintf("Task: %-14s Difficulty: %.2f  Risk: %-8s Reasoning:%v Tools:%v\n",
		a.Type, a.Difficulty, a.Risk, a.RequiresReasoning, a.RequiresTools))
	sb.WriteString(fmt.Sprintf("Strategy: %-10s Min-quality: %.2f  Budget: %s  Max-latency: %dms\n",
		dec.Strategy, dec.Requirements.MinQuality, core.FormatCost(dec.Requirements.MaxCost), dec.Requirements.MaxLatency))
	sb.WriteString(strings.Repeat("-", 78) + "\n")
	sb.WriteString(fmt.Sprintf("%-18s %8s %6s %10s %9s  %s\n", "Model", "Quality", "Prov", "Cost $", "Latency", "Status"))
	sb.WriteString(strings.Repeat("-", 78) + "\n")

	cands := make([]policy.Candidate, len(dec.Candidates))
	copy(cands, dec.Candidates)
	sort.Slice(cands, func(i, j int) bool { return cands[i].Key < cands[j].Key })
	for _, c := range cands {
		status := "✓ eligible"
		if !c.Eligible {
			status = "✗ " + c.RejectReason
		}
		if c.Key == dec.Chosen {
			status = "★ SELECTED"
		}
		prov := provAbbrev(c.Quality.Provenance)
		sb.WriteString(fmt.Sprintf("%-18s %8.2f %6s %10.5f %7dms  %s\n",
			c.Key, c.Quality.Score, prov, c.EstCost, c.Profile.AvgLatencyMS, status))
	}
	sb.WriteString(strings.Repeat("-", 78) + "\n")
	if dec.Chosen != "" {
		sb.WriteString(dec.Reason + "\n")
	}
	return sb.String()
}

func provAbbrev(p models.Provenance) string {
	switch p {
	case models.Measured:
		return "meas"
	case models.Estimated:
		return "est"
	default:
		return "unk"
	}
}
