package eval

import (
	"context"
	"sort"
	"time"

	"github.com/Orbis-Mind/aether/internal/core"
	"github.com/Orbis-Mind/aether/internal/models"
	"github.com/Orbis-Mind/aether/internal/provider"
)

// ModelResult aggregates one model's performance over a whole dataset.
type ModelResult struct {
	Model string

	Total       int
	Succeeded   int
	Failed      int
	Scored      int // items that were quality-scorable AND succeeded
	QualitySum  float64
	LatenciesMS []float64
	InTokens    int
	OutTokens   int
	// TokenCounting is "measured" only if every successful run reported real
	// token counts; otherwise "estimated" (honest about provenance).
	AllTokensMeasured bool

	// PerCategoryQuality accumulates scored quality per task category.
	perCatSum   map[string]float64
	perCatCount map[string]int

	FirstError string
}

// SuccessRate returns the fraction of items that produced a response.
func (r ModelResult) SuccessRate() float64 {
	if r.Total == 0 {
		return 0
	}
	return float64(r.Succeeded) / float64(r.Total)
}

// Quality returns the mean heuristic quality over scored items, and whether any
// items were scorable at all.
func (r ModelResult) Quality() (float64, bool) {
	if r.Scored == 0 {
		return 0, false
	}
	return r.QualitySum / float64(r.Scored), true
}

// P95LatencyMS returns the 95th-percentile latency of successful runs.
func (r ModelResult) P95LatencyMS() float64 {
	if len(r.LatenciesMS) == 0 {
		return 0
	}
	s := append([]float64(nil), r.LatenciesMS...)
	sort.Float64s(s)
	idx := int(float64(len(s)) * 0.95)
	if idx >= len(s) {
		idx = len(s) - 1
	}
	return s[idx]
}

// Report is the outcome of evaluating a dataset over one or more models.
type Report struct {
	Dataset string
	Results []ModelResult
}

// Options configure a run.
type Options struct {
	MaxTokens int
	// SystemPrompt is prepended to every item (kept minimal for fair scoring).
	SystemPrompt string
	// Timeout per item.
	Timeout time.Duration
	// Progress, if set, is called after each item completes.
	Progress func(model string, done, total int)
}

// Run evaluates the dataset against every model key given, using the provider
// registry. Models that are entirely unreachable still appear in the report
// (with failures and the first error) — we never hide failures.
func Run(ctx context.Context, ds *Dataset, modelKeys []string, reg *provider.Registry, opts Options) *Report {
	if opts.MaxTokens == 0 {
		opts.MaxTokens = 512
	}
	if opts.Timeout == 0 {
		opts.Timeout = 120 * time.Second
	}
	if opts.SystemPrompt == "" {
		opts.SystemPrompt = "You are a concise, accurate assistant. Answer directly."
	}

	rep := &Report{Dataset: ds.Name}
	for _, mk := range modelKeys {
		res := ModelResult{Model: mk, AllTokensMeasured: true, perCatSum: map[string]float64{}, perCatCount: map[string]int{}}
		for i, it := range ds.Items {
			res.Total++
			itemCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
			t0 := time.Now()
			resp, err := reg.Generate(itemCtx, mk, opts.SystemPrompt, it.Prompt, opts.MaxTokens)
			elapsed := float64(time.Since(t0).Milliseconds())
			cancel()
			if err != nil {
				res.Failed++
				if res.FirstError == "" {
					res.FirstError = err.Error()
				}
				if opts.Progress != nil {
					opts.Progress(mk, i+1, len(ds.Items))
				}
				continue
			}
			res.Succeeded++
			res.LatenciesMS = append(res.LatenciesMS, elapsed)
			res.InTokens += resp.InputTokens
			res.OutTokens += resp.OutputTokens
			if resp.TokenCounting != provider.TokensMeasured {
				res.AllTokensMeasured = false
			}
			if sc := ScoreItem(it, resp.Text); sc.Scorable {
				res.Scored++
				res.QualitySum += sc.Value
				res.perCatSum[it.Category] += sc.Value
				res.perCatCount[it.Category]++
			}
			if opts.Progress != nil {
				opts.Progress(mk, i+1, len(ds.Items))
			}
		}
		rep.Results = append(rep.Results, res)
	}
	return rep
}

// MeasuredRecords converts a report into measured per-(model,category) quality
// records suitable for models.LoadMeasured, so evaluation feeds routing.
func (rep *Report) MeasuredRecords() []models.MeasuredRecord {
	now := time.Now().UTC().Format(time.RFC3339)
	var out []models.MeasuredRecord
	for _, r := range rep.Results {
		for cat, sum := range r.perCatSum {
			cnt := r.perCatCount[cat]
			if cnt == 0 {
				continue
			}
			out = append(out, models.MeasuredRecord{
				Model:      r.Model,
				Task:       models.TaskType(cat),
				Score:      sum / float64(cnt),
				Samples:    cnt,
				Dataset:    rep.Dataset,
				MeasuredAt: now,
			})
		}
	}
	return out
}

// FormatCost is re-exported for report rendering convenience.
func FormatCost(c float64) string { return core.FormatCost(c) }
