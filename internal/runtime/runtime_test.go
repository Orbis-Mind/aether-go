package runtime

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Orbis-Mind/aether/internal/core"
)

func mockPlan() *core.ExecutionPlan {
	p := core.NewExecutionPlan()
	p.SelectedModel = "mock"
	p.OptimizedSystemPrompt = "You are a test assistant."
	p.OptimizedUserTemplate = "{user_message}"
	p.RetrievalStrategy = "none"
	p.CacheStrategy = "exact"
	p.Complexity = core.ComplexitySimple
	return &p
}

func mockSpec() *core.AetherSpec {
	return &core.AetherSpec{
		Name: "t", Version: "1.0", Policy: core.DefaultPolicy(),
		Prompts: map[string]string{"system": "s", "user_template": "{user_message}"},
	}
}

func TestFallbackChainRecoversFromPermanentFailure(t *testing.T) {
	dir := t.TempDir()
	spec := mockSpec()
	// Primary is a cloud model with no API key → permanent failure. Fallback to
	// the always-available mock.
	spec.Policy.FallbackModels = []string{"mock"}
	spec.Policy.MaxRetries = 0
	plan := mockPlan()
	plan.SelectedModel = "gpt-4o-mini"

	rt := New(spec, plan, dir)
	res := rt.Run("hello", nil)
	if !res.Success {
		t.Fatalf("expected fallback to succeed, got error: %s", res.Error)
	}
	if res.ModelUsed != "mock" {
		t.Errorf("expected fallback model 'mock', got %s", res.ModelUsed)
	}
}

func TestPermanentFailureNoFallbackReportsError(t *testing.T) {
	dir := t.TempDir()
	spec := mockSpec()
	spec.Policy.MaxRetries = 0
	spec.Policy.FallbackEnabled = false
	spec.Policy.FallbackModels = nil
	plan := mockPlan()
	plan.SelectedModel = "gpt-4o-mini" // no key → permanent

	rt := New(spec, plan, dir)
	res := rt.Run("hello", nil)
	if res.Success {
		t.Error("expected failure with no fallback and no API key")
	}
	if res.Error == "" {
		t.Error("failure must surface an explicit error, not be swallowed")
	}
}

func TestCacheHitOnRepeat(t *testing.T) {
	dir := t.TempDir()
	rt := New(mockSpec(), mockPlan(), dir)
	rt.Run("same question", nil)
	r2 := rt.Run("same question", nil)
	if !r2.Cached {
		t.Error("second identical run should be served from cache")
	}
	if r2.Cost != 0 {
		t.Error("cached run should cost nothing")
	}
}

func TestTimeSensitivePromptNotCached(t *testing.T) {
	dir := t.TempDir()
	rt := New(mockSpec(), mockPlan(), dir)
	rt.Run("what is the weather today", nil)
	r2 := rt.Run("what is the weather today", nil)
	if r2.Cached {
		t.Error("time-sensitive prompt must not be served from cache")
	}
}

func TestStreamAssemblesOutput(t *testing.T) {
	dir := t.TempDir()
	rt := New(mockSpec(), mockPlan(), dir)
	var buf bytes.Buffer
	res := rt.RunStream("hello world", nil, &buf)
	if !res.Success {
		t.Fatalf("stream failed: %s", res.Error)
	}
	if !strings.Contains(buf.String(), "MOCK") {
		t.Errorf("streamed output should contain the response, got %q", buf.String())
	}
	if buf.String() != res.Output {
		t.Error("streamed bytes should equal the final assembled output")
	}
}
