package compiler

import (
	"strings"
	"testing"
)

// TestNestedPolicyFlowForm proves the documented nested flow-form policy now
// actually parses (previously it was silently stored as a garbage string tier).
func TestNestedPolicyFlowForm(t *testing.T) {
	src := "name: n\nversion: \"1.0\"\npolicy:\n" +
		"  quality: {minimum: 0.9}\n" +
		"  cost: {max_per_run: 0.01}\n" +
		"  latency: {max_p95: 2000}\n" +
		"  privacy: {level: local}\n"
	spec, err := ValidateYAML(writeTemp(t, src))
	if err != nil {
		t.Fatalf("ValidateYAML: %v", err)
	}
	if spec.Policy.QualityMinimum != 0.9 {
		t.Errorf("QualityMinimum = %v, want 0.9", spec.Policy.QualityMinimum)
	}
	if spec.Policy.MaxCostPerRun != 0.01 {
		t.Errorf("MaxCostPerRun = %v, want 0.01", spec.Policy.MaxCostPerRun)
	}
	if spec.Policy.LatencyMaxP95 != 2000 {
		t.Errorf("LatencyMaxP95 = %v, want 2000", spec.Policy.LatencyMaxP95)
	}
	if !spec.Policy.LocalOnly() {
		t.Errorf("privacy level=local not applied (LocalOnly=false)")
	}
	// The quality tier must NOT be the raw flow string anymore.
	if strings.Contains(string(spec.Policy.Quality), "{") {
		t.Errorf("quality tier captured raw flow text: %q", spec.Policy.Quality)
	}
}

func TestSchemaRejectsUnknownTopLevelKey(t *testing.T) {
	src := "name: n\nversion: \"1.0\"\nqualdity: high\n"
	if _, err := ValidateYAML(writeTemp(t, src)); err == nil {
		t.Fatal("expected error for unknown top-level key, got nil")
	}
}

func TestSchemaRejectsUnknownPolicyKey(t *testing.T) {
	src := "name: n\nversion: \"1.0\"\npolicy:\n  max_retriez: 2\n"
	_, err := ValidateYAML(writeTemp(t, src))
	if err == nil {
		t.Fatal("expected error for unknown policy key, got nil")
	}
	if !strings.Contains(err.Error(), "max_retriez") {
		t.Errorf("error should name the offending key, got: %v", err)
	}
}

func TestSchemaAcceptsKnownKeys(t *testing.T) {
	src := "name: n\nversion: \"1.0\"\ndescription: d\n" +
		"policy:\n  max_cost_per_run: 0.1\n  quality: high\n  max_retries: 2\n" +
		"memory:\n  semantic: true\n  ttl_seconds: 3600\n" +
		"tools:\n  - name: t\n    description: d\n" +
		"prompts:\n  system: hi\n"
	if _, err := ValidateYAML(writeTemp(t, src)); err != nil {
		t.Fatalf("known-key spec rejected: %v", err)
	}
}
