package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Orbis-Mind/aether/internal/compiler"
	"github.com/Orbis-Mind/aether/internal/core"
	"github.com/Orbis-Mind/aether/internal/observability"
	"github.com/Orbis-Mind/aether/internal/registry"
	"github.com/Orbis-Mind/aether/internal/router"
	"github.com/Orbis-Mind/aether/internal/runtime"
)

const version = "0.1.0"

// ─── Color helpers (ANSI, work on Windows 10+ terminal) ───────────────────
func cyan(s string) string  { return "\033[36;1m" + s + "\033[0m" }
func green(s string) string { return "\033[32m" + s + "\033[0m" }
func yellow(s string) string { return "\033[33m" + s + "\033[0m" }
func red(s string) string   { return "\033[31m" + s + "\033[0m" }
func bold(s string) string  { return "\033[1m" + s + "\033[0m" }

func banner() {
	fmt.Println(cyan(`
  ╔═══════════════════════════════════════╗
  ║   ⚡  Aether AI Compiler & Runtime   ║
  ║   Make AI as simple as Docker        ║
  ╚═══════════════════════════════════════╝`))
}

func ok(msg string)   { fmt.Println(green("  ✓ " + msg)) }
func info(msg string) { fmt.Println("\033[34m  → " + msg + "\033[0m") }
func warn(msg string) { fmt.Println(yellow("  ⚠ " + msg)) }
func fail(msg string) { fmt.Fprintln(os.Stderr, red("  ✗ "+msg)) }

func findSpec(path string) (string, error) {
	if path != "" {
		if _, err := os.Stat(path); err == nil { return path, nil }
		return "", fmt.Errorf("spec file not found: %s", path)
	}
	for _, n := range []string{"agent.ae.yaml", "agent.ae.yml"} {
		if _, err := os.Stat(n); err == nil { return n, nil }
	}
	matches, _ := filepath.Glob("*.ae.yaml")
	if len(matches) > 0 { return matches[0], nil }
	return "", fmt.Errorf("no .ae.yaml spec found — run 'aether init' first")
}

func findArtifact(path string) (string, error) {
	if path != "" {
		if _, err := os.Stat(path); err == nil { return path, nil }
		return "", fmt.Errorf("artifact not found: %s", path)
	}
	matches, _ := filepath.Glob("*.ae")
	if len(matches) > 0 { return matches[0], nil }
	return "", fmt.Errorf("no .ae artifact found — run 'aether build' first")
}

func dataDir() string { os.MkdirAll(".aether", 0755); return ".aether" }

// splitArgs separates flag arguments from positional arguments so that flags
// may appear before or after positionals (Go's flag package stops at the first
// positional). Flags that take a value are passed as "--flag value".
func splitArgs(args []string) (flags []string, positional []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			// If it's not "--flag=value" and a value follows, consume it.
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i++
			}
		} else {
			positional = append(positional, a)
		}
	}
	return flags, positional
}

// ─── init ─────────────────────────────────────────────────────────────────
func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	dir := fs.String("dir", "", "directory to create project in")
	// Go's flag package stops at the first positional arg, so flags placed
	// after the project name (e.g. `init my-agent --dir foo`) would be ignored.
	// Reorder so all flags are parsed regardless of position.
	flags, positional := splitArgs(args)
	fs.Parse(flags)

	banner()
	name := "my-agent"
	if len(positional) > 0 { name = positional[0] }
	dest := *dir; if dest == "" { dest = name }
	os.MkdirAll(dest, 0755)

	template := "name: " + name + `
version: "1.0.0"
description: "A new Aether AI agent"

policy:
  max_cost_per_run: 0.05
  quality: balanced
  latency: balanced
  max_retries: 3
  # Run fully local & free via Ollama (no API key needed).
  # Uncomment, then: ollama serve && ollama pull llama3.2
  # prefer_local: true
  # Or pin specific providers, e.g. [ollama] or [openai, anthropic]:
  # allowed_providers: [ollama]
  # Tried in order if the primary model fails:
  # fallback_models: [llama3.2, gpt-4o-mini]

memory:
  semantic: true
  long_term: false
  entity: false
  relationship: false
  ttl_seconds: 86400

tools: []

prompts:
  system: |
    You are a helpful, accurate, and concise AI assistant.
    Always provide clear and actionable responses.

  user_template: |
    {user_message}
`
	specPath := filepath.Join(dest, "agent.ae.yaml")
	os.WriteFile(specPath, []byte(template), 0644)
	for _, sub := range []string{"cache","memory","observability","artifacts"} {
		os.MkdirAll(filepath.Join(dest, ".aether", sub), 0755)
	}
	os.WriteFile(filepath.Join(dest, ".aether", ".gitignore"), []byte("*\n!.gitignore\n"), 0644)

	ok(fmt.Sprintf("Project '%s' created at %s/", name, dest))
	ok("Spec written to " + specPath)
	fmt.Println("\n  Next steps:")
	fmt.Printf("    cd %s\n    aether build\n    aether run\n", dest)
}

// ─── build ────────────────────────────────────────────────────────────────
func cmdBuild(args []string) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	output  := fs.String("output", ".", "output directory")
	verbose := fs.Bool("verbose", false, "verbose output")
	fs.Parse(args)

	banner()
	specPath := ""; if fs.NArg() > 0 { specPath = fs.Arg(0) }
	p, err := findSpec(specPath)
	if err != nil { fail(err.Error()); os.Exit(1) }
	info("Compiling " + p + " ...")

	spec, err := compiler.ValidateYAML(p)
	if err != nil { fail("Validation: " + err.Error()); os.Exit(1) }

	os.MkdirAll(*output, 0755)
	result, err := compiler.Compile(spec, *output, *verbose)
	if err != nil { fail("Compilation: " + err.Error()); os.Exit(1) }

	ok(fmt.Sprintf("Compiled in %dms", result.ElapsedMS))
	ok("Artifact: " + result.ArtifactPath)
	fmt.Println("\n" + bold("  Build Summary"))
	fmt.Printf("    Name          : %s v%s\n", spec.Name, spec.Version)
	fmt.Printf("    Complexity    : %s\n", result.Plan.Complexity)
	fmt.Printf("    Selected model: %s\n", result.Plan.SelectedModel)
	fmt.Printf("    Est. tokens   : %d in / %d out\n", result.Plan.EstimatedInputTokens, result.Plan.EstimatedOutputTokens)
	fmt.Printf("    Est. cost     : %s per run\n", core.FormatCost(result.Plan.EstimatedCost))
	fmt.Printf("    Cache strategy: %s\n", result.Plan.CacheStrategy)
	fmt.Printf("    Retrieval     : %s\n", result.Plan.RetrievalStrategy)
	fmt.Printf("    Plan ID       : %s\n", result.Plan.PlanID)
}

// ─── run ──────────────────────────────────────────────────────────────────
func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	message := fs.String("message", "", "user message")
	mock    := fs.Bool("mock", false, "force mock model")
	jsonOut := fs.Bool("json", false, "output raw JSON")
	fs.Parse(args)

	if !*jsonOut { banner() }

	artifactPath := ""; if fs.NArg() > 0 { artifactPath = fs.Arg(0) }
	p, err := findArtifact(artifactPath)
	if err != nil { fail(err.Error()); os.Exit(1) }
	info("Loading artifact: " + p)

	ac, err := compiler.ReadAEArtifact(p)
	if err != nil { fail("Cannot read artifact: " + err.Error()); os.Exit(1) }

	ac.Plan.OptimizedSystemPrompt = ac.SystemPrompt
	ac.Plan.OptimizedUserTemplate = ac.UserTemplate
	if *mock { ac.Plan.SelectedModel = "mock"; warn("Using mock model (no API call will be made)") }

	if *message == "" {
		fmt.Print("  Message: ")
		fmt.Scanln(message)
	}

	rt := runtime.New(&ac.Spec, &ac.Plan, dataDir())
	info("Executing...")
	result := rt.Run(*message, nil)

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout); enc.SetIndent("", "  "); enc.Encode(result); return
	}
	fmt.Println()
	if result.Success {
		fmt.Println(cyan("  ─── Response ─────────────────────────────"))
		fmt.Println("\n" + result.Output + "\n")
		fmt.Println(cyan("  ─── Metadata ─────────────────────────────"))
		fmt.Printf("    Model    : %s\n", result.ModelUsed)
		fmt.Printf("    Tokens   : %d in / %d out\n", result.InputTokens, result.OutputTokens)
		fmt.Printf("    Cost     : %s\n", core.FormatCost(result.Cost))
		fmt.Printf("    Latency  : %.0fms\n", result.LatencyMS)
		fmt.Printf("    Cached   : %v\n", result.Cached)
		fmt.Printf("    Run ID   : %s\n", result.RunID)
	} else {
		fail("Execution failed: " + result.Error); os.Exit(1)
	}
}

// ─── test ─────────────────────────────────────────────────────────────────
func cmdTest(args []string) {
	fs := flag.NewFlagSet("test", flag.ExitOnError); fs.Parse(args)
	banner()
	specPath := ""; if fs.NArg() > 0 { specPath = fs.Arg(0) }
	p, err := findSpec(specPath)
	if err != nil { fail(err.Error()); os.Exit(1) }
	info("Testing " + p + " ...")

	spec, err := compiler.ValidateYAML(p)
	if err != nil { fail("Validation failed: " + err.Error()); os.Exit(1) }
	ok("YAML validation passed")

	tmp, _ := os.MkdirTemp("", "aether-test-*"); defer os.RemoveAll(tmp)
	result, err := compiler.Compile(spec, tmp, false)
	if err != nil { fail("Compilation failed: " + err.Error()); os.Exit(1) }
	ok(fmt.Sprintf("Compilation passed  (model: %s, complexity: %s)", result.Plan.SelectedModel, result.Plan.Complexity))

	r := router.New()
	fmt.Println()
	fmt.Println(r.Explain(result.Plan.Complexity, spec.Policy, result.Plan.EstimatedInputTokens, result.Plan.EstimatedOutputTokens))
	fmt.Println(); ok("All tests passed")
}

// ─── benchmark ────────────────────────────────────────────────────────────
func cmdBenchmark(args []string) {
	fs := flag.NewFlagSet("benchmark", flag.ExitOnError)
	runs := fs.Int("runs", 5, "number of runs")
	fs.Parse(args)
	banner()

	artifactPath := ""; if fs.NArg() > 0 { artifactPath = fs.Arg(0) }
	p, err := findArtifact(artifactPath)
	if err != nil { fail(err.Error()); os.Exit(1) }
	info(fmt.Sprintf("Benchmarking %s (%d runs) ...", p, *runs))

	ac, err := compiler.ReadAEArtifact(p)
	if err != nil { fail(err.Error()); os.Exit(1) }
	ac.Plan.OptimizedSystemPrompt = ac.SystemPrompt
	ac.Plan.OptimizedUserTemplate = ac.UserTemplate
	ac.Plan.SelectedModel = "mock"; ac.Plan.CacheStrategy = "none"

	messages := []string{
		"What is the capital of France?", "Explain quantum computing briefly.",
		"List 3 benefits of exercise.", "Write a haiku about the ocean.", "What is 42 * 17?",
	}
	tmp, _ := os.MkdirTemp("", "aether-bench-*"); defer os.RemoveAll(tmp)
	rt := runtime.New(&ac.Spec, &ac.Plan, tmp)

	var latencies, costs []float64
	successes := 0
	for i := 0; i < *runs; i++ {
		msg := messages[i%len(messages)]
		info(fmt.Sprintf("Run %d/%d: %s...", i+1, *runs, trunc(msg, 50)))
		r := rt.Run(msg, nil)
		latencies = append(latencies, r.LatencyMS)
		costs = append(costs, r.Cost)
		if r.Success { successes++ }
	}

	fmt.Println("\n" + bold("  Benchmark Results"))
	fmt.Printf("    Runs          : %d\n", *runs)
	fmt.Printf("    Success rate  : %.0f%%\n", 100*float64(successes)/float64(*runs))
	fmt.Printf("    Avg latency   : %.0fms\n", avgF(latencies))
	fmt.Printf("    Min latency   : %.0fms\n", minF(latencies))
	fmt.Printf("    Max latency   : %.0fms\n", maxF(latencies))
	fmt.Printf("    Avg cost      : %s\n", core.FormatCost(sumF(costs)/float64(*runs)))
	fmt.Printf("    Total cost    : %s\n", core.FormatCost(sumF(costs)))
	fmt.Printf("    Model (plan)  : %s\n", ac.Manifest["selected_model"])
}

// ─── publish ──────────────────────────────────────────────────────────────
func cmdPublish(args []string) {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	author := fs.String("author", "unknown", "package author")
	tagsStr := fs.String("tags", "", "comma-separated tags")
	fs.Parse(args)
	banner()

	artifactPath := ""; if fs.NArg() > 0 { artifactPath = fs.Arg(0) }
	p, err := findArtifact(artifactPath)
	if err != nil { fail(err.Error()); os.Exit(1) }

	ac, err := compiler.ReadAEArtifact(p)
	if err != nil { fail(err.Error()); os.Exit(1) }

	name := fmt.Sprintf("%v", ac.Manifest["name"])
	ver  := fmt.Sprintf("%v", ac.Manifest["version"])
	desc := fmt.Sprintf("%v", ac.Manifest["description"])
	info(fmt.Sprintf("Publishing %s v%s ...", name, ver))

	var tags []string
	if *tagsStr != "" { tags = strings.Split(*tagsStr, ",") }

	reg := registry.New("")
	entry, err := reg.Publish(p, name, ver, desc, *author, tags)
	if err != nil { fail(err.Error()); os.Exit(1) }

	ok(fmt.Sprintf("Published: %s@%s", entry.Name, entry.Version))
	ok(fmt.Sprintf("Checksum : %s...", entry.Checksum[:16]))
	ok(fmt.Sprintf("Size     : %d bytes", entry.SizeBytes))
	stats := reg.Stats()
	info(fmt.Sprintf("Registry: %d packages, %d versions", stats["total_packages"], stats["total_versions"]))
}

// ─── pull ─────────────────────────────────────────────────────────────────
func cmdPull(args []string) {
	fs := flag.NewFlagSet("pull", flag.ExitOnError)
	ver    := fs.String("version", "", "specific version")
	output := fs.String("output", ".", "output directory")
	fs.Parse(args)
	banner()
	if fs.NArg() == 0 { fail("Usage: aether pull <package-name>"); os.Exit(1) }

	name := fs.Arg(0)
	info(fmt.Sprintf("Pulling %s ...", name))
	reg := registry.New("")
	dest, err := reg.Pull(name, *ver, *output)
	if err != nil { fail(err.Error()); os.Exit(1) }
	ok("Downloaded: " + dest)
}

// ─── search ───────────────────────────────────────────────────────────────
func cmdSearch(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError); fs.Parse(args)
	banner()
	query := ""; if fs.NArg() > 0 { query = fs.Arg(0) }
	reg := registry.New("")
	results := reg.Search(query, nil)
	if len(results) == 0 { warn("No packages found."); return }
	fmt.Printf("  %-30s %-12s %10s  %s\n", "Package", "Version", "Downloads", "Description")
	fmt.Println("  " + strings.Repeat("-", 70))
	for _, e := range results {
		desc := e.Description; if len(desc) > 30 { desc = desc[:30] }
		fmt.Printf("  %-30s %-12s %10d  %s\n", e.Name, e.Version, e.Downloads, desc)
	}
}

// ─── stats ────────────────────────────────────────────────────────────────
func cmdStats(args []string) {
	banner()
	store := observability.NewStore(dataDir() + "/observability")
	fmt.Println(observability.RenderReport(store))
	recent := store.RecentRuns(5)
	if len(recent) > 0 {
		fmt.Println("  Recent Runs:")
		for _, r := range recent {
			status := green("✓")
			if !r.Success { status = red("✗") }
			fmt.Printf("    %s %s  %-25s %s  %.0fms\n",
				status, r.RunID, r.ModelUsed, core.FormatCost(r.Cost), r.LatencyMS)
		}
	}
}

// ─── Math helpers ─────────────────────────────────────────────────────────
func avgF(s []float64) float64 {
	if len(s) == 0 { return 0 }
	t := 0.0; for _, v := range s { t += v }; return t / float64(len(s))
}
func sumF(s []float64) float64 { t := 0.0; for _, v := range s { t += v }; return t }
func minF(s []float64) float64 {
	if len(s) == 0 { return 0 }
	m := s[0]; for _, v := range s[1:] { if v < m { m = v } }; return m
}
func maxF(s []float64) float64 {
	if len(s) == 0 { return 0 }
	m := s[0]; for _, v := range s[1:] { if v > m { m = v } }; return m
}
func trunc(s string, n int) string { if len(s) <= n { return s }; return s[:n] }

// ─── Help ─────────────────────────────────────────────────────────────────
func printHelp() {
	fmt.Print(cyan(`
  ╔═══════════════════════════════════════╗
  ║   ⚡  Aether AI Compiler & Runtime   ║
  ║   Make AI as simple as Docker        ║
  ╚═══════════════════════════════════════╝
`) + `
  Usage: aether <command> [options]

  Commands:
    init        Initialize a new Aether project
    build       Compile .ae.yaml spec into .ae artifact
    run         Execute an .ae artifact
    test        Validate spec and run routing analysis
    benchmark   Benchmark cost, latency, quality
    publish     Publish artifact to Aether Registry
    pull        Pull package from Aether Registry
    search      Search the Aether Registry
    stats       Show observability report

  Examples:
    aether init my-agent
    aether build
    aether run --mock --message "Hello"
    aether benchmark --runs 10
    aether stats

  Run 'aether <command> --help' for command-specific options.
  Version: ` + version + "\n\n")
}

// ─── Main ─────────────────────────────────────────────────────────────────
func main() {
	_ = time.Now() // keep import

	if len(os.Args) < 2 {
		printHelp(); return
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "init":      cmdInit(args)
	case "build":     cmdBuild(args)
	case "run":       cmdRun(args)
	case "test":      cmdTest(args)
	case "benchmark": cmdBenchmark(args)
	case "publish":   cmdPublish(args)
	case "pull":      cmdPull(args)
	case "search":    cmdSearch(args)
	case "stats":     cmdStats(args)
	case "--version", "-v", "version":
		fmt.Println("aether version " + version)
	case "--help", "-h", "help":
		printHelp()
	default:
		fail("Unknown command: " + cmd)
		printHelp()
		os.Exit(1)
	}
}
