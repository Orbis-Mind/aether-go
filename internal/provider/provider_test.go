package provider

import (
	"context"
	"strings"
	"testing"
)

func TestMockGenerate(t *testing.T) {
	m := NewMockProvider()
	resp, err := m.Generate(context.Background(), GenerateRequest{
		System:   "sys",
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Text, "MOCK") {
		t.Errorf("expected mock text, got %q", resp.Text)
	}
	if resp.TokenCounting != TokensEstimated {
		t.Errorf("mock tokens should be estimated, got %s", resp.TokenCounting)
	}
}

func TestMockStreamAssemblesFullText(t *testing.T) {
	m := NewMockProvider()
	ch, err := m.Stream(context.Background(), GenerateRequest{
		Messages: []Message{{Role: RoleUser, Content: "hello world"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var assembled strings.Builder
	var final *GenerateResponse
	for ev := range ch {
		if ev.Err != nil {
			t.Fatal(ev.Err)
		}
		assembled.WriteString(ev.Delta)
		if ev.Done {
			final = ev.Final
		}
	}
	if final == nil {
		t.Fatal("stream never signalled Done with a Final")
	}
	if assembled.String() != final.Text {
		t.Errorf("assembled deltas %q != final text %q", assembled.String(), final.Text)
	}
}

func TestRegistryDispatchByKey(t *testing.T) {
	reg := NewRegistry()
	resp, err := reg.Generate(context.Background(), "mock", "sys", "hi", 64)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text == "" {
		t.Error("expected non-empty mock response via registry")
	}
	if _, err := reg.Generate(context.Background(), "does-not-exist", "", "", 0); err == nil {
		t.Error("expected error for unknown model key")
	}
}

func TestIsPermanent(t *testing.T) {
	if !IsPermanent(Permanent("boom")) {
		t.Error("Permanent error should be detected as permanent")
	}
	if IsPermanent(context.Canceled) {
		t.Error("context.Canceled is not a permanent provider error")
	}
	if IsPermanent(nil) {
		t.Error("nil is not permanent")
	}
}
