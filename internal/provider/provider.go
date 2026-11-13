// Package provider defines the ModelProvider abstraction: a small, stable
// interface every model backend implements (local or cloud). The runtime and
// evaluator talk to providers only through this interface, so new backends can
// be added without touching the core routing/runtime code.
//
// Free/local providers (Ollama, Mock) are first-class. Cloud providers
// (OpenAI, Anthropic) implement the same interface but are never required.
package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Orbis-Mind/aether/internal/core"
)

// Role identifies who authored a message in a conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn in a conversation.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// GenerateRequest is a provider-agnostic generation request.
type GenerateRequest struct {
	// Model is the provider-native model identifier (e.g. "llama3.2",
	// "gpt-4o-mini"), not the Aether catalogue key.
	Model       string
	System      string
	Messages    []Message
	MaxTokens   int
	Temperature float64
}

// TokenCounting records whether token counts came back from the provider
// (measured) or had to be estimated locally. We never present an estimate as a
// measurement.
type TokenCounting string

const (
	TokensMeasured  TokenCounting = "measured"
	TokensEstimated TokenCounting = "estimated"
)

// GenerateResponse is the result of a non-streaming generation.
type GenerateResponse struct {
	Text          string
	InputTokens   int
	OutputTokens  int
	TokenCounting TokenCounting
	// FinishReason is provider-specific when available (e.g. "stop", "length").
	FinishReason string
}

// StreamEvent is emitted incrementally during streaming. Exactly one of Delta,
// Err, or (Done + Final) is meaningful per event:
//   - Delta: a chunk of newly generated text.
//   - Done: the stream finished successfully; Final holds token accounting.
//   - Err: the stream failed.
type StreamEvent struct {
	Delta string
	Done  bool
	Err   error
	Final *GenerateResponse
}

// Capabilities describes what a provider/model can do. Used by the router for
// capability matching (e.g. exclude non-tool models for agentic tasks).
type Capabilities struct {
	Streaming bool
	Tools     bool
	Vision    bool
	Audio     bool
	// Local is true when generation runs on the user's machine at zero cost
	// with no network egress of prompt data.
	Local bool
}

// ModelProvider is the single interface every backend implements.
type ModelProvider interface {
	Name() string
	Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error)
	Stream(ctx context.Context, req GenerateRequest) (<-chan StreamEvent, error)
	Capabilities() Capabilities
	// HealthCheck reports whether the provider is reachable and ready to serve
	// (daemon up, required credentials present). It returns nil when healthy and
	// a descriptive error otherwise. It must honour ctx for cancellation/timeout.
	HealthCheck(ctx context.Context) error
}

// ─────────────────────────────────────────────
// Registry
// ─────────────────────────────────────────────

// Registry maps provider names (e.g. "ollama") to their implementation. It is
// safe for concurrent use.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]ModelProvider
}

// NewRegistry builds a registry pre-populated with every free/local and
// optional-cloud provider. Cloud providers are registered but only actually
// reachable when their API key env var is set — the constructor never requires
// one, keeping the default experience $0.
func NewRegistry() *Registry {
	r := &Registry{providers: make(map[string]ModelProvider)}
	r.Register(NewMockProvider())
	r.Register(NewOllamaProvider(""))
	r.Register(NewOpenAIProvider())
	r.Register(NewAnthropicProvider())
	return r
}

// Register adds or replaces a provider.
func (r *Registry) Register(p ModelProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
}

// Get returns the provider registered under name.
func (r *Registry) Get(name string) (ModelProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// forModelKey resolves an Aether catalogue key to its provider and native model
// ID. This is the single place that bridges the catalogue to providers.
func (r *Registry) forModelKey(modelKey string) (ModelProvider, string, error) {
	spec, ok := core.ModelCatalogue[modelKey]
	if !ok {
		return nil, "", fmt.Errorf("unknown model key: %s", modelKey)
	}
	p, ok := r.Get(string(spec.Provider))
	if !ok {
		return nil, "", fmt.Errorf("no provider registered for %q", spec.Provider)
	}
	return p, spec.ID, nil
}

// Generate dispatches a catalogue-keyed generation to the right provider.
func (r *Registry) Generate(ctx context.Context, modelKey, system, user string, maxTokens int) (GenerateResponse, error) {
	p, modelID, err := r.forModelKey(modelKey)
	if err != nil {
		return GenerateResponse{}, err
	}
	return p.Generate(ctx, GenerateRequest{
		Model:     modelID,
		System:    system,
		Messages:  []Message{{Role: RoleUser, Content: user}},
		MaxTokens: maxTokens,
	})
}

// Stream dispatches a catalogue-keyed streaming generation.
func (r *Registry) Stream(ctx context.Context, modelKey, system, user string, maxTokens int) (<-chan StreamEvent, error) {
	p, modelID, err := r.forModelKey(modelKey)
	if err != nil {
		return nil, err
	}
	return p.Stream(ctx, GenerateRequest{
		Model:     modelID,
		System:    system,
		Messages:  []Message{{Role: RoleUser, Content: user}},
		MaxTokens: maxTokens,
	})
}

// ─────────────────────────────────────────────
// Error classification
// ─────────────────────────────────────────────

// PermanentError marks a failure that retrying cannot fix (missing API key,
// auth failure, model not pulled). The runtime fails fast on these instead of
// wasting exponential backoff, and moves on to a fallback model.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err as a PermanentError.
func Permanent(format string, a ...any) error {
	return &PermanentError{Err: fmt.Errorf(format, a...)}
}

// IsPermanent reports whether err (or anything it wraps) is a PermanentError.
func IsPermanent(err error) bool {
	if err == nil {
		return false
	}
	var p *PermanentError
	return errors.As(err, &p)
}

// TransientError marks a retryable failure (timeout, temporary network error,
// provider 5xx). The runtime retries these with backoff rather than failing
// fast as it does for PermanentError.
type TransientError struct{ Err error }

func (e *TransientError) Error() string { return e.Err.Error() }
func (e *TransientError) Unwrap() error { return e.Err }

// Transient wraps err as a TransientError.
func Transient(format string, a ...any) error {
	return &TransientError{Err: fmt.Errorf(format, a...)}
}

// IsTransient reports whether err (or anything it wraps) is a TransientError.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var t *TransientError
	return errors.As(err, &t)
}

// HealthCheck dispatches a readiness probe to the provider backing modelKey.
func (r *Registry) HealthCheck(ctx context.Context, modelKey string) error {
	p, _, err := r.forModelKey(modelKey)
	if err != nil {
		return err
	}
	return p.HealthCheck(ctx)
}
