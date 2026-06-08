package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Orbis-Mind/aether/internal/cache"
	"github.com/Orbis-Mind/aether/internal/core"
	"github.com/Orbis-Mind/aether/internal/memory"
	"github.com/Orbis-Mind/aether/internal/observability"
)

// ─────────────────────────────────────────────
// Model Response
// ─────────────────────────────────────────────

type modelResponse struct {
	Text         string
	InputTokens  int
	OutputTokens int
}

// ─────────────────────────────────────────────
// Anthropic Client
// ─────────────────────────────────────────────

type anthropicRequest struct {
	Model     string              `json:"model"`
	MaxTokens int                 `json:"max_tokens"`
	System    string              `json:"system"`
	Messages  []anthropicMessage  `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func callAnthropic(modelID, system, user string, maxTokens int) (*modelResponse, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	reqBody := anthropicRequest{
		Model:     modelID,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  []anthropicMessage{{Role: "user", Content: user}},
	}
	body, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP error: %w", err)
	}
	defer resp.Body.Close()

	respData, _ := io.ReadAll(resp.Body)
	var ar anthropicResponse
	json.Unmarshal(respData, &ar)

	if ar.Error != nil {
		return nil, fmt.Errorf("Anthropic API error: %s", ar.Error.Message)
	}

	text := ""
	for _, block := range ar.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}

	return &modelResponse{
		Text:         text,
		InputTokens:  ar.Usage.InputTokens,
		OutputTokens: ar.Usage.OutputTokens,
	}, nil
}

// ─────────────────────────────────────────────
// OpenAI Client
// ─────────────────────────────────────────────

type openAIRequest struct {
	Model     string           `json:"model"`
	Messages  []openAIMessage  `json:"messages"`
	MaxTokens int              `json:"max_tokens"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func callOpenAI(modelID, system, user string, maxTokens int) (*modelResponse, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}

	reqBody := openAIRequest{
		Model:     modelID,
		MaxTokens: maxTokens,
		Messages: []openAIMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}
	body, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP error: %w", err)
	}
	defer resp.Body.Close()

	respData, _ := io.ReadAll(resp.Body)
	var or openAIResponse
	json.Unmarshal(respData, &or)

	if or.Error != nil {
		return nil, fmt.Errorf("OpenAI API error: %s", or.Error.Message)
	}
	if len(or.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned from OpenAI")
	}

	return &modelResponse{
		Text:         or.Choices[0].Message.Content,
		InputTokens:  or.Usage.PromptTokens,
		OutputTokens: or.Usage.CompletionTokens,
	}, nil
}

// ─────────────────────────────────────────────
// Mock Client
// ─────────────────────────────────────────────

func callMock(system, user string) *modelResponse {
	time.Sleep(10 * time.Millisecond)
	preview := user
	if len(preview) > 100 {
		preview = preview[:100] + "..."
	}
	text := fmt.Sprintf(
		"[MOCK RESPONSE]\nSystem context received (%d chars).\nUser message: %s\n"+
			"This is a mock response from the Aether runtime.\n"+
			"In production, a real model would generate the actual answer here.",
		len(system), preview,
	)
	return &modelResponse{
		Text:         text,
		InputTokens:  core.EstimateTokens(system) + core.EstimateTokens(user),
		OutputTokens: core.EstimateTokens(text),
	}
}

// ─────────────────────────────────────────────
// Dispatch
// ─────────────────────────────────────────────

func dispatch(modelKey, system, user string, maxTokens int) (*modelResponse, error) {
	spec, ok := core.ModelCatalogue[modelKey]
	if !ok {
		return nil, fmt.Errorf("unknown model key: %s", modelKey)
	}
	switch spec.Provider {
	case core.ProviderAnthropic:
		return callAnthropic(spec.ID, system, user, maxTokens)
	case core.ProviderOpenAI:
		return callOpenAI(spec.ID, system, user, maxTokens)
	case core.ProviderMock:
		return callMock(system, user), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", spec.Provider)
	}
}

// ─────────────────────────────────────────────
// Runtime
// ─────────────────────────────────────────────

type Runtime struct {
	spec    *core.AetherSpec
	plan    *core.ExecutionPlan
	memory  *memory.Orchestrator
	cache   *cache.Orchestrator
	obs     *observability.Store
	dataDir string
}

func New(
	spec *core.AetherSpec,
	plan *core.ExecutionPlan,
	dataDir string,
) *Runtime {
	return &Runtime{
		spec:    spec,
		plan:    plan,
		memory:  memory.NewOrchestrator(spec.Memory, dataDir+"/memory"),
		cache:   cache.NewOrchestrator(plan.CacheStrategy, dataDir+"/cache"),
		obs:     observability.NewStore(dataDir + "/observability"),
		dataDir: dataDir,
	}
}

func (r *Runtime) Run(userMessage string, variables map[string]string) *core.ExecutionResult {
	result := core.NewExecutionResult(r.plan.PlanID)
	t0 := time.Now()

	// 1. Retrieve memory
	memCtx := ""
	if r.plan.RetrievalStrategy != "none" {
		memCtx = r.memory.Retrieve(userMessage, 3)
	}

	// 2. Render prompt
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

	// 3. Cache lookup
	cacheKey := system + "|||" + user
	if cached, ok := r.cache.Lookup(cacheKey); ok {
		result.Output = cached
		result.ModelUsed = r.plan.SelectedModel
		result.Cached = true
		result.LatencyMS = float64(time.Since(t0).Milliseconds())
		result.Success = true
		r.obs.Record(&result)
		return &result
	}

	// 4. Execute with retry and fallback
	models := []string{r.plan.SelectedModel}
	models = append(models, r.spec.Policy.FallbackModels...)

	var lastErr error
	var mr *modelResponse
	usedModel := r.plan.SelectedModel

	for _, modelKey := range models {
		if _, exists := core.ModelCatalogue[modelKey]; !exists {
			continue
		}
		var attempt int
		for attempt = 0; attempt <= r.spec.Policy.MaxRetries; attempt++ {
			resp, err := dispatch(modelKey, system, user, 2048)
			if err == nil {
				mr = resp
				usedModel = modelKey
				break
			}
			lastErr = err
			if attempt < r.spec.Policy.MaxRetries {
				backoff := time.Duration(1<<uint(attempt)) * 500 * time.Millisecond
				time.Sleep(backoff)
			}
		}
		result.Retries += attempt
		if mr != nil {
			break
		}
	}

	if mr == nil {
		result.Success = false
		result.Error = fmt.Sprintf("%v", lastErr)
		result.ModelUsed = usedModel
		result.LatencyMS = float64(time.Since(t0).Milliseconds())
		r.obs.Record(&result)
		return &result
	}

	// 5. Calculate cost
	mSpec := core.ModelCatalogue[usedModel]
	cost := mSpec.CostEstimate(mr.InputTokens, mr.OutputTokens)

	// 6. Store memory
	if r.spec.Memory.LongTerm {
		summary := "Q: " + truncate(userMessage, 200) + "\nA: " + truncate(mr.Text, 200)
		r.memory.Store(summary, "long_term")
	}
	if r.spec.Memory.Semantic {
		r.memory.Store(truncate(mr.Text, 500), "semantic")
	}

	// 7. Cache response
	r.cache.Store(cacheKey, mr.Text)

	// 8. Build result
	result.Success = true
	result.Output = mr.Text
	result.ModelUsed = usedModel
	result.InputTokens = mr.InputTokens
	result.OutputTokens = mr.OutputTokens
	result.Cost = cost
	result.LatencyMS = float64(time.Since(t0).Milliseconds())

	r.obs.Record(&result)
	return &result
}

func (r *Runtime) Obs() *observability.Store {
	return r.obs
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
