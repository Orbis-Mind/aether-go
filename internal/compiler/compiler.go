package compiler

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Orbis-Mind/aether/internal/core"
	"github.com/Orbis-Mind/aether/internal/router"
	"github.com/Orbis-Mind/aether/internal/yamlx"
)

var aeMagic = []byte("AETHER\x00\x01")

type ValidationError struct{ msg string }
func (e ValidationError) Error() string { return "validation error: " + e.msg }

func ValidateYAML(path string) (*core.AetherSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil { return nil, fmt.Errorf("cannot read spec: %w", err) }

	raw, err := yamlx.Unmarshal(data)
	if err != nil { return nil, fmt.Errorf("invalid YAML: %w", err) }

	name := yamlx.GetString(raw, "name")
	version := yamlx.GetString(raw, "version")
	if name == "" { return nil, ValidationError{"field 'name' required"} }
	if version == "" { return nil, ValidationError{"field 'version' required"} }

	spec := &core.AetherSpec{
		Name:        name,
		Version:     version,
		Description: yamlx.GetString(raw, "description"),
		Policy:      core.DefaultPolicy(),
		Memory:      core.DefaultMemoryConfig(),
		Prompts:     make(map[string]string),
		Metadata:    make(map[string]interface{}),
	}

	if pm := yamlx.GetMap(raw, "policy"); pm != nil {
		if v := yamlx.GetFloat(pm, "max_cost_per_run", -1); v >= 0 { spec.Policy.MaxCostPerRun = v }
		if v := yamlx.GetString(pm, "quality"); v != "" { spec.Policy.Quality = core.QualityLevel(v) }
		if v := yamlx.GetString(pm, "latency"); v != "" { spec.Policy.Latency = core.LatencyProfile(v) }
		if v := yamlx.GetInt(pm, "max_retries", -1); v >= 0 { spec.Policy.MaxRetries = v }
		for _, fb := range yamlx.GetSlice(pm, "fallback_models") {
			if s, ok := fb.(string); ok { spec.Policy.FallbackModels = append(spec.Policy.FallbackModels, s) }
		}
		for _, ap := range yamlx.GetSlice(pm, "allowed_providers") {
			if s, ok := ap.(string); ok { spec.Policy.AllowedProviders = append(spec.Policy.AllowedProviders, s) }
		}
		spec.Policy.PreferLocal = yamlx.GetBool(pm, "prefer_local")
	}

	if mm := yamlx.GetMap(raw, "memory"); mm != nil {
		spec.Memory.Semantic = yamlx.GetBool(mm, "semantic")
		spec.Memory.LongTerm = yamlx.GetBool(mm, "long_term")
		spec.Memory.Entity = yamlx.GetBool(mm, "entity")
		spec.Memory.Relationship = yamlx.GetBool(mm, "relationship")
		if v := yamlx.GetInt(mm, "ttl_seconds", 0); v > 0 { spec.Memory.TTLSeconds = v }
	}

	for _, t := range yamlx.GetSlice(raw, "tools") {
		if tm, ok := t.(map[string]interface{}); ok {
			tool := core.ToolDefinition{
				Name:        yamlx.GetString(tm, "name"),
				Description: yamlx.GetString(tm, "description"),
			}
			spec.Tools = append(spec.Tools, tool)
		}
	}

	if pm := yamlx.GetMap(raw, "prompts"); pm != nil {
		for k, v := range pm {
			if s, ok := v.(string); ok { spec.Prompts[k] = s }
		}
	}

	return spec, nil
}

var (
	multiNewline  = regexp.MustCompile(`\n{3,}`)
	multiSpace    = regexp.MustCompile(`[ \t]+`)
	fillerPhrases = regexp.MustCompile(`(?i)(please\s+|kindly\s+|I would like you to\s+|Could you please\s+)`)
)

func compressPrompt(text string) (string, int, int) {
	orig := core.EstimateTokens(text)
	p := multiNewline.ReplaceAllString(text, "\n\n")
	p = multiSpace.ReplaceAllString(p, " ")
	p = fillerPhrases.ReplaceAllString(p, "")
	p = strings.TrimSpace(p)
	return p, orig, core.EstimateTokens(p)
}

var (
	expertKW  = []string{"synthesize","compare","evaluate","critique","strategy","design","architect","research","analyze","reason"}
	complexKW = []string{"explain","summarize","describe","generate","write","create","build","implement","solve","debug"}
	simpleKW  = []string{"list","extract","format","convert","translate","classify","label","tag"}
	trivialKW = []string{"check","validate","count","find","lookup","get","fetch"}
)

func analyzeComplexity(system, user string, tools []core.ToolDefinition) core.TaskComplexity {
	combined := strings.ToLower(system + " " + user)
	for _, kw := range expertKW { if strings.Contains(combined, kw) { return core.ComplexityExpert } }
	if len(tools) > 0 { return core.ComplexityComplex }
	for _, kw := range complexKW { if strings.Contains(combined, kw) { return core.ComplexityComplex } }
	for _, kw := range simpleKW { if strings.Contains(combined, kw) { return core.ComplexitySimple } }
	for _, kw := range trivialKW { if strings.Contains(combined, kw) { return core.ComplexityTrivial } }
	return core.ComplexityModerate
}

var outTokens = map[core.TaskComplexity]int{
	core.ComplexityTrivial: 50, core.ComplexitySimple: 150,
	core.ComplexityModerate: 400, core.ComplexityComplex: 800, core.ComplexityExpert: 1500,
}

type CompileResult struct {
	Plan         *core.ExecutionPlan
	ArtifactPath string
	TokensSaved  int
	ElapsedMS    int64
}

func Compile(spec *core.AetherSpec, outputDir string, verbose bool) (*CompileResult, error) {
	t0 := time.Now()

	system := spec.Prompts["system"]
	if system == "" { system = "You are a helpful AI assistant." }
	user := spec.Prompts["user_template"]
	if user == "" { user = "{user_message}" }

	sysOpt, sysOrig, sysOptTok := compressPrompt(system)
	usrOpt, usrOrig, usrOptTok := compressPrompt(user)
	tokensSaved := (sysOrig + usrOrig) - (sysOptTok + usrOptTok)

	complexity := analyzeComplexity(system, user, spec.Tools)
	estIn := sysOptTok + usrOptTok
	estOut := outTokens[complexity]

	r := router.New()
	selectedModel, err := r.Select(complexity, spec.Policy, estIn, estOut)
	if err != nil { return nil, fmt.Errorf("router: %w", err) }

	mSpec := core.ModelCatalogue[selectedModel]
	retrieval := "none"
	if spec.Memory.Semantic { retrieval = "semantic" }
	cacheStrat := "semantic"
	if complexity == core.ComplexityComplex || complexity == core.ComplexityExpert { cacheStrat = "exact" }

	plan := core.NewExecutionPlan()
	plan.SpecName = spec.Name; plan.SpecVersion = spec.Version
	plan.SelectedModel = selectedModel
	plan.EstimatedInputTokens = estIn; plan.EstimatedOutputTokens = estOut
	plan.EstimatedCost = mSpec.CostEstimate(estIn, estOut)
	plan.OptimizedSystemPrompt = sysOpt; plan.OptimizedUserTemplate = usrOpt
	plan.RetrievalStrategy = retrieval; plan.CacheStrategy = cacheStrat
	plan.Complexity = complexity

	var steps []core.ExecutionStep
	if retrieval != "none" { steps = append(steps, core.ExecutionStep{Step: "retrieve_context", Strategy: retrieval}) }
	steps = append(steps, core.ExecutionStep{Step: "inject_memory"})
	steps = append(steps, core.ExecutionStep{Step: "render_prompt"})
	toolNames := make([]string, len(spec.Tools))
	for i, t := range spec.Tools { toolNames[i] = t.Name }
	steps = append(steps, core.ExecutionStep{Step: "execute_model", Tools: toolNames})
	if spec.Memory.LongTerm { steps = append(steps, core.ExecutionStep{Step: "store_memory"}) }
	steps = append(steps, core.ExecutionStep{Step: "cache_response"})
	plan.ExecutionSteps = steps

	artifactPath := fmt.Sprintf("%s/%s-%s.ae", outputDir, spec.Name, spec.Version)
	if err := writeAE(spec, &plan, artifactPath); err != nil { return nil, err }

	elapsed := time.Since(t0).Milliseconds()
	if verbose {
		fmt.Printf("  Compiled in %dms | tokens saved: %d | model: %s\n", elapsed, tokensSaved, selectedModel)
	}
	return &CompileResult{Plan: &plan, ArtifactPath: artifactPath, TokensSaved: tokensSaved, ElapsedMS: elapsed}, nil
}

func writeAE(spec *core.AetherSpec, plan *core.ExecutionPlan, path string) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	writeJSON := func(name string, v interface{}) error {
		w, err := zw.Create(name)
		if err != nil { return err }
		enc := json.NewEncoder(w); enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	writeText := func(name, text string) error {
		w, err := zw.Create(name)
		if err != nil { return err }
		_, err = w.Write([]byte(text)); return err
	}

	manifest := map[string]interface{}{
		"aether_version": "0.1.0", "artifact_version": "1",
		"name": spec.Name, "version": spec.Version, "description": spec.Description,
		"plan_id": plan.PlanID, "selected_model": plan.SelectedModel,
		"complexity": string(plan.Complexity), "estimated_cost": plan.EstimatedCost,
		"created_at": plan.CreatedAt,
	}
	writeJSON("manifest.json", manifest)
	writeJSON("spec.json", spec)
	writeJSON("plan.json", plan)
	writeText("prompts/system.txt", plan.OptimizedSystemPrompt)
	writeText("prompts/user_template.txt", plan.OptimizedUserTemplate)
	zw.Close()

	payload := buf.Bytes()
	sum := sha256.Sum256(payload)

	f, err := os.Create(path)
	if err != nil { return err }
	defer f.Close()

	f.Write(aeMagic)
	sizeBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(sizeBuf, uint32(len(payload)))
	f.Write(sizeBuf); f.Write(sum[:4]); f.Write(payload)
	return nil
}

type ArtifactContents struct {
	Manifest     map[string]interface{}
	Spec         core.AetherSpec
	Plan         core.ExecutionPlan
	SystemPrompt string
	UserTemplate string
}

func ReadAEArtifact(path string) (*ArtifactContents, error) {
	data, err := os.ReadFile(path)
	if err != nil { return nil, fmt.Errorf("cannot read artifact: %w", err) }
	if len(data) < 16 { return nil, fmt.Errorf("artifact too small") }
	if !bytes.Equal(data[:8], aeMagic) { return nil, fmt.Errorf("invalid .ae artifact") }
	size := binary.BigEndian.Uint32(data[8:12])
	payload := data[16 : 16+size]

	zr, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil { return nil, fmt.Errorf("cannot open artifact zip: %w", err) }

	readFile := func(name string) ([]byte, error) {
		for _, f := range zr.File {
			if f.Name == name {
				rc, _ := f.Open(); defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, fmt.Errorf("%s not found in artifact", name)
	}

	ac := &ArtifactContents{}
	manifestData, _ := readFile("manifest.json"); json.Unmarshal(manifestData, &ac.Manifest)
	specData, _ := readFile("spec.json"); json.Unmarshal(specData, &ac.Spec)
	planData, _ := readFile("plan.json"); json.Unmarshal(planData, &ac.Plan)
	sys, _ := readFile("prompts/system.txt"); ac.SystemPrompt = string(sys)
	usr, _ := readFile("prompts/user_template.txt"); ac.UserTemplate = string(usr)
	return ac, nil
}
