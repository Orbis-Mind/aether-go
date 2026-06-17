package core

import (
	"crypto/rand"
	"fmt"
	"time"
)

type TaskComplexity string
const (
	ComplexityTrivial  TaskComplexity = "trivial"
	ComplexitySimple   TaskComplexity = "simple"
	ComplexityModerate TaskComplexity = "moderate"
	ComplexityComplex  TaskComplexity = "complex"
	ComplexityExpert   TaskComplexity = "expert"
)

type QualityLevel string
const (
	QualityDraft    QualityLevel = "draft"
	QualityBalanced QualityLevel = "balanced"
	QualityHigh     QualityLevel = "high"
	QualityExpert   QualityLevel = "expert"
)

type LatencyProfile string
const (
	LatencyRealtime LatencyProfile = "realtime"
	LatencyFast     LatencyProfile = "fast"
	LatencyBalanced LatencyProfile = "balanced"
	LatencyBatch    LatencyProfile = "batch"
)

type ModelProvider string
const (
	ProviderAnthropic ModelProvider = "anthropic"
	ProviderOpenAI    ModelProvider = "openai"
	ProviderOllama    ModelProvider = "ollama"
	ProviderMock      ModelProvider = "mock"
)

type ModelSpec struct {
	ID                 string
	Provider           ModelProvider
	ContextWindow      int
	CostPerInputToken  float64
	CostPerOutputToken float64
	AvgLatencyMS       int
	QualityScore       float64
	SupportsTools      bool
	// ApproxDiskGB is the rough on-disk footprint of a local (Ollama) model,
	// shown so users can trade quality against storage. Zero for hosted APIs.
	ApproxDiskGB       float64
}

// IsLocal reports whether the model runs locally (no network/API key, zero cost).
func (m ModelSpec) IsLocal() bool { return m.Provider == ProviderOllama }

func (m ModelSpec) CostEstimate(inputTokens, outputTokens int) float64 {
	return (float64(inputTokens)/1000)*m.CostPerInputToken +
		(float64(outputTokens)/1000)*m.CostPerOutputToken
}

var ModelCatalogue = map[string]ModelSpec{
	"claude-haiku-3-5": {
		ID: "claude-haiku-3-5-20241022", Provider: ProviderAnthropic,
		ContextWindow: 200_000, CostPerInputToken: 0.001, CostPerOutputToken: 0.005,
		AvgLatencyMS: 800, QualityScore: 0.72, SupportsTools: true,
	},
	"claude-sonnet-3-5": {
		ID: "claude-sonnet-3-5-20241022", Provider: ProviderAnthropic,
		ContextWindow: 200_000, CostPerInputToken: 0.003, CostPerOutputToken: 0.015,
		AvgLatencyMS: 2000, QualityScore: 0.88, SupportsTools: true,
	},
	"claude-opus-4": {
		ID: "claude-opus-4-5", Provider: ProviderAnthropic,
		ContextWindow: 200_000, CostPerInputToken: 0.015, CostPerOutputToken: 0.075,
		AvgLatencyMS: 5000, QualityScore: 0.97, SupportsTools: true,
	},
	"gpt-4o-mini": {
		ID: "gpt-4o-mini", Provider: ProviderOpenAI,
		ContextWindow: 128_000, CostPerInputToken: 0.00015, CostPerOutputToken: 0.0006,
		AvgLatencyMS: 700, QualityScore: 0.70, SupportsTools: true,
	},
	"gpt-4o": {
		ID: "gpt-4o", Provider: ProviderOpenAI,
		ContextWindow: 128_000, CostPerInputToken: 0.005, CostPerOutputToken: 0.015,
		AvgLatencyMS: 2500, QualityScore: 0.90, SupportsTools: true,
	},
	// ─── Local models via Ollama (zero cost, runs on your machine) ───
	// IDs are Ollama tags — pull with e.g. `ollama pull llama3.2`.
	// Small footprint first, so a laptop can run them with little storage.
	"phi3": {
		ID: "phi3", Provider: ProviderOllama,
		ContextWindow: 128_000, CostPerInputToken: 0.0, CostPerOutputToken: 0.0,
		AvgLatencyMS: 1400, QualityScore: 0.66, SupportsTools: false, ApproxDiskGB: 2.3,
	},
	"llama3.2": {
		ID: "llama3.2", Provider: ProviderOllama,
		ContextWindow: 128_000, CostPerInputToken: 0.0, CostPerOutputToken: 0.0,
		AvgLatencyMS: 1500, QualityScore: 0.69, SupportsTools: true, ApproxDiskGB: 2.0,
	},
	"gemma2": {
		ID: "gemma2:2b", Provider: ProviderOllama,
		ContextWindow: 8_192, CostPerInputToken: 0.0, CostPerOutputToken: 0.0,
		AvgLatencyMS: 1300, QualityScore: 0.71, SupportsTools: false, ApproxDiskGB: 1.6,
	},
	"mistral": {
		ID: "mistral", Provider: ProviderOllama,
		ContextWindow: 32_768, CostPerInputToken: 0.0, CostPerOutputToken: 0.0,
		AvgLatencyMS: 2200, QualityScore: 0.76, SupportsTools: true, ApproxDiskGB: 4.1,
	},
	"llama3.1": {
		ID: "llama3.1", Provider: ProviderOllama,
		ContextWindow: 128_000, CostPerInputToken: 0.0, CostPerOutputToken: 0.0,
		AvgLatencyMS: 2500, QualityScore: 0.79, SupportsTools: true, ApproxDiskGB: 4.9,
	},
	"qwen2.5": {
		ID: "qwen2.5", Provider: ProviderOllama,
		ContextWindow: 128_000, CostPerInputToken: 0.0, CostPerOutputToken: 0.0,
		AvgLatencyMS: 2200, QualityScore: 0.81, SupportsTools: true, ApproxDiskGB: 4.7,
	},
	"mock": {
		ID: "mock", Provider: ProviderMock,
		ContextWindow: 1_000_000, CostPerInputToken: 0.0, CostPerOutputToken: 0.0,
		AvgLatencyMS: 10, QualityScore: 0.5, SupportsTools: true,
	},
}

type Policy struct {
	MaxCostPerRun    float64        `yaml:"max_cost_per_run"`
	Quality          QualityLevel   `yaml:"quality"`
	Latency          LatencyProfile `yaml:"latency"`
	MaxRetries       int            `yaml:"max_retries"`
	FallbackModels   []string       `yaml:"fallback_models"`
	AllowedProviders []string       `yaml:"allowed_providers"`
	// PreferLocal restricts routing to local (Ollama) models — zero cost,
	// fully private, no API key. Set `prefer_local: true` in the spec.
	PreferLocal      bool           `yaml:"prefer_local"`
}

func DefaultPolicy() Policy {
	return Policy{MaxCostPerRun: 0.10, Quality: QualityBalanced, Latency: LatencyBalanced, MaxRetries: 3}
}

type MemoryConfig struct {
	Semantic     bool `yaml:"semantic"`
	LongTerm     bool `yaml:"long_term"`
	Entity       bool `yaml:"entity"`
	Relationship bool `yaml:"relationship"`
	TTLSeconds   int  `yaml:"ttl_seconds"`
}

func DefaultMemoryConfig() MemoryConfig { return MemoryConfig{TTLSeconds: 86400} }

type ToolDefinition struct {
	Name           string                 `yaml:"name"`
	Description    string                 `yaml:"description"`
	Parameters     map[string]interface{} `yaml:"parameters"`
	Implementation string                 `yaml:"implementation"`
}

type AetherSpec struct {
	Name        string                 `yaml:"name"`
	Version     string                 `yaml:"version"`
	Description string                 `yaml:"description"`
	Policy      Policy                 `yaml:"policy"`
	Memory      MemoryConfig           `yaml:"memory"`
	Tools       []ToolDefinition       `yaml:"tools"`
	Prompts     map[string]string      `yaml:"prompts"`
	Metadata    map[string]interface{} `yaml:"metadata"`
}

type ExecutionStep struct {
	Step     string   `json:"step"`
	Strategy string   `json:"strategy,omitempty"`
	Tools    []string `json:"tools,omitempty"`
}

type ExecutionPlan struct {
	PlanID                string          `json:"plan_id"`
	SpecName              string          `json:"spec_name"`
	SpecVersion           string          `json:"spec_version"`
	SelectedModel         string          `json:"selected_model"`
	EstimatedInputTokens  int             `json:"estimated_input_tokens"`
	EstimatedOutputTokens int             `json:"estimated_output_tokens"`
	EstimatedCost         float64         `json:"estimated_cost"`
	OptimizedSystemPrompt string          `json:"optimized_system_prompt"`
	OptimizedUserTemplate string          `json:"optimized_user_template"`
	RetrievalStrategy     string          `json:"retrieval_strategy"`
	CacheStrategy         string          `json:"cache_strategy"`
	Complexity            TaskComplexity  `json:"complexity"`
	ExecutionSteps        []ExecutionStep `json:"execution_steps"`
	CreatedAt             string          `json:"created_at"`
}

func shortID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("%08x", b)
}

func NewExecutionPlan() ExecutionPlan {
	return ExecutionPlan{PlanID: shortID(), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
}

type ExecutionResult struct {
	PlanID       string            `json:"plan_id"`
	RunID        string            `json:"run_id"`
	Success      bool              `json:"success"`
	Output       string            `json:"output"`
	ModelUsed    string            `json:"model_used"`
	InputTokens  int               `json:"input_tokens"`
	OutputTokens int               `json:"output_tokens"`
	Cost         float64           `json:"cost"`
	LatencyMS    float64           `json:"latency_ms"`
	Cached       bool              `json:"cached"`
	Retries      int               `json:"retries"`
	Error        string            `json:"error,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	CreatedAt    string            `json:"created_at"`
}

func NewExecutionResult(planID string) ExecutionResult {
	return ExecutionResult{PlanID: planID, RunID: shortID(), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
}

type RegistryEntry struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description"`
	Author       string   `json:"author"`
	PublishedAt  string   `json:"published_at"`
	ArtifactPath string   `json:"artifact_path"`
	SizeBytes    int64    `json:"size_bytes"`
	Checksum     string   `json:"checksum"`
	Tags         []string `json:"tags"`
	Downloads    int      `json:"downloads"`
}

func EstimateTokens(text string) int {
	t := len(text) / 4
	if t < 1 { return 1 }
	return t
}

func FormatCost(cost float64) string { return fmt.Sprintf("$%.6f", cost) }
