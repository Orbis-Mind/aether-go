package provider

import (
	"context"
	"errors"
	"testing"
)

func TestMockHealthCheck(t *testing.T) {
	if err := NewMockProvider().HealthCheck(context.Background()); err != nil {
		t.Errorf("mock HealthCheck = %v, want nil", err)
	}
}

func TestCloudHealthCheckRequiresCredentials(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	if err := NewOpenAIProvider().HealthCheck(context.Background()); err == nil {
		t.Error("OpenAI HealthCheck should fail with no key and no base URL")
	} else if !IsPermanent(err) {
		t.Errorf("expected permanent error, got %v", err)
	}
	if err := NewAnthropicProvider().HealthCheck(context.Background()); err == nil {
		t.Error("Anthropic HealthCheck should fail with no key")
	}
}

func TestRegistryHealthCheckDispatch(t *testing.T) {
	r := NewRegistry()
	if err := r.HealthCheck(context.Background(), "mock"); err != nil {
		t.Errorf("mock registry health = %v, want nil", err)
	}
	if err := r.HealthCheck(context.Background(), "no-such-model"); err == nil {
		t.Error("expected error for unknown model key")
	}
}

func TestErrorClassification(t *testing.T) {
	if !IsPermanent(Permanent("boom")) {
		t.Error("Permanent should be permanent")
	}
	if IsTransient(Permanent("boom")) {
		t.Error("Permanent must not be transient")
	}
	if !IsTransient(Transient("timeout")) {
		t.Error("Transient should be transient")
	}
	if IsPermanent(Transient("timeout")) {
		t.Error("Transient must not be permanent")
	}
	if IsPermanent(nil) || IsTransient(nil) {
		t.Error("nil is neither permanent nor transient")
	}
}

// TestOllamaCancellationNotPermanent verifies a cancelled context yields a
// context error (not a permanent "unreachable"), so retries/propagation work.
func TestOllamaCancellationNotPermanent(t *testing.T) {
	// Point at an unroutable host and cancel immediately.
	o := NewOllamaProvider("http://127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := o.Generate(ctx, GenerateRequest{Model: "llama3.2", System: "s",
		Messages: []Message{{Role: RoleUser, Content: "hi"}}, MaxTokens: 8})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v (permanent=%v)", err, IsPermanent(err))
	}
	if IsPermanent(err) {
		t.Error("cancellation must not be classified permanent")
	}
}
