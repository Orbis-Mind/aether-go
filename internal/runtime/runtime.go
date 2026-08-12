// Package runtime executes a compiled plan: render prompt, consult cache,
// call a model provider with retry + fallback, persist memory/cache, and record
// observability + routing telemetry. All model I/O goes through the provider
// abstraction, so the runtime has no provider-specific code.
package runtime

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Orbis-Mind/aether/internal/cache"
	"github.com/Orbis-Mind/aether/internal/classifier"
	"github.com/Orbis-Mind/aether/internal/core"
	"github.com/Orbis-Mind/aether/internal/memory"
	"github.com/Orbis-Mind/aether/internal/observability"
	"github.com/Orbis-Mind/aether/internal/provider"
)

const (
	defaultMaxTokens = 2048
	// perAttemptTimeout bounds a single model call. Generous because a local
	// model's first token can lag while it pages into RAM/VRAM.
	perAttemptTimeout = 300 * time.Second
)

// Runtime holds the wired execution dependencies for one agent.
type Runtime struct {
	spec       *core.AetherSpec
	plan       *core.ExecutionPlan
	memory     *memory.Orchestrator
	cache      *cache.Orchestrator
	obs        *observability.Store
	providers  *provider.Registry
	classifier classifier.Classifier
	dataDir    string
}

// New wires a runtime for a spec + plan, persisting state under dataDir.
func New(spec *core.AetherSpec, plan *core.ExecutionPlan, dataDir string) *Runtime {
	return &Runtime{
		spec:       spec,
		plan:       plan,
		memory:     memory.NewOrchestrator(spec.Memory, dataDir+"/memory"),
		cache:      cache.NewOrchestrator(plan.CacheStrategy, dataDir+"/cache"),
		obs:        observability.NewStore(dataDir + "/observability"),
		providers:  provider.NewRegistry(),
		classifier: classifier.New(),
		dataDir:    dataDir,
	}
}

// Obs exposes the observability store (used by the CLI and tests).
func (r *Runtime) Obs() *observability.Store { return r.obs }

// renderContext holds the resolved prompt pieces for one run.
type renderContext struct {
	system   string
	user     string
	cacheKey string
	memCtx   string
}

// prepare renders the system+user prompt, injecting retrieved memory.
func (r *Runtime) prepare(userMessage string, variables map[string]string) renderContext {
	memCtx := ""
	if r.plan.RetrievalStrategy != "none" {
		memCtx = r.memory.Retrieve(userMessage, 3)
	}
	system := r.plan.OptimizedSystemPrompt
	if memCtx != "" {
		system += "\n\n" + memCtx
	}
	user := r.plan.OptimizedUserTemplate
	if variables == nil {
		variables = make(map[string]string)
	}
	variables["user_message"] = userMessage
	if memCtx != "" {
		variables["retrieved_context"] = memCtx
	}
	for k, v := range variables {
		user = strings.ReplaceAll(user, "{"+k+"}", v)
	}
	return renderContext{system: system, user: user, cacheKey: system + "|||" + user, memCtx: memCtx}
}

// fallbackChain returns the ordered model keys to try: primary then any
// fallback models, honouring the fallback policy toggle.
func (r *Runtime) fallbackChain() []string {
	chain := []string{r.plan.SelectedModel}
	if r.spec.Policy.FallbackEnabled || len(r.spec.Policy.FallbackModels) > 0 {
		max := r.spec.Policy.FallbackMaxAttempts
		for _, m := range r.spec.Policy.FallbackModels {
			if max > 0 && len(chain) > max {
				break
			}
			chain = append(chain, m)
		}
	}
	return chain
}

// execute runs the fallback chain with per-model retry + exponential backoff and
// jitter. Permanent errors (missing key, model not pulled) skip retries and move
// straight to the next fallback. Returns the response, the model that produced
// it, retry count, and last error.
func (r *Runtime) execute(rc renderContext) (*provider.GenerateResponse, string, int, error) {
	var lastErr error
	usedModel := r.plan.SelectedModel
	totalRetries := 0

	for _, modelKey := range r.fallbackChain() {
		if _, ok := core.ModelCatalogue[modelKey]; !ok {
			continue
		}
		for attempt := 0; attempt <= r.spec.Policy.MaxRetries; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), perAttemptTimeout)
			resp, err := r.providers.Generate(ctx, modelKey, rc.system, rc.user, defaultMaxTokens)
			cancel()
			if err == nil {
				return &resp, modelKey, totalRetries, nil
			}
			lastErr = err
			if provider.IsPermanent(err) {
				break // hopeless for this model; try the next fallback
			}
			if attempt < r.spec.Policy.MaxRetries {
				totalRetries++
				time.Sleep(backoff(attempt))
			}
		}
	}
	return nil, usedModel, totalRetries, lastErr
}

// backoff computes exponential backoff with jitter to avoid retry storms.
// Jitter is ±25% of the base delay, seeded from the clock so concurrent
// retriers don't align, without depending on math/rand global state.
func backoff(attempt int) time.Duration {
	base := time.Duration(1<<uint(attempt)) * 500 * time.Millisecond
	sign := time.Now().UnixNano()%3 - 1 // -1, 0, or +1
	jitter := time.Duration(int64(base)/4) * time.Duration(sign)
	return base + jitter
}

// Run executes the plan for one user message and returns the result.
func (r *Runtime) Run(userMessage string, variables map[string]string) *core.ExecutionResult {
	result := core.NewExecutionResult(r.plan.PlanID)
	t0 := time.Now()
	rc := r.prepare(userMessage, variables)

	// Cache lookup — only for prompts safe to reuse (see cache.Cacheable).
	if cache.Cacheable(rc.cacheKey) {
		if cached, ok := r.cache.Lookup(rc.cacheKey); ok {
			result.Output = cached
			result.ModelUsed = r.plan.SelectedModel
			result.Cached = true
			result.Success = true
			result.LatencyMS = float64(time.Since(t0).Milliseconds())
			r.obs.Record(&result)
			r.captureRouting(rc, &result, false)
			return &result
		}
	}

	resp, usedModel, retries, err := r.execute(rc)
	result.Retries = retries
	if resp == nil {
		result.Success = false
		result.Error = fmt.Sprintf("%v", err)
		result.ModelUsed = usedModel
		result.LatencyMS = float64(time.Since(t0).Milliseconds())
		r.obs.Record(&result)
		r.captureRouting(rc, &result, usedModel != r.plan.SelectedModel)
		return &result
	}

	r.persist(userMessage, resp.Text, rc)

	mSpec := core.ModelCatalogue[usedModel]
	result.Success = true
	result.Output = resp.Text
	result.ModelUsed = usedModel
	result.InputTokens = resp.InputTokens
	result.OutputTokens = resp.OutputTokens
	result.Cost = mSpec.CostEstimate(resp.InputTokens, resp.OutputTokens)
	result.LatencyMS = float64(time.Since(t0).Milliseconds())
	if result.Metadata == nil {
		result.Metadata = map[string]string{}
	}
	result.Metadata["token_counting"] = string(resp.TokenCounting)

	r.obs.Record(&result)
	r.captureRouting(rc, &result, usedModel != r.plan.SelectedModel)
	return &result
}

// RunStream executes the plan and streams response deltas to out as they
// arrive (no full-response buffering before first byte). It returns the final
// result once the stream completes. Streaming bypasses cache and fallback to
// keep the streamed byte order well-defined.
func (r *Runtime) RunStream(userMessage string, variables map[string]string, out io.Writer) *core.ExecutionResult {
	result := core.NewExecutionResult(r.plan.PlanID)
	t0 := time.Now()
	rc := r.prepare(userMessage, variables)

	ctx, cancel := context.WithTimeout(context.Background(), perAttemptTimeout)
	defer cancel()
	events, err := r.providers.Stream(ctx, r.plan.SelectedModel, rc.system, rc.user, defaultMaxTokens)
	if err != nil {
		result.Success = false
		result.Error = err.Error()
		result.ModelUsed = r.plan.SelectedModel
		result.LatencyMS = float64(time.Since(t0).Milliseconds())
		r.obs.Record(&result)
		return &result
	}

	var final *provider.GenerateResponse
	for ev := range events {
		switch {
		case ev.Err != nil:
			result.Success = false
			result.Error = ev.Err.Error()
			result.ModelUsed = r.plan.SelectedModel
			result.LatencyMS = float64(time.Since(t0).Milliseconds())
			r.obs.Record(&result)
			return &result
		case ev.Delta != "":
			io.WriteString(out, ev.Delta)
		case ev.Done:
			final = ev.Final
		}
	}

	if final == nil {
		final = &provider.GenerateResponse{}
	}
	r.persist(userMessage, final.Text, rc)
	mSpec := core.ModelCatalogue[r.plan.SelectedModel]
	result.Success = true
	result.Output = final.Text
	result.ModelUsed = r.plan.SelectedModel
	result.InputTokens = final.InputTokens
	result.OutputTokens = final.OutputTokens
	result.Cost = mSpec.CostEstimate(final.InputTokens, final.OutputTokens)
	result.LatencyMS = float64(time.Since(t0).Milliseconds())
	r.obs.Record(&result)
	r.captureRouting(rc, &result, false)
	return &result
}

// persist writes memory and (when safe) the cache.
func (r *Runtime) persist(userMessage, output string, rc renderContext) {
	if r.spec.Memory.LongTerm {
		r.memory.Store("Q: "+truncate(userMessage, 200)+"\nA: "+truncate(output, 200), "long_term")
	}
	if r.spec.Memory.Semantic {
		r.memory.Store(truncate(output, 500), "semantic")
	}
	if cache.Cacheable(rc.cacheKey) {
		r.cache.Store(rc.cacheKey, output)
	}
}

// captureRouting appends a routing-dataset row for this run (best-effort).
func (r *Runtime) captureRouting(rc renderContext, result *core.ExecutionResult, usedFallback bool) {
	a := r.classifier.Classify(rc.system, rc.user, len(r.spec.Tools) > 0)
	strategy := string(r.spec.Policy.Strategy)
	if strategy == "" {
		strategy = string(core.StrategyAuto)
	}
	_ = observability.AppendRouting(r.dataDir+"/observability", observability.RoutingRecord{
		TaskType:     string(a.Type),
		Difficulty:   a.Difficulty,
		Risk:         string(a.Risk),
		Strategy:     strategy,
		Model:        result.ModelUsed,
		InputTokens:  result.InputTokens,
		OutTokens:    result.OutputTokens,
		LatencyMS:    result.LatencyMS,
		Cost:         result.Cost,
		Success:      result.Success,
		UsedFallback: usedFallback,
		Cached:       result.Cached,
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
