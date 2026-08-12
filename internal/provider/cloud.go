package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/Orbis-Mind/aether/internal/core"
)

// This file holds OPTIONAL cloud providers. They implement the same
// ModelProvider interface as the local ones but are never required: without an
// API key set, they return a PermanentError telling the user how to enable them
// (or to use a local model instead). The default $0 experience never calls them.

// ─────────────────────────────────────────────
// OpenAI-compatible provider
// ─────────────────────────────────────────────

// OpenAIProvider targets the OpenAI chat-completions API. Because the API is a
// de-facto standard, it also works with any OpenAI-compatible endpoint (vLLM,
// LocalAI, llama.cpp server) by setting OPENAI_BASE_URL — keeping a local,
// free path open even here.
type OpenAIProvider struct {
	client *http.Client
}

// NewOpenAIProvider constructs the provider. It does not read or require a key.
func NewOpenAIProvider() *OpenAIProvider { return &OpenAIProvider{client: http.DefaultClient} }

func (p *OpenAIProvider) Name() string { return string(core.ProviderOpenAI) }

func (p *OpenAIProvider) Capabilities() Capabilities {
	return Capabilities{Streaming: false, Tools: true, Vision: true}
}

func (p *OpenAIProvider) baseURL() string {
	if v := os.Getenv("OPENAI_BASE_URL"); v != "" {
		return v
	}
	return "https://api.openai.com/v1"
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *OpenAIProvider) Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	// Local OpenAI-compatible servers often need no key; only require one for
	// the real hosted API.
	if apiKey == "" && os.Getenv("OPENAI_BASE_URL") == "" {
		return GenerateResponse{}, Permanent("OPENAI_API_KEY not set — export it, point OPENAI_BASE_URL at a local server, or use a local Ollama model")
	}
	msgs := []Message{}
	if req.System != "" {
		msgs = append(msgs, Message{Role: RoleSystem, Content: req.System})
	}
	msgs = append(msgs, req.Messages...)

	body, _ := json.Marshal(map[string]any{
		"model":       req.Model,
		"messages":    msgs,
		"max_tokens":  req.MaxTokens,
		"temperature": req.Temperature,
	})
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.baseURL()+"/chat/completions", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return GenerateResponse{}, fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var or openAIResponse
	json.Unmarshal(data, &or)
	if or.Error != nil {
		return GenerateResponse{}, fmt.Errorf("openai API error: %s", or.Error.Message)
	}
	if len(or.Choices) == 0 {
		return GenerateResponse{}, fmt.Errorf("openai: no choices returned")
	}
	return GenerateResponse{
		Text:          or.Choices[0].Message.Content,
		InputTokens:   or.Usage.PromptTokens,
		OutputTokens:  or.Usage.CompletionTokens,
		TokenCounting: TokensMeasured,
		FinishReason:  or.Choices[0].FinishReason,
	}, nil
}

// Stream falls back to a single Generate call wrapped as one event. A native
// SSE implementation can be added later without changing callers.
func (p *OpenAIProvider) Stream(ctx context.Context, req GenerateRequest) (<-chan StreamEvent, error) {
	return generateAsStream(ctx, p, req), nil
}

// ─────────────────────────────────────────────
// Anthropic provider
// ─────────────────────────────────────────────

// AnthropicProvider targets the Anthropic Messages API. Optional; requires
// ANTHROPIC_API_KEY.
type AnthropicProvider struct {
	client *http.Client
}

// NewAnthropicProvider constructs the provider without requiring a key.
func NewAnthropicProvider() *AnthropicProvider {
	return &AnthropicProvider{client: http.DefaultClient}
}

func (p *AnthropicProvider) Name() string { return string(core.ProviderAnthropic) }

func (p *AnthropicProvider) Capabilities() Capabilities {
	return Capabilities{Streaming: false, Tools: true, Vision: true}
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *AnthropicProvider) Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return GenerateResponse{}, Permanent("ANTHROPIC_API_KEY not set — export it or use a local Ollama model")
	}
	msgs := make([]map[string]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, map[string]string{"role": string(m.Role), "content": m.Content})
	}
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 1024
	}
	body, _ := json.Marshal(map[string]any{
		"model":      req.Model,
		"max_tokens": maxTok,
		"system":     req.System,
		"messages":   msgs,
	})
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return GenerateResponse{}, fmt.Errorf("anthropic: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var ar anthropicResponse
	json.Unmarshal(data, &ar)
	if ar.Error != nil {
		return GenerateResponse{}, fmt.Errorf("anthropic API error: %s", ar.Error.Message)
	}
	var text string
	for _, b := range ar.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	return GenerateResponse{
		Text:          text,
		InputTokens:   ar.Usage.InputTokens,
		OutputTokens:  ar.Usage.OutputTokens,
		TokenCounting: TokensMeasured,
		FinishReason:  ar.StopReason,
	}, nil
}

func (p *AnthropicProvider) Stream(ctx context.Context, req GenerateRequest) (<-chan StreamEvent, error) {
	return generateAsStream(ctx, p, req), nil
}

// generateAsStream adapts a blocking Generate into a single-event stream so any
// provider satisfies the streaming interface even before native SSE support.
func generateAsStream(ctx context.Context, p ModelProvider, req GenerateRequest) <-chan StreamEvent {
	ch := make(chan StreamEvent, 1)
	go func() {
		defer close(ch)
		resp, err := p.Generate(ctx, req)
		if err != nil {
			ch <- StreamEvent{Err: err}
			return
		}
		if resp.Text != "" {
			ch <- StreamEvent{Delta: resp.Text}
		}
		r := resp
		ch <- StreamEvent{Done: true, Final: &r}
	}()
	return ch
}
