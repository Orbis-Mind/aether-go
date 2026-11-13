package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Orbis-Mind/aether/internal/core"
)

// MockProvider is a deterministic, offline provider used for tests, benchmarks
// of the runtime itself, and `--mock` runs. It never touches the network.
type MockProvider struct{}

// NewMockProvider constructs a MockProvider.
func NewMockProvider() *MockProvider { return &MockProvider{} }

func (m *MockProvider) Name() string { return string(core.ProviderMock) }

func (m *MockProvider) Capabilities() Capabilities {
	return Capabilities{Streaming: true, Tools: true, Local: true}
}

// HealthCheck always succeeds (unless ctx is already done): the mock provider is
// always available and offline.
func (m *MockProvider) HealthCheck(ctx context.Context) error { return ctx.Err() }

func lastUser(req GenerateRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == RoleUser {
			return req.Messages[i].Content
		}
	}
	return ""
}

func (m *MockProvider) render(req GenerateRequest) string {
	user := lastUser(req)
	preview := user
	if len(preview) > 100 {
		preview = preview[:100] + "..."
	}
	return fmt.Sprintf(
		"[MOCK RESPONSE]\nSystem context received (%d chars).\nUser message: %s\n"+
			"This is a mock response from the Aether runtime.\n"+
			"In production, a real model would generate the actual answer here.",
		len(req.System), preview,
	)
}

func (m *MockProvider) Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error) {
	select {
	case <-ctx.Done():
		return GenerateResponse{}, ctx.Err()
	case <-time.After(10 * time.Millisecond):
	}
	text := m.render(req)
	return GenerateResponse{
		Text:          text,
		InputTokens:   core.EstimateTokens(req.System) + core.EstimateTokens(lastUser(req)),
		OutputTokens:  core.EstimateTokens(text),
		TokenCounting: TokensEstimated,
		FinishReason:  "stop",
	}, nil
}

func (m *MockProvider) Stream(ctx context.Context, req GenerateRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent)
	text := m.render(req)
	go func() {
		defer close(ch)
		for _, word := range strings.SplitAfter(text, " ") {
			select {
			case <-ctx.Done():
				ch <- StreamEvent{Err: ctx.Err()}
				return
			case <-time.After(3 * time.Millisecond):
			}
			ch <- StreamEvent{Delta: word}
		}
		ch <- StreamEvent{Done: true, Final: &GenerateResponse{
			Text:          text,
			InputTokens:   core.EstimateTokens(req.System) + core.EstimateTokens(lastUser(req)),
			OutputTokens:  core.EstimateTokens(text),
			TokenCounting: TokensEstimated,
			FinishReason:  "stop",
		}}
	}()
	return ch, nil
}
