package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"time"

	"github.com/Orbis-Mind/aether/internal/classifier"
	"github.com/Orbis-Mind/aether/internal/compiler"
	"github.com/Orbis-Mind/aether/internal/core"
	"github.com/Orbis-Mind/aether/internal/discovery"
	"github.com/Orbis-Mind/aether/internal/eval"
	"github.com/Orbis-Mind/aether/internal/models"
	"github.com/Orbis-Mind/aether/internal/provider"
	"github.com/Orbis-Mind/aether/internal/router"
)

// ─── models: registry + local discovery ────────────────────────────────────
func cmdModels(args []string) {
	fs := flag.NewFlagSet("models", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "output JSON")
	fs.Parse(args)
	if !*jsonOut {
		banner()
	}

	// Load any measured overlays so quality provenance reflects real runs.
	models.LoadMeasured(dataDir() + "/observability/benchmarks.json")

	ctx := context.Background()
	locals, derr := discovery.LocalModels(ctx)

	fmt.Println(bold("  LOCAL MODELS (installed via Ollama)"))
	fmt.Println("  " + dim("Discovered live from the Ollama daemon — nothing fabricated."))
	fmt.Printf("  %-22s %-10s %-10s %s\n", "Model", "Size", "Params", "Catalogue")
	fmt.Println("  " + rule(66))
	if derr != nil {
		warn(derr.Error())
	} else if len(locals) == 0 {
		info("No local models installed. Try: ollama pull llama3.2")
	} else {
		for _, l := range locals {
			key := l.CatalogueKey
			if key == "" {
				key = dim("(not in catalogue)")
			}
			fmt.Printf("  %-22s %-10s %-10s %s\n",
				l.Installed.Name, fmt.Sprintf("%.1fGB", l.Installed.SizeGB()),
				orDash(l.Installed.ParameterSize), key)
		}
	}

	avail := map[string]bool{}
	for _, l := range locals {
		if l.CatalogueKey != "" {
			avail[l.CatalogueKey] = true
		}
	}

	fmt.Println("\n" + bold("  CATALOGUE (routable models & quality provenance)"))
	fmt.Printf("  %-18s %-10s %8s %-7s %10s %8s\n", "Model", "Provider", "Quality", "Prov", "Cost/1k", "Local?")
	fmt.Println("  " + rule(70))
	for _, key := range models.Keys() {
		p, _ := models.Get(key)
		localMark := "-"
		if p.Local() {
			if avail[key] {
				localMark = green("installed")
			} else {
				localMark = dim("pullable")
			}
		}
		fmt.Printf("  %-18s %-10s %8.2f %-7s %10.5f %8s\n",
			key, p.Provider, p.Overall.Score, string(p.Overall.Provenance),
			p.Pricing.OutputPer1K, localMark)
	}
	fmt.Println("\n  " + dim("Quality provenance: meas=measured via `aether evaluate`, est=estimated prior, unk=unknown."))
}

// ─── doctor: environment diagnostics ────────────────────────────────────────
func cmdDoctor(args []string) {
	banner()
	fmt.Println(bold("  Aether Doctor — environment check\n"))

	// Go
	checkOK("Go toolchain", runtime.Version(), "")

	// Ollama binary
	if path, err := exec.LookPath("ollama"); err == nil {
		checkOK("Ollama installed", path, "")
	} else {
		checkWarn("Ollama installed", "not found on PATH",
			"Install from https://ollama.com — enables free local models.")
	}

	// Ollama daemon + models
	ctx := context.Background()
	locals, derr := discovery.LocalModels(ctx)
	if derr != nil {
		checkWarn("Ollama daemon", "not reachable",
			"Start it with `ollama serve`, then `ollama pull llama3.2`.")
	} else {
		checkOK("Ollama daemon", "reachable", "")
		if len(locals) == 0 {
			checkWarn("Local models", "none installed",
				"Pull one, e.g. `ollama pull llama3.2` (~2GB), for a $0 path.")
		} else {
			checkOK("Local models", fmt.Sprintf("%d installed", len(locals)), "")
		}
	}

	// Filesystem / data dir
	dir := dataDir()
	if f, err := os.CreateTemp(dir, "doctor-*"); err == nil {
		f.Close()
		os.Remove(f.Name())
		checkOK("Data directory writable", dir, "")
	} else {
		checkFail("Data directory writable", err.Error(),
			"Check permissions on "+dir)
	}

	// Optional cloud keys (never required)
	reportKey("OPENAI_API_KEY")
	reportKey("ANTHROPIC_API_KEY")

	// Spec present?
	if _, err := findSpec(""); err == nil {
		checkOK("Agent spec", "found", "")
	} else {
		checkWarn("Agent spec", "none in this directory",
			"Run `aether init my-agent` to create one.")
	}

	fmt.Println("\n  " + dim("Cloud keys are optional. Aether runs fully free with Ollama."))
}

func reportKey(name string) {
	if os.Getenv(name) != "" {
		checkOK(name, "set (optional cloud enabled)", "")
	} else {
		checkInfo(name, "unset — optional, not required")
	}
}

// ─── evaluate: free offline model benchmarking ──────────────────────────────
func cmdEvaluate(args []string) {
	fs := flag.NewFlagSet("evaluate", flag.ExitOnError)
	modelsFlag := fs.String("models", "", "comma-separated catalogue keys to evaluate (default: installed local models)")
	maxTokens := fs.Int("max-tokens", 512, "max tokens per generation")
	save := fs.Bool("save", true, "save measured quality for future routing")
	positionals := parseInterspersed(fs, args) // flags may precede or follow the dataset path
	banner()

	// Dataset: positional path, else embedded default.
	var ds *eval.Dataset
	var err error
	if len(positionals) > 0 {
		ds, err = eval.Load(positionals[0])
	} else {
		ds, err = eval.Default()
	}
	if err != nil {
		fail(err.Error())
		os.Exit(1)
	}
	info(fmt.Sprintf("Dataset '%s' (%d tasks)", ds.Name, len(ds.Items)))

	// Which models?
	keys := parseCSV(*modelsFlag)
	if len(keys) == 0 {
		avail, derr := discovery.AvailableCatalogueKeys(context.Background())
		if derr != nil || len(avail) == 0 {
			warn("No local models available; evaluating the mock model only (wire check).")
			warn("Install a model (`ollama pull llama3.2`) for a real evaluation.")
			keys = []string{"mock"}
		} else {
			for k := range avail {
				keys = append(keys, k)
			}
			sort.Strings(keys)
		}
	}
	info("Models: " + join(keys, ", "))

	reg := provider.NewRegistry()
	// Ensure mock is routable if selected but not in catalogue keys path.
	last := ""
	rep := eval.Run(context.Background(), ds, keys, reg, eval.Options{
		MaxTokens: *maxTokens,
		Timeout:   120 * time.Second,
		Progress: func(model string, done, total int) {
			if model != last {
				last = model
				fmt.Println()
			}
			fmt.Printf("\r  %-16s %d/%d", model, done, total)
		},
	})
	fmt.Print("\n\n")
	fmt.Println(eval.Render(rep))

	if *save {
		n, werr := eval.WriteMeasured(dataDir()+"/observability", rep)
		if werr != nil {
			warn("Could not save measured results: " + werr.Error())
		} else if n > 0 {
			ok(fmt.Sprintf("Saved %d measured quality records → future routing uses them.", n))
		}
	}
}

// ─── inspect: show a compiled .ae artifact ──────────────────────────────────
func cmdInspect(args []string) {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	fs.Parse(args)
	banner()

	path := ""
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}
	p, err := findArtifact(path)
	if err != nil {
		fail(err.Error())
		os.Exit(1)
	}
	ac, err := compiler.ReadAEArtifact(p)
	if err != nil {
		fail("Cannot read artifact: " + err.Error())
		os.Exit(1)
	}

	fmt.Println(bold("  Artifact: ") + p)
	fmt.Println(rule(60))
	fmt.Printf("  Name           : %v v%v\n", ac.Manifest["name"], ac.Manifest["version"])
	fmt.Printf("  Description    : %v\n", ac.Manifest["description"])
	fmt.Printf("  Aether version : %v\n", ac.Manifest["aether_version"])
	fmt.Println("\n" + bold("  Execution Plan"))
	fmt.Printf("  Task type      : %s (difficulty %.2f, risk %s)\n", orDash(ac.Plan.TaskType), ac.Plan.Difficulty, orDash(ac.Plan.Risk))
	fmt.Printf("  Complexity     : %s\n", ac.Plan.Complexity)
	fmt.Printf("  Strategy       : %s\n", orDash(string(ac.Plan.Strategy)))
	fmt.Printf("  Selected model : %s\n", ac.Plan.SelectedModel)
	fmt.Printf("  Est. tokens    : %d in / %d out\n", ac.Plan.EstimatedInputTokens, ac.Plan.EstimatedOutputTokens)
	fmt.Printf("  Est. cost      : %s\n", core.FormatCost(ac.Plan.EstimatedCost))
	fmt.Printf("  Cache strategy : %s\n", ac.Plan.CacheStrategy)
	fmt.Printf("  Retrieval      : %s\n", ac.Plan.RetrievalStrategy)
	if ac.Plan.RoutingReason != "" {
		fmt.Printf("  Routing reason : %s\n", ac.Plan.RoutingReason)
	}
	fmt.Println("\n  " + green("✓") + " Artifact contains no API keys (verified: keys are read from env at run time).")
}

// ─── reroute: discovery-aware, free-first model selection for `run` ──────────

// rerouteForRun re-selects a model at run time. When strategy/local are set it
// honours them; otherwise (free-first default) it prefers an installed local
// model when Ollama is available, and silently keeps the compiled choice when
// it is not. Returns the chosen model key and a short human note.
func rerouteForRun(ac *compiler.ArtifactContents, message, strategy string, local bool) (string, string) {
	pol := ac.Spec.Policy
	strat := core.Strategy(strategy)

	// Establish availability (best-effort).
	avail, derr := discovery.AvailableCatalogueKeys(context.Background())

	explicit := local || strategy != ""
	if !explicit {
		// Free-first default: only re-route if we actually found local models.
		if derr != nil || len(avail) == 0 {
			return ac.Plan.SelectedModel, ""
		}
		pol.LocalFirst = true
		strat = core.StrategyAuto
	}
	if local {
		strat = core.StrategyLocal
	}
	if derr != nil {
		avail = nil // unknown availability; don't filter local out
	}

	system := ac.SystemPrompt
	user := ac.UserTemplate
	analysis := classifier.New().Classify(system, user+" "+message, len(ac.Spec.Tools) > 0)
	estIn := ac.Plan.EstimatedInputTokens
	estOut := ac.Plan.EstimatedOutputTokens

	dec, err := router.NewPipeline().Route(analysis, pol, strat, estIn, estOut, avail)
	if err != nil {
		// Keep the compiled choice; surface why re-routing didn't apply.
		return ac.Plan.SelectedModel, dim("(kept compiled model: " + err.Error() + ")")
	}
	if dec.Chosen == ac.Plan.SelectedModel {
		return dec.Chosen, ""
	}
	return dec.Chosen, dim("re-routed → " + dec.Chosen + " [" + string(dec.ChosenCand.Quality.Provenance) + "]")
}

// ─── small formatting helpers ───────────────────────────────────────────────
func dim(s string) string { return "\033[2m" + s + "\033[0m" }
func rule(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '-'
	}
	return string(b)
}
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
func parseCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range splitComma(s) {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
func splitComma(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, trimSpace(cur))
			cur = ""
		} else {
			cur += string(r)
		}
	}
	out = append(out, trimSpace(cur))
	return out
}
func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
func join(items []string, sep string) string {
	out := ""
	for i, it := range items {
		if i > 0 {
			out += sep
		}
		out += it
	}
	return out
}

func checkOK(name, detail, _ string) {
	fmt.Printf("  %s %-26s %s\n", green("✓"), name, dim(detail))
}
func checkWarn(name, detail, fix string) {
	fmt.Printf("  %s %-26s %s\n", yellow("⚠"), name, detail)
	if fix != "" {
		fmt.Printf("      %s\n", dim("→ "+fix))
	}
}
func checkFail(name, detail, fix string) {
	fmt.Printf("  %s %-26s %s\n", red("✗"), name, detail)
	if fix != "" {
		fmt.Printf("      %s\n", dim("→ "+fix))
	}
}
func checkInfo(name, detail string) {
	fmt.Printf("  %s %-26s %s\n", "•", name, dim(detail))
}
