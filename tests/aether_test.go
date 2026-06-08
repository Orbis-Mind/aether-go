package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Orbis-Mind/aether/internal/cache"
	"github.com/Orbis-Mind/aether/internal/compiler"
	"github.com/Orbis-Mind/aether/internal/core"
	"github.com/Orbis-Mind/aether/internal/memory"
	"github.com/Orbis-Mind/aether/internal/observability"
	"github.com/Orbis-Mind/aether/internal/registry"
	"github.com/Orbis-Mind/aether/internal/router"
	"github.com/Orbis-Mind/aether/internal/runtime"
)

// ─────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────

func tmpDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "aether-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func writeSpec(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "agent.ae.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// ─────────────────────────────────────────────
// Core Tests
// ─────────────────────────────────────────────

func TestModelCatalogue(t *testing.T) {
	for _, key := range []string{"claude-haiku-3-5", "claude-sonnet-3-5", "claude-opus-4", "gpt-4o-mini", "gpt-4o", "mock"} {
		if _, ok := core.ModelCatalogue[key]; !ok {
			t.Errorf("missing model: %s", key)
		}
	}
}

func TestCostEstimate(t *testing.T) {
	mock := core.ModelCatalogue["mock"]
	if cost := mock.CostEstimate(1000, 500); cost != 0.0 {
		t.Errorf("mock cost should be 0, got %f", cost)
	}

	haiku := core.ModelCatalogue["claude-haiku-3-5"]
	cost := haiku.CostEstimate(1000, 1000)
	if cost <= 0 {
		t.Errorf("haiku cost should be > 0, got %f", cost)
	}
}

func TestEstimateTokens(t *testing.T) {
	tokens := core.EstimateTokens("hello world")
	if tokens < 1 {
		t.Error("token estimate should be >= 1")
	}
}

// ─────────────────────────────────────────────
// Router Tests
// ─────────────────────────────────────────────

func TestRouterSelectsModel(t *testing.T) {
	r := router.New()
	policy := core.DefaultPolicy()
	policy.MaxCostPerRun = 10.0

	selected, err := r.Select(core.ComplexityModerate, policy, 100, 100)
	if err != nil {
		t.Fatalf("router failed: %v", err)
	}
	if _, ok := core.ModelCatalogue[selected]; !ok {
		t.Errorf("selected unknown model: %s", selected)
	}
}

func TestRouterLowBudget(t *testing.T) {
	r := router.New()
	policy := core.DefaultPolicy()
	policy.MaxCostPerRun = 0.0001
	policy.Quality = core.QualityDraft

	selected, err := r.Select(core.ComplexityTrivial, policy, 10, 10)
	if err != nil {
		t.Fatalf("router failed: %v", err)
	}
	m := core.ModelCatalogue[selected]
	if m.CostEstimate(10, 10) > 0.0001 {
		t.Errorf("selected model %s exceeds budget", selected)
	}
}

func TestRouterExpertQuality(t *testing.T) {
	r := router.New()
	policy := core.DefaultPolicy()
	policy.MaxCostPerRun = 10.0
	policy.Quality = core.QualityExpert

	selected, err := r.Select(core.ComplexityExpert, policy, 500, 500)
	if err != nil {
		t.Fatalf("router failed: %v", err)
	}
	m := core.ModelCatalogue[selected]
	if m.QualityScore < 0.90 {
		t.Errorf("expert quality should pick high-quality model, got score %.2f", m.QualityScore)
	}
}

func TestRouterImpossibleConstraints(t *testing.T) {
	r := router.New()
	policy := core.DefaultPolicy()
	policy.MaxCostPerRun = 0.000001
	policy.Quality = core.QualityExpert

	_, err := r.Select(core.ComplexityExpert, policy, 500, 500)
	if err == nil {
		t.Error("expected routing error for impossible constraints")
	}
}

func TestRouterExplain(t *testing.T) {
	r := router.New()
	table := r.Explain(core.ComplexityModerate, core.DefaultPolicy(), 300, 200)
	if table == "" {
		t.Error("explain should return non-empty string")
	}
	if len(table) < 100 {
		t.Error("explain table seems too short")
	}
}

// ─────────────────────────────────────────────
// Compiler Tests
// ─────────────────────────────────────────────

func TestValidateMinimalSpec(t *testing.T) {
	dir := tmpDir(t)
	path := writeSpec(t, dir, "name: test\nversion: \"1.0\"\n")
	spec, err := compiler.ValidateYAML(path)
	if err != nil {
		t.Fatalf("validation failed: %v", err)
	}
	if spec.Name != "test" {
		t.Errorf("expected name 'test', got '%s'", spec.Name)
	}
}

func TestValidateMissingName(t *testing.T) {
	dir := tmpDir(t)
	path := writeSpec(t, dir, "version: \"1.0\"\n")
	_, err := compiler.ValidateYAML(path)
	if err == nil {
		t.Error("expected validation error for missing name")
	}
}

func TestValidateFullSpec(t *testing.T) {
	dir := tmpDir(t)
	content := `
name: full-agent
version: "2.0.0"
description: Full test
policy:
  max_cost_per_run: 0.02
  quality: high
  latency: fast
memory:
  semantic: true
  long_term: true
tools:
  - name: search
    description: Web search
prompts:
  system: You are helpful.
  user_template: "{user_message}"
`
	path := writeSpec(t, dir, content)
	spec, err := compiler.ValidateYAML(path)
	if err != nil {
		t.Fatalf("validation failed: %v", err)
	}
	if !spec.Memory.Semantic {
		t.Error("semantic memory should be true")
	}
	if len(spec.Tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(spec.Tools))
	}
}

func TestCompileProducesArtifact(t *testing.T) {
	dir := tmpDir(t)
	path := writeSpec(t, dir, "name: myagent\nversion: \"1.0\"\n")
	spec, _ := compiler.ValidateYAML(path)
	result, err := compiler.Compile(spec, dir, false)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if _, err := os.Stat(result.ArtifactPath); err != nil {
		t.Error("artifact file not created")
	}
}

func TestAEArtifactMagic(t *testing.T) {
	dir := tmpDir(t)
	path := writeSpec(t, dir, "name: x\nversion: \"1.0\"\n")
	spec, _ := compiler.ValidateYAML(path)
	result, _ := compiler.Compile(spec, dir, false)

	data, _ := os.ReadFile(result.ArtifactPath)
	magic := []byte("AETHER\x00\x01")
	for i, b := range magic {
		if data[i] != b {
			t.Errorf("bad magic byte at %d: expected %x, got %x", i, b, data[i])
		}
	}
}

func TestReadAEArtifact(t *testing.T) {
	dir := tmpDir(t)
	path := writeSpec(t, dir, "name: readtest\nversion: \"1.1\"\n")
	spec, _ := compiler.ValidateYAML(path)
	result, _ := compiler.Compile(spec, dir, false)

	ac, err := compiler.ReadAEArtifact(result.ArtifactPath)
	if err != nil {
		t.Fatalf("read artifact failed: %v", err)
	}
	if name, _ := ac.Manifest["name"].(string); name != "readtest" {
		t.Errorf("expected name 'readtest', got '%s'", name)
	}
	if ver, _ := ac.Manifest["version"].(string); ver != "1.1" {
		t.Errorf("expected version '1.1', got '%s'", ver)
	}
}

// ─────────────────────────────────────────────
// Memory Tests
// ─────────────────────────────────────────────

func TestVectorStoreAddSearch(t *testing.T) {
	vs := memory.NewVectorStore("")
	vs.Add("k1", "Python is a programming language", nil, 86400)
	vs.Add("k2", "Dogs are loyal animals", nil, 86400)

	results := vs.Search("coding with Python", 1, 0.0)
	if len(results) == 0 {
		t.Error("expected at least 1 result")
	}
}

func TestVectorStorePersist(t *testing.T) {
	dir := tmpDir(t)
	path := filepath.Join(dir, "store.json")

	vs1 := memory.NewVectorStore(path)
	vs1.Add("k1", "The sky is blue", nil, 86400)

	vs2 := memory.NewVectorStore(path)
	results := vs2.Search("sky color", 1, 0.0)
	if len(results) == 0 {
		t.Error("expected persisted entry to be found")
	}
}

func TestEntityMemory(t *testing.T) {
	em := memory.NewEntityMemory()
	em.Upsert("Alice", map[string]string{"role": "engineer"})
	e := em.Get("Alice")
	if e == nil {
		t.Fatal("entity not found")
	}
	if e["role"] != "engineer" {
		t.Errorf("expected role 'engineer', got '%s'", e["role"])
	}
}

func TestRelationshipMemory(t *testing.T) {
	rm := memory.NewRelationshipMemory()
	rm.Add("Alice", "works_with", "Bob", 1.0)
	ctx := rm.ToContext()
	if ctx == "" {
		t.Error("relationship context should not be empty")
	}
}

func TestMemoryOrchestrator(t *testing.T) {
	dir := tmpDir(t)
	cfg := core.MemoryConfig{
		Semantic: true, LongTerm: true, Entity: true,
		TTLSeconds: 86400,
	}
	orch := memory.NewOrchestrator(cfg, dir)
	orch.Store("Aether is an AI compiler", "semantic")
	ctx := orch.Retrieve("what is Aether", 3)
	_ = ctx // may or may not find results; just check no panic
	stats := orch.Stats()
	if stats["semantic_entries"] < 0 {
		t.Error("semantic entries should be >= 0")
	}
}

// ─────────────────────────────────────────────
// Cache Tests
// ─────────────────────────────────────────────

func TestExactCacheHit(t *testing.T) {
	c := cache.NewExactCache("")
	c.Set("hello world", "response A", 3600)
	val, ok := c.Get("hello world")
	if !ok {
		t.Error("expected cache hit")
	}
	if val != "response A" {
		t.Errorf("expected 'response A', got '%s'", val)
	}
}

func TestExactCacheMiss(t *testing.T) {
	c := cache.NewExactCache("")
	_, ok := c.Get("never stored")
	if ok {
		t.Error("expected cache miss")
	}
}

func TestExactCachePersist(t *testing.T) {
	dir := tmpDir(t)
	path := filepath.Join(dir, "cache.json")

	c1 := cache.NewExactCache(path)
	c1.Set("key", "val", 3600)

	c2 := cache.NewExactCache(path)
	val, ok := c2.Get("key")
	if !ok {
		t.Error("expected persisted cache hit")
	}
	if val != "val" {
		t.Errorf("expected 'val', got '%s'", val)
	}
}

func TestCacheOrchestrator(t *testing.T) {
	dir := tmpDir(t)
	orch := cache.NewOrchestrator("exact", dir)
	orch.Store("hello", "world")
	val, ok := orch.Lookup("hello")
	if !ok {
		t.Error("expected cache hit")
	}
	if val != "world" {
		t.Errorf("expected 'world', got '%s'", val)
	}
	_, ok = orch.Lookup("goodbye")
	if ok {
		t.Error("expected cache miss for 'goodbye'")
	}
}

// ─────────────────────────────────────────────
// Runtime Tests
// ─────────────────────────────────────────────

func makeRuntime(t *testing.T, dir string) (*runtime.Runtime, *core.AetherSpec, *core.ExecutionPlan) {
	t.Helper()
	spec := &core.AetherSpec{
		Name:    "test",
		Version: "1.0",
		Policy:  core.DefaultPolicy(),
		Memory:  core.MemoryConfig{Semantic: true, TTLSeconds: 86400},
		Prompts: map[string]string{
			"system":        "You are a test assistant.",
			"user_template": "{user_message}",
		},
	}
	plan := core.NewExecutionPlan()
	plan.SpecName = "test"
	plan.SpecVersion = "1.0"
	plan.SelectedModel = "mock"
	plan.OptimizedSystemPrompt = "You are a test assistant."
	plan.OptimizedUserTemplate = "{user_message}"
	plan.RetrievalStrategy = "none"
	plan.CacheStrategy = "exact"
	plan.Complexity = core.ComplexitySimple

	rt := runtime.New(spec, &plan, dir)
	return rt, spec, &plan
}

func TestRuntimeSuccessfulRun(t *testing.T) {
	dir := tmpDir(t)
	rt, _, _ := makeRuntime(t, dir)
	result := rt.Run("Hello world", nil)
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	if result.Output == "" {
		t.Error("output should not be empty")
	}
	if result.ModelUsed != "mock" {
		t.Errorf("expected mock model, got %s", result.ModelUsed)
	}
}

func TestRuntimeCacheHit(t *testing.T) {
	dir := tmpDir(t)
	rt, _, _ := makeRuntime(t, dir)
	rt.Run("Same question", nil)
	r2 := rt.Run("Same question", nil)
	if !r2.Cached {
		t.Error("second run should be cached")
	}
	if r2.Cost != 0 {
		t.Error("cached run should have zero cost")
	}
}

func TestRuntimeRecordsObservability(t *testing.T) {
	dir := tmpDir(t)
	rt, _, _ := makeRuntime(t, dir)
	rt.Run("Test message", nil)
	summary := rt.Obs().Summary()
	totalRuns, _ := summary["total_runs"].(int)
	if totalRuns < 1 {
		t.Error("expected at least 1 run recorded")
	}
}

// ─────────────────────────────────────────────
// Observability Tests
// ─────────────────────────────────────────────

func TestObservabilityRecordAndSummary(t *testing.T) {
	dir := tmpDir(t)
	store := observability.NewStore(dir)

	for i := 0; i < 3; i++ {
		result := core.NewExecutionResult("plan1")
		result.Success = true
		result.ModelUsed = "mock"
		result.InputTokens = 100
		result.OutputTokens = 50
		result.Cost = 0.001
		result.LatencyMS = 15.0
		store.Record(&result)
	}

	summary := store.Summary()
	if summary["total_runs"].(int) != 3 {
		t.Errorf("expected 3 runs, got %v", summary["total_runs"])
	}
	if summary["success_rate"].(string) != "100.0%" {
		t.Errorf("expected 100%% success rate, got %v", summary["success_rate"])
	}
}

func TestObservabilityRenderReport(t *testing.T) {
	dir := tmpDir(t)
	store := observability.NewStore(dir)
	result := core.NewExecutionResult("p1")
	result.Success = true
	result.ModelUsed = "mock"
	result.LatencyMS = 10.0
	store.Record(&result)

	report := observability.RenderReport(store)
	if report == "" {
		t.Error("report should not be empty")
	}
	if len(report) < 50 {
		t.Error("report seems too short")
	}
}

// ─────────────────────────────────────────────
// Registry Tests
// ─────────────────────────────────────────────

func makeArtifact(t *testing.T, dir, name, version string) string {
	t.Helper()
	specContent := "name: " + name + "\nversion: \"" + version + "\"\n"
	specPath := writeSpec(t, dir, specContent)
	spec, err := compiler.ValidateYAML(specPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(spec, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return result.ArtifactPath
}

func TestRegistryPublishAndPull(t *testing.T) {
	dir := tmpDir(t)
	reg := registry.New(filepath.Join(dir, "registry"))
	ae := makeArtifact(t, dir, "test-pkg", "1.0.0")

	entry, err := reg.Publish(ae, "test-pkg", "1.0.0", "Test package", "tester", nil)
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	if entry.Name != "test-pkg" {
		t.Errorf("expected name 'test-pkg', got '%s'", entry.Name)
	}

	pullDir := filepath.Join(dir, "pulled")
	os.MkdirAll(pullDir, 0755)
	dest, err := reg.Pull("test-pkg", "", pullDir)
	if err != nil {
		t.Fatalf("pull failed: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Error("pulled artifact not found")
	}
}

func TestRegistryVersionCollision(t *testing.T) {
	dir := tmpDir(t)
	reg := registry.New(filepath.Join(dir, "registry"))
	ae := makeArtifact(t, dir, "mypkg", "1.0.0")

	reg.Publish(ae, "mypkg", "1.0.0", "", "", nil)
	_, err := reg.Publish(ae, "mypkg", "1.0.0", "", "", nil)
	if err == nil {
		t.Error("expected error on duplicate version")
	}
}

func TestRegistrySearch(t *testing.T) {
	dir := tmpDir(t)
	reg := registry.New(filepath.Join(dir, "registry"))

	ae1 := makeArtifact(t, dir, "nlp-agent", "1.0.0")
	ae2 := makeArtifact(t, dir, "code-agent", "1.0.0")
	reg.Publish(ae1, "nlp-agent", "1.0.0", "NLP tasks", "", nil)
	reg.Publish(ae2, "code-agent", "1.0.0", "Code generation", "", nil)

	results := reg.Search("nlp", nil)
	found := false
	for _, r := range results {
		if r.Name == "nlp-agent" {
			found = true
		}
	}
	if !found {
		t.Error("search 'nlp' should find nlp-agent")
	}

	all := reg.Search("", nil)
	if len(all) != 2 {
		t.Errorf("expected 2 results, got %d", len(all))
	}
}

func TestRegistryStats(t *testing.T) {
	dir := tmpDir(t)
	reg := registry.New(filepath.Join(dir, "registry"))
	ae := makeArtifact(t, dir, "pkg", "1.0.0")
	reg.Publish(ae, "pkg", "1.0.0", "", "", nil)

	stats := reg.Stats()
	if stats["total_packages"] != 1 {
		t.Errorf("expected 1 package, got %d", stats["total_packages"])
	}
}
