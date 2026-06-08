# Aether ⚡ — AI Compiler and Runtime (Go Edition)

> Make AI development as simple as Docker made application deployment.

**Single binary. Zero dependencies. Instant startup. Works on any laptop.**

---

## Install

**Windows** — Download `aether.exe`, move to `C:\Windows\System32\`

**Mac (Apple Silicon)**
```bash
curl -L https://github.com/Orbis-Mind/aether/releases/latest/download/aether-mac-arm -o /usr/local/bin/aether
chmod +x /usr/local/bin/aether
```

**Mac (Intel)**
```bash
curl -L https://github.com/Orbis-Mind/aether/releases/latest/download/aether-mac-intel -o /usr/local/bin/aether
chmod +x /usr/local/bin/aether
```

**Linux**
```bash
curl -L https://github.com/Orbis-Mind/aether/releases/latest/download/aether-linux -o /usr/local/bin/aether
chmod +x /usr/local/bin/aether
```

**One-line Windows installer (PowerShell as Admin)**
```powershell
irm https://raw.githubusercontent.com/Orbis-Mind/aether/main/install.ps1 | iex
```

---

## Quick Start

```bash
aether init my-agent
cd my-agent

aether build       # compiles agent.ae.yaml → .ae artifact, selects best model
aether test        # validates spec, shows full routing decision table
aether run --mock  # runs with mock model (no API key needed)
aether benchmark   # measures latency, cost, success rate
aether publish     # pushes to local Aether Registry
aether stats       # shows cost/token/latency analytics
```

---

## With a Real AI Model

```bash
# Anthropic (Claude)
set ANTHROPIC_API_KEY=sk-ant-...   # Windows
export ANTHROPIC_API_KEY=sk-ant-... # Mac/Linux
aether run --message "Explain quantum computing"

# OpenAI (GPT)
set OPENAI_API_KEY=sk-...
aether run --message "Explain quantum computing"
```

---

## Write Your Agent

```yaml
# agent.ae.yaml
name: my-agent
version: "1.0.0"

policy:
  max_cost_per_run: 0.05   # USD budget — router picks model automatically
  quality: high             # draft | balanced | high | expert
  latency: fast             # realtime | fast | balanced | batch

memory:
  semantic: true            # similarity-based context retrieval
  long_term: true           # persist facts across sessions

tools:
  - name: search_kb
    description: Search the knowledge base

prompts:
  system: |
    You are a helpful assistant.
  user_template: |
    {user_message}
```

```bash
aether build   # router automatically selects best model for your policy
aether run
```

---

## How Model Selection Works

Aether automatically scores every model and picks the best one:

```
Score = 0.50 × quality − 0.35 × cost − 0.15 × latency

Model              Quality   Cost/run   Latency   Eligible
─────────────────────────────────────────────────────────
gpt-4o-mini         0.70    $0.00024    700ms        ✓  ← selected (best score)
claude-haiku-3-5    0.72    $0.00203    800ms        ✓
claude-sonnet-3-5   0.88    $0.00608   2000ms        ✓
gpt-4o              0.90    $0.00614   2500ms        ✓
claude-opus-4       0.97    $0.03042   5000ms        ✓
```

Change `quality: expert` → Aether picks `claude-opus-4` automatically.
Change `max_cost_per_run: 0.001` → Aether picks `gpt-4o-mini` automatically.

You never touch model names.

---

## Architecture

```
agent.ae.yaml
     │
     ▼
 Compiler          Validates YAML, optimises prompts,
                   estimates tokens, runs Router
     │
     ▼
  Router           Scores all models on quality/cost/latency
                   Picks the winner within your policy
     │
     ▼
 .ae Artifact      Binary format: magic header + ZIP
                   Contains: spec, plan, optimised prompts
     │
     ▼
  Runtime          Checks cache → retrieves memory →
                   calls model API → stores results →
                   records observability
     │
     ▼
ExecutionResult    output, model, tokens, cost, latency, run_id
```

---

## CLI Reference

| Command | Description |
|---------|-------------|
| `aether init [name]` | Scaffold a new project |
| `aether build` | Compile spec → .ae artifact |
| `aether run` | Execute artifact |
| `aether run --mock` | Execute with mock (no API key) |
| `aether run --message "..."` | Run with inline message |
| `aether test` | Validate + routing analysis |
| `aether benchmark --runs N` | Performance benchmark |
| `aether publish` | Publish to registry |
| `aether pull <name>` | Pull from registry |
| `aether search [query]` | Search registry |
| `aether stats` | Observability report |
| `aether --version` | Show version |

---

## Build from Source

Requires Go 1.22+. Zero external dependencies.

```bash
git clone https://github.com/Orbis-Mind/aether
cd aether

# Build for your OS
go build -o aether ./cmd/aether

# Build all platforms
make build-all

# Run tests
go test ./tests/ -v
```

---

## Binary Sizes

| Platform | Size |
|----------|------|
| Windows (aether.exe) | ~5.7 MB |
| Mac Apple Silicon | ~5.4 MB |
| Mac Intel | ~5.7 MB |
| Linux x64 | ~5.6 MB |

Single static binary. No runtime. No installer. Just copy and run.

---

## License

MIT © Orbis-Mind
