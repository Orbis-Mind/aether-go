// Package classifier turns a raw prompt into a structured TaskAnalysis using
// deterministic, offline heuristics. It requires no API and no model. The
// Classifier interface is deliberately small so a local ML classifier can
// replace the heuristic one later without touching callers.
package classifier

import (
	"regexp"
	"strings"

	"github.com/Orbis-Mind/aether/internal/core"
	"github.com/Orbis-Mind/aether/internal/models"
)

// RiskLevel captures how costly a wrong or unsafe answer would be, used by
// policy (e.g. high-risk tasks should not be served from cache or the weakest
// model).
type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskModerate RiskLevel = "moderate"
	RiskHigh     RiskLevel = "high"
)

// TaskAnalysis is the structured result of classifying a prompt.
type TaskAnalysis struct {
	Type              models.TaskType
	Difficulty        float64 // 0..1
	Risk              RiskLevel
	ContextSize       int // estimated input tokens
	RequiresReasoning bool
	RequiresTools     bool
}

// Complexity maps the continuous difficulty onto the existing core complexity
// buckets, so the legacy compiler/router keep working unchanged.
func (a TaskAnalysis) Complexity() core.TaskComplexity {
	switch {
	case a.Difficulty >= 0.85:
		return core.ComplexityExpert
	case a.Difficulty >= 0.65:
		return core.ComplexityComplex
	case a.Difficulty >= 0.45:
		return core.ComplexityModerate
	case a.Difficulty >= 0.25:
		return core.ComplexitySimple
	default:
		return core.ComplexityTrivial
	}
}

// Classifier produces a TaskAnalysis for a prompt. Implementations must be
// deterministic for a given input.
type Classifier interface {
	Classify(system, user string, hasTools bool) TaskAnalysis
}

// Heuristic is the default zero-cost, offline classifier.
type Heuristic struct{}

// New returns the default heuristic classifier.
func New() *Heuristic { return &Heuristic{} }

// Signal keyword sets per task type. Order of evaluation matters: more specific
// categories are checked before general ones.
var taskSignals = []struct {
	Type     models.TaskType
	Keywords []string
	Patterns []*regexp.Regexp
}{
	{models.TaskCoding, []string{"code", "function", "bug", "compile", "refactor", "unit test", "api", "regex", "algorithm", "implement", "debug", "stack trace", "syntax"}, []*regexp.Regexp{
		regexp.MustCompile("```"),
		regexp.MustCompile(`(?i)\b(python|golang|javascript|typescript|rust|java|c\+\+|sql)\b`),
	}},
	{models.TaskMath, []string{"calculate", "solve for", "equation", "derivative", "integral", "probability", "arithmetic", "compute the"}, []*regexp.Regexp{
		regexp.MustCompile(`\d+\s*[\+\-\*/\^]\s*\d+`),
	}},
	{models.TaskTranslation, []string{"translate", "translation", "in french", "in spanish", "in german", "into english", "从中文", "en español"}, nil},
	{models.TaskExtraction, []string{"extract", "parse out", "pull the", "return json", "structured output", "list all the", "find every"}, nil},
	{models.TaskClassification, []string{"classify", "categorize", "label", "sentiment", "is this spam", "which category", "tag the"}, nil},
	{models.TaskSummarization, []string{"summarize", "summary", "tl;dr", "condense", "key points", "abstract of"}, nil},
	{models.TaskRAG, []string{"based on the following", "using the context", "according to the document", "given the passage", "cite", "from the provided"}, nil},
	{models.TaskAgentic, []string{"use the tool", "call the", "step by step plan", "then do", "orchestrate", "multi-step", "take action"}, nil},
	{models.TaskResearch, []string{"research", "investigate", "compare and contrast", "literature", "survey the", "pros and cons of"}, nil},
	{models.TaskReasoning, []string{"why", "explain why", "reason", "deduce", "infer", "logic", "prove", "justify", "analyze", "evaluate", "critique"}, nil},
	{models.TaskCreative, []string{"write a story", "poem", "haiku", "creative", "imagine", "brainstorm", "slogan", "lyrics"}, nil},
}

// difficulty-raising signals: presence pushes the difficulty score up.
var (
	reasoningKW = []string{"why", "reason", "explain", "analyze", "evaluate", "critique", "synthesize", "design", "architect", "prove", "derive", "trade-off", "compare"}
	longFormKW  = []string{"comprehensive", "detailed", "in depth", "thorough", "step by step", "exhaustive"}
	toolKW      = []string{"use the tool", "call the", "fetch", "search the web", "run ", "execute", "query the database"}
	highRiskKW  = []string{"medical", "diagnos", "legal", "lawsuit", "financial advice", "invest", "password", "security", "production database", "delete", "irreversible", "dosage", "credentials"}
)

func containsAny(s string, kws []string) (bool, int) {
	n := 0
	for _, kw := range kws {
		if strings.Contains(s, kw) {
			n++
		}
	}
	return n > 0, n
}

// Classify implements Classifier using keyword and pattern heuristics.
func (h *Heuristic) Classify(system, user string, hasTools bool) TaskAnalysis {
	combined := strings.ToLower(system + "\n" + user)
	contextTokens := core.EstimateTokens(system) + core.EstimateTokens(user)

	// 1. Task type: first matching signal set wins; default general.
	taskType := models.TaskGeneral
	for _, sig := range taskSignals {
		matched, _ := containsAny(combined, sig.Keywords)
		if !matched {
			for _, p := range sig.Patterns {
				if p.MatchString(user) || p.MatchString(system) {
					matched = true
					break
				}
			}
		}
		if matched {
			taskType = sig.Type
			break
		}
	}

	// 2. Difficulty: start from a task-type base, then adjust for signals.
	difficulty := baseDifficulty(taskType)
	if ok, n := containsAny(combined, reasoningKW); ok {
		difficulty += 0.05 * float64(min(n, 3))
	}
	if ok, _ := containsAny(combined, longFormKW); ok {
		difficulty += 0.1
	}
	// Long inputs are harder to handle well.
	switch {
	case contextTokens > 4000:
		difficulty += 0.15
	case contextTokens > 1500:
		difficulty += 0.08
	}
	if hasTools {
		difficulty += 0.05
	}
	difficulty = clamp(difficulty, 0, 1)

	// 3. Reasoning / tools requirements.
	requiresReasoning, _ := containsAny(combined, reasoningKW)
	if taskType == models.TaskReasoning || taskType == models.TaskResearch || taskType == models.TaskMath {
		requiresReasoning = true
	}
	requiresTools := hasTools
	if ok, _ := containsAny(combined, toolKW); ok {
		requiresTools = true
	}
	if taskType == models.TaskAgentic {
		requiresTools = true
	}

	// 4. Risk.
	risk := RiskLow
	if ok, _ := containsAny(combined, highRiskKW); ok {
		risk = RiskHigh
	} else if difficulty >= 0.7 || requiresTools {
		risk = RiskModerate
	}

	return TaskAnalysis{
		Type:              taskType,
		Difficulty:        difficulty,
		Risk:              risk,
		ContextSize:       contextTokens,
		RequiresReasoning: requiresReasoning,
		RequiresTools:     requiresTools,
	}
}

// baseDifficulty is a per-task starting difficulty before signal adjustment.
func baseDifficulty(t models.TaskType) float64 {
	switch t {
	case models.TaskClassification, models.TaskExtraction, models.TaskTranslation:
		return 0.30
	case models.TaskSummarization:
		return 0.40
	case models.TaskGeneral, models.TaskCreative:
		return 0.45
	case models.TaskRAG, models.TaskCoding:
		return 0.60
	case models.TaskMath, models.TaskReasoning:
		return 0.68
	case models.TaskResearch, models.TaskAgentic:
		return 0.78
	default:
		return 0.5
	}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
