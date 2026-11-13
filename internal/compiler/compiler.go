package compiler

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Orbis-Mind/aether/internal/classifier"
	"github.com/Orbis-Mind/aether/internal/core"
	"github.com/Orbis-Mind/aether/internal/router"
	"github.com/Orbis-Mind/aether/internal/yamlx"
)

// ─────────────────────────────────────────────
// Artifact format
// ─────────────────────────────────────────────
//
// The .ae container is: magic(8) ‖ payloadSize(uint32 BE, 4) ‖ checksum ‖ payload.
//
//	v1 (legacy): magic ends 0x01, checksum is the first 4 bytes of sha256(payload).
//	v2 (current): magic ends 0x02, checksum is the full 32-byte sha256(payload),
//	              verified on read. New artifacts are always written as v2.
//
// Readers accept both versions; writers emit v2 only.
var (
	aeMagicV1 = []byte("AETHER\x00\x01")
	aeMagicV2 = []byte("AETHER\x00\x02")
)

const (
	aeMagicLen  = 8
	aeHeaderV1  = aeMagicLen + 4 + 4  // 16: magic + size + truncated checksum
	aeHeaderV2  = aeMagicLen + 4 + 32 // 44: magic + size + full sha256
	aeChecksumV = "AETHER"            // magic ASCII prefix (bytes 0..5)

	// Size limits guard against resource exhaustion from untrusted input.
	maxSpecSize     = 5 << 20   // 5 MiB: largest accepted .ae.yaml spec
	maxArtifactSize = 64 << 20  // 64 MiB: largest accepted .ae payload
	maxUncompressed = 128 << 20 // 128 MiB: zip-bomb guard (total decompressed)
)

// deterministicPlanID derives a stable, content-addressed plan ID from the spec
// so the same spec always compiles to the same artifact (no random component).
func deterministicPlanID(specJSON []byte) string {
	sum := sha256.Sum256(specJSON)
	return "plan-" + hex.EncodeToString(sum[:6])
}

// buildTime returns the timestamp embedded in artifacts. It honours
// SOURCE_DATE_EPOCH (the reproducible-builds convention) and otherwise defaults
// to the Unix epoch, keeping compilation deterministic by default.
func buildTime() time.Time {
	if s := os.Getenv("SOURCE_DATE_EPOCH"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return time.Unix(n, 0).UTC()
		}
	}
	return time.Unix(0, 0).UTC()
}

type ValidationError struct{ msg string }

func (e ValidationError) Error() string { return "validation error: " + e.msg }

func ValidateYAML(path string) (*core.AetherSpec, error) {
	if info, err := os.Stat(path); err == nil && info.Size() > maxSpecSize {
		return nil, ValidationError{fmt.Sprintf("spec file too large: %d bytes (max %d)", info.Size(), maxSpecSize)}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read spec: %w", err)
	}

	raw, err := yamlx.Unmarshal(data)
	if err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}

	if err := validateSchema(raw); err != nil {
		return nil, err
	}

	name := yamlx.GetString(raw, "name")
	version := yamlx.GetString(raw, "version")
	if name == "" {
		return nil, ValidationError{"field 'name' required"}
	}
	if version == "" {
		return nil, ValidationError{"field 'version' required"}
	}

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
		parsePolicy(pm, &spec.Policy)
	}

	if mm := yamlx.GetMap(raw, "memory"); mm != nil {
		spec.Memory.Semantic = yamlx.GetBool(mm, "semantic")
		spec.Memory.LongTerm = yamlx.GetBool(mm, "long_term")
		spec.Memory.Entity = yamlx.GetBool(mm, "entity")
		spec.Memory.Relationship = yamlx.GetBool(mm, "relationship")
		if v := yamlx.GetInt(mm, "ttl_seconds", 0); v > 0 {
			spec.Memory.TTLSeconds = v
		}
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
			if s, ok := v.(string); ok {
				spec.Prompts[k] = s
			}
		}
	}

	return spec, nil
}

// Schema allow-lists. Keys outside these sets are rejected with an actionable
// error so typos (e.g. `qualdity:`) fail fast instead of being silently ignored.
var (
	knownTopLevel = set("name", "version", "description", "policy", "memory", "tools", "prompts", "metadata")
	knownPolicy   = set("max_cost_per_run", "cost", "quality", "latency", "max_retries",
		"fallback_models", "allowed_providers", "prefer_local", "quality_minimum",
		"latency_max_p95", "privacy", "execution", "fallback", "strategy",
		"local_first", "fallback_enabled", "fallback_max_attempts")
	knownMemory = set("semantic", "long_term", "entity", "relationship", "ttl_seconds")
)

func set(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// validateSchema rejects unknown keys at the top level and inside the policy and
// memory blocks, listing the offending key. Free-form blocks (prompts, metadata,
// tools) are not key-checked.
func validateSchema(raw map[string]interface{}) error {
	if err := checkKeys(raw, knownTopLevel, "top level"); err != nil {
		return err
	}
	if pm := yamlx.GetMap(raw, "policy"); pm != nil {
		if err := checkKeys(pm, knownPolicy, "policy"); err != nil {
			return err
		}
	}
	if mm := yamlx.GetMap(raw, "memory"); mm != nil {
		if err := checkKeys(mm, knownMemory, "memory"); err != nil {
			return err
		}
	}
	return nil
}

func checkKeys(m map[string]interface{}, allowed map[string]bool, where string) error {
	for k := range m {
		if !allowed[k] {
			return ValidationError{fmt.Sprintf("unknown %s key %q", where, k)}
		}
	}
	return nil
}

// parsePolicy reads a policy block supporting BOTH the original flat form and
// the nested policy-engine form, so existing specs keep working:
//
//	# flat                     # nested
//	quality: balanced          quality: {minimum: 0.9}
//	max_cost_per_run: 0.05     cost: {max_per_run: 0.01}
//	latency: fast              latency: {max_p95: 2000}
//	prefer_local: true         privacy: {level: local}
//	                           execution: {local_first: true}
//	                           fallback: {enabled: true, max_attempts: 2}
func parsePolicy(pm map[string]interface{}, p *core.Policy) {
	// Cost.
	if v := yamlx.GetFloat(pm, "max_cost_per_run", -1); v >= 0 {
		p.MaxCostPerRun = v
	}
	if cm := yamlx.GetMap(pm, "cost"); cm != nil {
		if v := yamlx.GetFloat(cm, "max_per_run", -1); v >= 0 {
			p.MaxCostPerRun = v
		}
	}
	// Quality: string tier (flat) or {minimum: float} (nested).
	if qm := yamlx.GetMap(pm, "quality"); qm != nil {
		if v := yamlx.GetFloat(qm, "minimum", -1); v >= 0 {
			p.QualityMinimum = v
		}
	} else if v := yamlx.GetString(pm, "quality"); v != "" {
		p.Quality = core.QualityLevel(v)
	}
	// Latency: string profile (flat) or {max_p95: int} (nested).
	if lm := yamlx.GetMap(pm, "latency"); lm != nil {
		if v := yamlx.GetInt(lm, "max_p95", 0); v > 0 {
			p.LatencyMaxP95 = v
		}
	} else if v := yamlx.GetString(pm, "latency"); v != "" {
		p.Latency = core.LatencyProfile(v)
	}
	// Privacy.
	if prm := yamlx.GetMap(pm, "privacy"); prm != nil {
		if v := yamlx.GetString(prm, "level"); v != "" {
			p.Privacy = core.PrivacyLevel(v)
		}
	}
	// Execution.
	if em := yamlx.GetMap(pm, "execution"); em != nil {
		p.LocalFirst = yamlx.GetBool(em, "local_first")
	}
	// Fallback: nested block or flat toggles.
	if fm := yamlx.GetMap(pm, "fallback"); fm != nil {
		p.FallbackEnabled = yamlx.GetBool(fm, "enabled")
		if v := yamlx.GetInt(fm, "max_attempts", 0); v > 0 {
			p.FallbackMaxAttempts = v
		}
	}
	// Strategy (may be pinned in-spec; usually set via CLI).
	if v := yamlx.GetString(pm, "strategy"); v != "" {
		p.Strategy = core.Strategy(v)
	}
	// Retries + provider controls (flat).
	if v := yamlx.GetInt(pm, "max_retries", -1); v >= 0 {
		p.MaxRetries = v
	}
	for _, fb := range yamlx.GetSlice(pm, "fallback_models") {
		if s, ok := fb.(string); ok {
			p.FallbackModels = append(p.FallbackModels, s)
		}
	}
	for _, ap := range yamlx.GetSlice(pm, "allowed_providers") {
		if s, ok := ap.(string); ok {
			p.AllowedProviders = append(p.AllowedProviders, s)
		}
	}
	if yamlx.GetBool(pm, "prefer_local") {
		p.PreferLocal = true
	}
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
	if system == "" {
		system = "You are a helpful AI assistant."
	}
	user := spec.Prompts["user_template"]
	if user == "" {
		user = "{user_message}"
	}

	sysOpt, sysOrig, sysOptTok := compressPrompt(system)
	usrOpt, usrOrig, usrOptTok := compressPrompt(user)
	tokensSaved := (sysOrig + usrOrig) - (sysOptTok + usrOptTok)

	// Classify the workload, then route it through the intelligent pipeline
	// (task analysis → policy filtering → capability/quality/cost/latency
	// scoring → selection). Availability is a runtime fact, so compile-time
	// routing passes nil and pins a policy-satisfying default; `aether run` can
	// re-route against installed local models.
	analysis := classifier.New().Classify(system, user, len(spec.Tools) > 0)
	complexity := analysis.Complexity()
	estIn := sysOptTok + usrOptTok
	estOut := outTokens[complexity]

	strategy := spec.Policy.Strategy
	if strategy == "" {
		strategy = core.StrategyAuto
	}
	decision, err := router.NewPipeline().Route(analysis, spec.Policy, strategy, estIn, estOut, nil)
	if err != nil {
		return nil, fmt.Errorf("router: %w", err)
	}
	selectedModel := decision.Chosen

	mSpec := core.ModelCatalogue[selectedModel]
	retrieval := "none"
	if spec.Memory.Semantic {
		retrieval = "semantic"
	}
	cacheStrat := "semantic"
	if complexity == core.ComplexityComplex || complexity == core.ComplexityExpert {
		cacheStrat = "exact"
	}

	// Deterministic compilation: the artifact must be byte-identical for the same
	// spec. Derive the plan ID from spec content and use a reproducible build
	// time instead of a random ID + wall clock (see deterministicPlanID/buildTime).
	specJSON, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("serialize spec: %w", err)
	}

	plan := core.NewExecutionPlan()
	plan.PlanID = deterministicPlanID(specJSON)
	plan.CreatedAt = buildTime().Format(time.RFC3339)
	plan.SpecName = spec.Name
	plan.SpecVersion = spec.Version
	plan.SelectedModel = selectedModel
	plan.EstimatedInputTokens = estIn
	plan.EstimatedOutputTokens = estOut
	plan.EstimatedCost = mSpec.CostEstimate(estIn, estOut)
	plan.OptimizedSystemPrompt = sysOpt
	plan.OptimizedUserTemplate = usrOpt
	plan.RetrievalStrategy = retrieval
	plan.CacheStrategy = cacheStrat
	plan.Complexity = complexity
	plan.Strategy = strategy
	plan.TaskType = string(analysis.Type)
	plan.Difficulty = analysis.Difficulty
	plan.Risk = string(analysis.Risk)
	plan.RoutingReason = decision.Reason

	var steps []core.ExecutionStep
	if retrieval != "none" {
		steps = append(steps, core.ExecutionStep{Step: "retrieve_context", Strategy: retrieval})
	}
	steps = append(steps, core.ExecutionStep{Step: "inject_memory"})
	steps = append(steps, core.ExecutionStep{Step: "render_prompt"})
	toolNames := make([]string, len(spec.Tools))
	for i, t := range spec.Tools {
		toolNames[i] = t.Name
	}
	steps = append(steps, core.ExecutionStep{Step: "execute_model", Tools: toolNames})
	if spec.Memory.LongTerm {
		steps = append(steps, core.ExecutionStep{Step: "store_memory"})
	}
	steps = append(steps, core.ExecutionStep{Step: "cache_response"})
	plan.ExecutionSteps = steps

	artifactPath := fmt.Sprintf("%s/%s-%s.ae", outputDir, spec.Name, spec.Version)
	if err := writeAE(spec, &plan, artifactPath); err != nil {
		return nil, err
	}

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
		if err != nil {
			return err
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	writeText := func(name, text string) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = io.WriteString(w, text)
		return err
	}

	manifest := map[string]interface{}{
		"aether_version": "0.1.0", "artifact_version": "2",
		"name": spec.Name, "version": spec.Version, "description": spec.Description,
		"plan_id": plan.PlanID, "selected_model": plan.SelectedModel,
		"complexity": string(plan.Complexity), "estimated_cost": plan.EstimatedCost,
		"created_at": plan.CreatedAt,
	}
	// Propagate every write error instead of silently producing a corrupt artifact.
	if err := writeJSON("manifest.json", manifest); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := writeJSON("spec.json", spec); err != nil {
		return fmt.Errorf("write spec: %w", err)
	}
	if err := writeJSON("plan.json", plan); err != nil {
		return fmt.Errorf("write plan: %w", err)
	}
	if err := writeText("prompts/system.txt", plan.OptimizedSystemPrompt); err != nil {
		return fmt.Errorf("write system prompt: %w", err)
	}
	if err := writeText("prompts/user_template.txt", plan.OptimizedUserTemplate); err != nil {
		return fmt.Errorf("write user template: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("finalize artifact zip: %w", err)
	}

	payload := buf.Bytes()
	if len(payload) > maxArtifactSize {
		return fmt.Errorf("artifact payload too large: %d bytes (max %d)", len(payload), maxArtifactSize)
	}
	sum := sha256.Sum256(payload)

	// v2 header: magic ‖ size ‖ full sha256.
	header := make([]byte, 0, aeHeaderV2)
	header = append(header, aeMagicV2...)
	var sizeBuf [4]byte
	binary.BigEndian.PutUint32(sizeBuf[:], uint32(len(payload)))
	header = append(header, sizeBuf[:]...)
	header = append(header, sum[:]...)

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(header); err != nil {
		return fmt.Errorf("write artifact header: %w", err)
	}
	if _, err := f.Write(payload); err != nil {
		return fmt.Errorf("write artifact payload: %w", err)
	}
	return f.Sync()
}

type ArtifactContents struct {
	Manifest     map[string]interface{}
	Spec         core.AetherSpec
	Plan         core.ExecutionPlan
	SystemPrompt string
	UserTemplate string
}

func ReadAEArtifact(path string) (*ArtifactContents, error) {
	if info, err := os.Stat(path); err == nil && info.Size() > int64(maxArtifactSize)+aeHeaderV2 {
		return nil, fmt.Errorf("artifact too large: %d bytes (max %d)", info.Size(), maxArtifactSize)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read artifact: %w", err)
	}

	payload, version, err := decodeFrame(data)
	if err != nil {
		return nil, err
	}

	zr, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return nil, fmt.Errorf("cannot open artifact zip: %w", err)
	}

	// Zip-bomb guard: refuse artifacts that declare an implausible decompressed size.
	var totalUncompressed uint64
	for _, f := range zr.File {
		totalUncompressed += f.UncompressedSize64
		if totalUncompressed > maxUncompressed {
			return nil, fmt.Errorf("artifact decompresses to too much data (> %d bytes)", maxUncompressed)
		}
	}

	readFile := func(name string) ([]byte, error) {
		for _, f := range zr.File {
			if f.Name == name {
				rc, err := f.Open()
				if err != nil {
					return nil, fmt.Errorf("open %s: %w", name, err)
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, maxUncompressed))
			}
		}
		return nil, fmt.Errorf("%s not found in artifact", name)
	}

	ac := &ArtifactContents{}
	if err := readJSONMember(readFile, "manifest.json", &ac.Manifest); err != nil {
		return nil, err
	}
	if err := readJSONMember(readFile, "spec.json", &ac.Spec); err != nil {
		return nil, err
	}
	if err := readJSONMember(readFile, "plan.json", &ac.Plan); err != nil {
		return nil, err
	}
	sys, err := readFile("prompts/system.txt")
	if err != nil {
		return nil, err
	}
	ac.SystemPrompt = string(sys)
	usr, err := readFile("prompts/user_template.txt")
	if err != nil {
		return nil, err
	}
	ac.UserTemplate = string(usr)
	_ = version
	return ac, nil
}

// decodeFrame validates the .ae container header, bounds-checks the declared
// payload size against the actual file, verifies the integrity checksum, and
// returns the raw zip payload. It never slices on untrusted sizes without a
// prior bounds check, so a corrupt or truncated artifact yields an error, never
// a panic. Both v1 (truncated checksum) and v2 (full sha256) frames are accepted.
func decodeFrame(data []byte) (payload []byte, version byte, err error) {
	if len(data) < aeMagicLen {
		return nil, 0, fmt.Errorf("artifact too small (%d bytes)", len(data))
	}
	if string(data[:6]) != aeChecksumV || data[6] != 0x00 {
		return nil, 0, fmt.Errorf("not an Aether artifact (bad magic)")
	}
	version = data[7]

	var headerLen int
	switch version {
	case 0x01:
		headerLen = aeHeaderV1
	case 0x02:
		headerLen = aeHeaderV2
	default:
		return nil, 0, fmt.Errorf("unsupported artifact version %d", version)
	}
	if len(data) < headerLen {
		return nil, 0, fmt.Errorf("artifact truncated: header needs %d bytes, have %d", headerLen, len(data))
	}

	size := binary.BigEndian.Uint32(data[8:12])
	avail := len(data) - headerLen
	if uint64(size) > uint64(avail) {
		return nil, 0, fmt.Errorf("artifact corrupt: declared payload size %d exceeds available %d bytes", size, avail)
	}
	if size > maxArtifactSize {
		return nil, 0, fmt.Errorf("artifact payload too large: %d bytes (max %d)", size, maxArtifactSize)
	}
	payload = data[headerLen : headerLen+int(size)]

	got := sha256.Sum256(payload)
	switch version {
	case 0x02:
		if !bytes.Equal(data[12:44], got[:]) {
			return nil, 0, fmt.Errorf("artifact integrity check failed: checksum mismatch (file may be corrupt or tampered)")
		}
	case 0x01:
		if !bytes.Equal(data[12:16], got[:4]) {
			return nil, 0, fmt.Errorf("artifact integrity check failed: v1 checksum mismatch")
		}
	}
	return payload, version, nil
}

func readJSONMember(read func(string) ([]byte, error), name string, dst interface{}) error {
	b, err := read(name)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("decode %s: %w", name, err)
	}
	return nil
}
