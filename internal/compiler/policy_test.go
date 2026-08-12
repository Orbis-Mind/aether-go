package compiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Orbis-Mind/aether/internal/core"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.ae.yaml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseNestedPolicy(t *testing.T) {
	spec, err := ValidateYAML(writeTemp(t, `
name: nested
version: "1.0"
policy:
  quality:
    minimum: 0.9
  cost:
    max_per_run: 0.01
  latency:
    max_p95: 2000
  privacy:
    level: local
  execution:
    local_first: true
  fallback:
    enabled: true
    max_attempts: 3
`))
	if err != nil {
		t.Fatal(err)
	}
	p := spec.Policy
	if p.QualityMinimum != 0.9 {
		t.Errorf("quality.minimum = %v, want 0.9", p.QualityMinimum)
	}
	if p.MaxCostPerRun != 0.01 {
		t.Errorf("cost.max_per_run = %v, want 0.01", p.MaxCostPerRun)
	}
	if p.LatencyMaxP95 != 2000 {
		t.Errorf("latency.max_p95 = %v, want 2000", p.LatencyMaxP95)
	}
	if p.Privacy != core.PrivacyLocal {
		t.Errorf("privacy.level = %v, want local", p.Privacy)
	}
	if !p.LocalFirst {
		t.Error("execution.local_first should be true")
	}
	if !p.FallbackEnabled || p.FallbackMaxAttempts != 3 {
		t.Errorf("fallback parsing wrong: enabled=%v max=%d", p.FallbackEnabled, p.FallbackMaxAttempts)
	}
	if !p.LocalOnly() {
		t.Error("privacy=local must make policy LocalOnly")
	}
}

func TestParseFlatPolicyStillWorks(t *testing.T) {
	spec, err := ValidateYAML(writeTemp(t, `
name: flat
version: "1.0"
policy:
  quality: high
  max_cost_per_run: 0.05
  latency: fast
  prefer_local: true
`))
	if err != nil {
		t.Fatal(err)
	}
	p := spec.Policy
	if p.Quality != core.QualityHigh {
		t.Errorf("flat quality = %v, want high", p.Quality)
	}
	if p.MaxCostPerRun != 0.05 {
		t.Errorf("flat cost = %v, want 0.05", p.MaxCostPerRun)
	}
	if !p.PreferLocal {
		t.Error("prefer_local should be true")
	}
}

func TestCompileLocalPolicyPinsLocalModel(t *testing.T) {
	spec, err := ValidateYAML(writeTemp(t, `
name: localagent
version: "1.0"
policy:
  privacy:
    level: local
prompts:
  system: You are helpful.
  user_template: "{user_message}"
`))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	res, err := Compile(spec, dir, false)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	m := core.ModelCatalogue[res.Plan.SelectedModel]
	if !m.IsLocal() {
		t.Errorf("local-only policy pinned non-local model %s", res.Plan.SelectedModel)
	}
}
