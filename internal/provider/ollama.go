package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Orbis-Mind/aether/internal/core"
)

// classifyDoErr maps an *http.Client.Do error to a typed error. A cancelled or
// timed-out context is surfaced as the context error (so the runtime can stop
// or retry appropriately) rather than being mislabelled as "Ollama unreachable".
// Genuine connection failures remain permanent for the current run.
func classifyDoErr(ctx context.Context, host, model string, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		if errors.Is(cerr, context.DeadlineExceeded) {
			return Transient("ollama request to %s timed out", host)
		}
		return cerr // context.Canceled — propagate for cancellation handling
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return Permanent("cannot reach Ollama at %s — start it with `ollama serve` and pull a model (`ollama pull %s`)", host, model)
}

// OllamaProvider talks to a local Ollama daemon (http://localhost:11434 by
// default) over its OpenAI-adjacent chat API. Zero cost, fully offline once a
// model is pulled, and the default free-first backend.
type OllamaProvider struct {
	host   string
	client *http.Client

	mu          sync.Mutex
	installed   []LocalModel
	installedAt time.Time
}

// resolveModel maps a catalogue model name to the tag actually installed in
// Ollama. Catalogue IDs are often untagged (e.g. "qwen2.5"), but Ollama only
// auto-resolves untagged names to ":latest" — so a model installed as
// "qwen2.5:3b" would 404. When the name has no explicit tag and no exact match
// is installed, we substitute the first installed "name:*" tag. The installed
// list is cached briefly to avoid a lookup on every call.
func (o *OllamaProvider) resolveModel(ctx context.Context, name string) string {
	if strings.Contains(name, ":") {
		return name
	}
	o.mu.Lock()
	if o.installed == nil || time.Since(o.installedAt) > 10*time.Second {
		if ms, err := o.List(ctx); err == nil {
			o.installed, o.installedAt = ms, time.Now()
		}
	}
	installed := o.installed
	o.mu.Unlock()

	for _, m := range installed {
		if m.Name == name { // exact (untagged) match exists
			return name
		}
	}
	for _, m := range installed {
		if strings.HasPrefix(m.Name, name+":") { // e.g. qwen2.5 -> qwen2.5:3b
			return m.Name
		}
	}
	return name
}

// NewOllamaProvider builds a provider for the given host. An empty host uses
// the OLLAMA_HOST env var, then falls back to http://localhost:11434.
func NewOllamaProvider(host string) *OllamaProvider {
	return &OllamaProvider{
		host: resolveOllamaHost(host),
		// Local generation can be slow on first load (model paged into RAM/VRAM);
		// per-request context deadlines provide the real bound.
		client: &http.Client{Timeout: 0},
	}
}

func resolveOllamaHost(host string) string {
	if host == "" {
		host = os.Getenv("OLLAMA_HOST")
	}
	if host == "" {
		return "http://localhost:11434"
	}
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	return strings.TrimRight(host, "/")
}

// Host returns the resolved Ollama base URL.
func (o *OllamaProvider) Host() string { return o.host }

func (o *OllamaProvider) Name() string { return string(core.ProviderOllama) }

func (o *OllamaProvider) Capabilities() Capabilities {
	return Capabilities{Streaming: true, Tools: true, Local: true}
}

// HealthCheck probes the local Ollama daemon's /api/tags endpoint with a short
// timeout. A healthy daemon returns nil; otherwise a descriptive error.
func (o *OllamaProvider) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, "GET", o.host+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := o.client.Do(httpReq)
	if err != nil {
		return classifyDoErr(ctx, o.host, "", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama health check: HTTP %d from %s", resp.StatusCode, o.host)
	}
	return nil
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaOptions struct {
	NumPredict  int     `json:"num_predict,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
}

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  ollamaOptions   `json:"options"`
}

type ollamaChatChunk struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Done            bool   `json:"done"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Error           string `json:"error,omitempty"`
}

func (o *OllamaProvider) buildMessages(req GenerateRequest) []ollamaMessage {
	msgs := make([]ollamaMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, ollamaMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, ollamaMessage{Role: string(m.Role), Content: m.Content})
	}
	return msgs
}

func (o *OllamaProvider) countTokens(chunk ollamaChatChunk, req GenerateRequest, out string) (int, int, TokenCounting) {
	in := chunk.PromptEvalCount
	outTok := chunk.EvalCount
	counting := TokensMeasured
	if in == 0 {
		in = core.EstimateTokens(req.System)
		for _, m := range req.Messages {
			in += core.EstimateTokens(m.Content)
		}
		counting = TokensEstimated
	}
	if outTok == 0 {
		outTok = core.EstimateTokens(out)
		counting = TokensEstimated
	}
	return in, outTok, counting
}

func (o *OllamaProvider) Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error) {
	req.Model = o.resolveModel(ctx, req.Model)
	body, _ := json.Marshal(ollamaRequest{
		Model:    req.Model,
		Stream:   false,
		Messages: o.buildMessages(req),
		Options:  ollamaOptions{NumPredict: req.MaxTokens, Temperature: req.Temperature},
	})
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", o.host+"/api/chat", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return GenerateResponse{}, classifyDoErr(ctx, o.host, req.Model, err)
	}
	defer resp.Body.Close()

	var chunk ollamaChatChunk
	if err := json.NewDecoder(resp.Body).Decode(&chunk); err != nil {
		return GenerateResponse{}, fmt.Errorf("ollama: decoding response: %w", err)
	}
	if chunk.Error != "" {
		return GenerateResponse{}, Permanent("Ollama error: %s (try `ollama pull %s`)", chunk.Error, req.Model)
	}
	if resp.StatusCode != http.StatusOK {
		return GenerateResponse{}, fmt.Errorf("ollama HTTP %d", resp.StatusCode)
	}

	in, out, counting := o.countTokens(chunk, req, chunk.Message.Content)
	return GenerateResponse{
		Text:          chunk.Message.Content,
		InputTokens:   in,
		OutputTokens:  out,
		TokenCounting: counting,
		FinishReason:  "stop",
	}, nil
}

func (o *OllamaProvider) Stream(ctx context.Context, req GenerateRequest) (<-chan StreamEvent, error) {
	req.Model = o.resolveModel(ctx, req.Model)
	body, _ := json.Marshal(ollamaRequest{
		Model:    req.Model,
		Stream:   true,
		Messages: o.buildMessages(req),
		Options:  ollamaOptions{NumPredict: req.MaxTokens, Temperature: req.Temperature},
	})
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", o.host+"/api/chat", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, classifyDoErr(ctx, o.host, req.Model, err)
	}

	ch := make(chan StreamEvent)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		var full strings.Builder
		var last ollamaChatChunk
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			var chunk ollamaChatChunk
			if err := json.Unmarshal(line, &chunk); err != nil {
				continue
			}
			if chunk.Error != "" {
				ch <- StreamEvent{Err: Permanent("Ollama error: %s (try `ollama pull %s`)", chunk.Error, req.Model)}
				return
			}
			if chunk.Message.Content != "" {
				full.WriteString(chunk.Message.Content)
				ch <- StreamEvent{Delta: chunk.Message.Content}
			}
			if chunk.Done {
				last = chunk
			}
		}
		if err := sc.Err(); err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("ollama: stream read: %w", err)}
			return
		}
		in, out, counting := o.countTokens(last, req, full.String())
		ch <- StreamEvent{Done: true, Final: &GenerateResponse{
			Text:          full.String(),
			InputTokens:   in,
			OutputTokens:  out,
			TokenCounting: counting,
			FinishReason:  "stop",
		}}
	}()
	return ch, nil
}

// ─────────────────────────────────────────────
// Discovery
// ─────────────────────────────────────────────

// LocalModel describes a model actually installed in the local Ollama daemon.
type LocalModel struct {
	Name          string `json:"name"`
	SizeBytes     int64  `json:"size"`
	ParameterSize string `json:"parameter_size"`
	Quantization  string `json:"quantization"`
}

// SizeGB returns the on-disk footprint in gigabytes.
func (m LocalModel) SizeGB() float64 { return float64(m.SizeBytes) / 1e9 }

type ollamaTagsResponse struct {
	Models []struct {
		Name    string `json:"name"`
		Size    int64  `json:"size"`
		Details struct {
			ParameterSize     string `json:"parameter_size"`
			QuantizationLevel string `json:"quantization_level"`
		} `json:"details"`
	} `json:"models"`
}

// List queries the local Ollama daemon for installed models. It returns a clear
// error (no fabricated data) when the daemon is unreachable.
func (o *OllamaProvider) List(ctx context.Context) ([]LocalModel, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", o.host+"/api/tags", nil)
	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, classifyDoErr(ctx, o.host, "", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama /api/tags HTTP %d", resp.StatusCode)
	}
	var tags ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("ollama: decoding tags: %w", err)
	}
	out := make([]LocalModel, 0, len(tags.Models))
	for _, m := range tags.Models {
		out = append(out, LocalModel{
			Name:          m.Name,
			SizeBytes:     m.Size,
			ParameterSize: m.Details.ParameterSize,
			Quantization:  m.Details.QuantizationLevel,
		})
	}
	return out, nil
}
