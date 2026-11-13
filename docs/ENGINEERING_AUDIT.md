# Aether Go — Phase 1 Engineering Audit

**Date:** 2026-11-11
**Scope:** Full repository audit (39 Go files, ~7,370 LOC, zero third-party dependencies).
**Method:** Static read of every package + empirical verification of the highest-severity findings via the built CLI.

## Baseline (verified green)

| Gate | Result |
|------|--------|
| `go build ./...` | ✅ pass |
| `go vet ./...` | ✅ clean |
| `gofmt -l .` | ✅ clean (no files) |
| `go test ./...` | ✅ all packages pass |
| `go test -race ./...` | ✅ no races detected |
| Dependencies | ✅ stdlib only (no `go.sum`) |

**The green baseline is misleading.** It reflects a prototype whose tests exercise happy paths on non-shared instances. The defects below are real and, in three cases, empirically reproduced. "Compiles and tests pass" is not production-readiness (per the project's own Definition of Done).

---

## Severity summary

| ID | Severity | Area | One-line | Status |
|----|----------|------|----------|--------|
| C1 | 🔴 Critical | Compiler | Corrupt `.ae` artifact panics the process (OOB slice) — **reproduced** | ✅ Phase 2 |
| C2 | 🔴 Critical | Compiler | Artifact integrity is never verified on read; 4-byte checksum; no decompression bound | ✅ Phase 2 |
| H1 | 🟠 High | Compiler | Compilation is non-deterministic — **reproduced** | ✅ Phase 2 |
| H2 | 🟠 High | Compiler | `yamlx` is not strict — silently ignores unknown/malformed/mis-indented keys | ✅ Phase 2 |
| H3 | 🟠 High | Compiler | Documented nested policy flow-form (`{minimum: 0.9}`) does not parse — silent garbage | ✅ Phase 2 |
| H4 | 🟠 High | Routing | Two competing routers; legacy `router.Router` is dead except in tests | ✅ Phase 4 |
| H5 | 🟠 High | Runtime | `Run`/`RunStream` take no `context.Context`; callers cannot cancel | ✅ Phase 3 |
| H6 | 🟠 High | Provider | Ollama maps timeout/cancellation to a permanent "unreachable" error; no retry | ✅ Phase 3 |
| M1 | 🟡 Medium | Cache | Caches are unbounded (TTL only, no eviction / size cap) | ✅ Phase 5 |
| M2 | 🟡 Medium | Memory | No data-deletion API (`VectorStore` has no `Delete`) | ✅ Phase 3 |
| M3 | 🟡 Medium | Concurrency | Cache / memory / observability mutate shared state without locks | ✅ Phase 3 |
| M4 | 🟡 Medium | Reliability | Persistence errors swallowed (artifact write, cache/memory/obs save) | ✅ Phase 2 + logging pass |
| M5 | 🟡 Medium | CLI | `build -output` only applies when the flag precedes the spec path (stdlib `flag` stops at first positional) | ✅ Phase 6 |
| M6 | 🟡 Medium | Provider | No health-check method on the `ModelProvider` interface | ✅ Phase 3 |
| M7 | 🟡 Medium | Compiler | 4-byte (32-bit) checksum is weak integrity even once verified | ✅ Phase 2 |
| M8 | 🟡 Medium | Core | 4-byte random `shortID` for Plan/Run IDs; `rand.Read` error ignored | ✅ Phase 3 |
| L1 | ⚪ Low | Hygiene | Duplicate eval dataset (identical bytes, two paths) | ✅ Phase 6 |
| L2 | ⚪ Low | Hygiene | Stray `mybot/` scaffold committed | ✅ Phase 6 |
| L3 | ⚪ Low | CI/CD | No coverage / vuln scan / secret scan / SBOM; non-reproducible builds | ✅ Phase 6 |
| L4 | ⚪ Low | Runtime | `fallbackChain` off-by-one vs `FallbackMaxAttempts` semantics | ✅ Phase 3 |

> **M5 correction:** on re-check, `Compile` does honour `outputDir`; the CLI symptom was Go's `flag` package halting at the first positional arg, so `build <spec> -output <dir>` leaves `-output` at its default. `build -output <dir> <spec>` works. Reclassified as a CLI UX fix (Phase 6), not a logic bug.

---

## Critical

### C1 — Corrupt artifact panics the process (OOB slice)
**File:** `internal/compiler/compiler.go:362-363`
```go
size := binary.BigEndian.Uint32(data[8:12])
payload := data[16 : 16+size]   // size is attacker-controlled; no bounds check
```
`size` is read from untrusted file bytes and used directly to slice. A 16-byte file with an oversized declared size crashes the CLI.

**Reproduced:**
```
$ aether inspect corrupt.ae
panic: runtime error: slice bounds out of range [16:15]
  .../internal/compiler/compiler.go:363 ReadAEArtifact
  .../cmd/aether/commands.go:233 cmdInspect
```
**Impact:** Unauthenticated crash / DoS on any malformed or truncated artifact. Fails §3 (*Artifact corruption tests, Safe serialization/deserialization, Maximum artifact size limits*).
**Fix direction:** Validate `len(data) >= 16`, then `size <= uint32(len(data)-16)`, and enforce a max artifact size before slicing. Add fuzz + corruption tests.

### C2 — Artifact integrity is never verified on read
**File:** `internal/compiler/compiler.go:326,338` (write) vs `351-392` (read)
The writer computes `sha256` and stores only the **first 4 bytes** (`f.Write(sum[:4])`). `ReadAEArtifact` **never recomputes or compares** the checksum, and ignores every `json.Unmarshal`/`readFile` error. A tampered or truncated-payload artifact loads silently as zero-value structs. The zip reader also has **no decompression bound** (zip-bomb exposure).
**Impact:** Fails §3 (*Artifact integrity verification, Safe deserialization*). Supply-chain / tamper risk for published artifacts.
**Fix direction:** Store the full 32-byte digest; verify on read and return a typed error on mismatch; bound total uncompressed size; stop ignoring unmarshal errors.

---

## High

### H1 — Non-deterministic compilation
**File:** `internal/core/core.go:263-271` (`shortID`, `CreatedAt`), embedded by `compiler.go:242,314-316`
`plan.PlanID` (random) and `plan.CreatedAt` (`time.Now()`) are written into the artifact, so the same spec compiles to different bytes every time.
**Reproduced:** two builds of the identical spec →
```
first.ae  1e841a9b...   second.ae  3eff2267...   (differ at header)
```
**Impact:** Fails §3 (*Deterministic compilation*). Breaks reproducible builds, content-addressing, and artifact caching.
**Fix direction:** Derive the artifact from spec content only (or a caller-supplied build time); make IDs content-derived or move them out of the artifact; zero zip mod-times.

### H2 — `yamlx` is not strict
**File:** `internal/yamlx/yamlx.go`
The hand-rolled parser **never returns a non-nil error** for structurally invalid input. Unknown keys, malformed lines (`colonIdx < 0` → `i++; continue`), wrong-indent lines (`ind > indent` → skipped), and duplicate keys (last wins) are all silently accepted. This is the opposite of §3 (*Strict YAML parsing, Schema validation*).
**Fix direction:** Add a schema layer with unknown-key rejection and actionable diagnostics (line/column), or adopt strict decoding; return real errors.

### H3 — Documented nested policy form does not parse
**File:** `internal/compiler/compiler.go:97-101` (doc comment) vs `internal/yamlx/yamlx.go:74-77,208-233`
The compiler documents flow-form nested policy, e.g. `quality: {minimum: 0.9}`. `yamlx` has no flow-map support: `parseScalar("{minimum: 0.9}")` returns the **literal string**, so `GetMap` misses and `p.Quality` is set to `"{minimum: 0.9}"` — a garbage tier. A claimed feature is silently broken (violates §1/§12: *never claim a feature works unless implemented and tested*).
**Fix direction:** Implement flow maps/sequences in `yamlx` (with tests), or remove the claim and support only block form.

### H4 — Two competing routing implementations
**Files:** `internal/router/router.go` (legacy `Router.Select/Explain`, uses `core.ModelCatalogue` directly) vs `internal/router/pipeline.go` (authoritative `Pipeline.Route`).
`Pipeline` is what the compiler/CLI use (`compiler.go:226`, `commands.go:294`, `main.go:364`). `Router` is **dead production code**, reachable only from `tests/aether_test.go`. Violates §5 (*one authoritative routing implementation*).
**Fix direction:** Delete `router.go`; migrate its test assertions onto `Pipeline`.

### H5 — Runtime is not context-aware at the boundary
**File:** `internal/runtime/runtime.go:150,205` (`Run`, `RunStream`)
Both create their own `context.Background()` internally (`:120`, `:210`); neither accepts a caller `context.Context`. External cancellation, deadlines, and request-scoped tracing cannot propagate in. Violates §4 (*Context-aware execution, Cancellation propagation*).
**Fix direction:** Thread `ctx context.Context` through `Run`/`RunStream`/`execute`; derive per-attempt timeouts from it; honor `ctx.Err()`.

### H6 — Ollama misclassifies timeout/cancellation as permanent
**File:** `internal/provider/ollama.go:124-127,161-163`
Every `client.Do` error — including `context.DeadlineExceeded` and `context.Canceled` — is wrapped as `Permanent("cannot reach Ollama … ollama serve")`. The runtime then treats it as permanent (`runtime.go:127`): no retry, misleading message, cancellation swallowed. Conflicts with §4 (*Retry policies, Cancellation propagation*).
**Fix direction:** Branch on `errors.Is(err, context.Canceled/DeadlineExceeded)` and return a typed cancellation/timeout (retryable) error distinct from connection-refused.

---

## Medium

- **M1 — Unbounded caches.** `internal/cache/cache.go` `ExactCache`/`SemanticCache` expire on read (TTL) but never cap size or evict; the backing maps grow without bound. Fails §7 (*Bounded caching*). → add max-entries + LRU/size eviction.
- **M2 — No data deletion.** `internal/memory/memory.go` `VectorStore` exposes `Add/Search/Count/save/load` but **no `Delete`**; there is no way to erase stored data. Fails §7 (*Data deletion*). → add `Delete(key)` / `Purge`.
- **M3 — Missing concurrency guards.** `cache` (`ExactCache`/`SemanticCache` maps), `memory` (`VectorStore`), and `observability` (`Store.records` slice, `:50`) mutate shared state without mutexes. The race detector passes only because tests use per-run instances. Latent data races under concurrent use (§9 *Concurrency tests*). → add `sync.RWMutex`; add a concurrency test.
- **M4 — Swallowed persistence errors.** `writeAE` ignores `f.Write`/`writeJSON`/`writeText` returns (`compiler.go:318-337`); `cache.save/load`, `memory.save/load` (`:153-181`), `observability.save` (`:193-195`) ignore `os.WriteFile`/`ReadFile`. Fails §4 (*Never swallow errors*). → propagate and log.
- **M5 — `build -output` ignored.** `cmd/aether` build always writes the artifact to the cwd; `-output <dir>` has no effect (**reproduced**: target dir stays empty). → honor the flag or remove it.
- **M6 — No provider health check.** `provider.ModelProvider` (`provider.go:90-95`) has no health/ready method; §4/§6 require *Provider health checks*. Ollama has `List` but nothing generic. → add `HealthCheck(ctx) error` to the interface.
- **M7 — Weak checksum.** Even once C2 is fixed, storing `sum[:4]` (`compiler.go:338`) is 32-bit integrity. → store/verify the full digest.
- **M8 — Collision-prone IDs.** `shortID` (`core.go:263`) is 4 random bytes and ignores `rand.Read`'s error; used for `PlanID`/`RunID`. → widen (e.g. 16 bytes) and check the error.

---

## Low / hygiene

- **L1** — `internal/eval/aether-basic-v1.json` and `evaluation/datasets/aether-basic-v1.json` are byte-identical duplicates. Pick one source of truth.
- **L2** — `mybot/` is a stray `aether init` scaffold committed to the repo. Remove.
- **L3** — CI (`.github/workflows/ci.yml`) runs gofmt/vet/`test -race`/cross-build but has **no** coverage reporting, `govulncheck`, secret scanning, or SBOM; releases aren't reproducible (`-trimpath` absent) and only the Windows build stamps a version. Fails parts of §10.
- **L4** — `runtime.fallbackChain` (`runtime.go:92-104`) uses `len(chain) > max`, which is off-by-one relative to the intended `FallbackMaxAttempts` semantics.

---

## Recommended sequencing

Follows the mandate's phases, highest-severity first:

1. **Phase 2 — Compiler & artifact hardening** (C1, C2, H1, H2, H3, M4, M5, M7): bounds-checked + integrity-verified artifact read, deterministic output, strict YAML + schema with diagnostics, size limits, fuzz/corruption tests. *Highest ROI — contains both Criticals and the reproduced crash.*
2. **Phase 3 — Runtime & provider reliability** (H5, H6, M3, M6, L4): context threading, typed cancellation/timeout, health checks, concurrency guards.
3. **Phase 4 — Routing & evaluation** (H4): collapse to one authoritative router; harden eval provenance.
4. **Phase 5 — Observability & memory** (M1, M2, M3): bounded cache, deletion API, structured logging/trace IDs.
5. **Phase 6 — Security, testing & release** (L1, L2, L3): coverage, govulncheck, secret scan, SBOM, reproducible/`-trimpath` builds, checksums.

Each item above cites the exact file:line to change.

---

## Phase 2 — Compiler & artifact hardening (completed 2026-11-11)

Scope delivered, all changes in `internal/compiler/` and `internal/yamlx/`, backward-compatibility preserved (all three example specs still validate; `v2` artifact format reads legacy `v1`).

| ID | Fix | Evidence |
|----|-----|----------|
| C1 | `decodeFrame` bounds-checks the declared payload size against the actual file before slicing; adds artifact + spec + decompression size limits. | `TestCorruptArtifactNoPanic` (7 cases), `FuzzReadAEArtifact` (~19k execs, no panics); CLI `inspect` on the original repro now prints `artifact corrupt: declared payload size … exceeds available …` instead of panicking. |
| C2 | Full 32-byte SHA-256 stored (v2) and **verified on read**; v1 truncated checksum also verified; zip-bomb guard caps total decompressed size; all member reads use `io.LimitReader`. | `TestTamperedPayloadFailsIntegrity`, `TestV1BackwardCompatibility`, `TestArtifactRoundTrip`. |
| H1 | Deterministic compilation: content-addressed `plan_id` (`deterministicPlanID`) + reproducible build time honouring `SOURCE_DATE_EPOCH` (`buildTime`), default Unix epoch. | `TestCompileDeterministic` — same spec → byte-identical artifact + identical SHA-256. |
| H2 | `yamlx` now rejects tab indentation, missing-colon lines, and duplicate keys (with line numbers); compiler adds schema validation rejecting unknown top-level / policy / memory keys. | `TestStrictRejectsTab/MissingColon/DuplicateKey`, `TestSchemaRejectsUnknown*`, `TestSchemaAcceptsKnownKeys`. |
| H3 | Implemented flow mappings `{k: v}` and sequences `[a, b]` in `yamlx`, so the documented nested policy form is now real. | `TestFlowMap/FlowSeq/EmptyFlowCollections`, `TestNestedPolicyFlowForm` (quality/cost/latency/privacy all applied). |
| M4 (partial) | `writeAE` propagates every zip/write error and `fsync`s; `ReadAEArtifact` propagates all unmarshal/member errors. (cache/memory/observability save paths remain for Phase 3/5.) | — |
| M7 | Checksum widened from 4 → 32 bytes. | folded into C2. |

**Format change:** new artifacts are `artifact_version: 2` (magic `AETHER\x00\x02`, 44-byte header with full digest). The reader still accepts `v1` (magic `AETHER\x00\x01`, 16-byte header) per the agreed compatibility policy.

**Verification run:** `gofmt -l` clean · `go vet ./...` clean · `go test -race ./...` all packages pass · example specs validate.

---

## Phase 3 — Runtime & provider reliability (completed 2026-11-13)

| ID | Fix | Evidence |
|----|-----|----------|
| H5 | Added `RunContext`/`RunStreamContext` threading `context.Context` into model calls and retry backoff; `Run`/`RunStream` kept as background-context wrappers (backward compatible). `execute` checks `ctx.Err()` before each attempt and uses an interruptible backoff timer. CLI wires `signal.NotifyContext` so Ctrl-C/SIGTERM cancels in-flight work. | `TestRunContextCancelledFailsFast`, `TestRunStreamContextCancelled`. |
| H6 | Ollama (`classifyDoErr`) and cloud (`classifyCloudErr`) now map `context.Canceled` → propagated, `DeadlineExceeded` → typed `Transient` timeout, genuine dial failure → `Permanent`. Cancellation is no longer mislabelled "unreachable". | `TestOllamaCancellationNotPermanent`. |
| M2 | `VectorStore.Delete`/`Purge` + `Orchestrator.Forget`/`Clear` provide data deletion. | `TestVectorStoreDelete`, `TestVectorStorePurge`. |
| M3 | Added mutexes to `ExactCache`, `SemanticCache`, `VectorStore`, `EntityMemory`, `RelationshipMemory`, and observability `Store`. | `-race` concurrency tests across cache/memory/observability packages. |
| M6 | New `HealthCheck(ctx)` on the `ModelProvider` interface + `Registry.HealthCheck`; implemented for Ollama (pings `/api/tags`), OpenAI/Anthropic (credential readiness), and mock. Added `TransientError` type + `IsTransient`. | `TestMockHealthCheck`, `TestCloudHealthCheckRequiresCredentials`, `TestRegistryHealthCheckDispatch`, `TestErrorClassification`. |
| M8 | `shortID` widened to 16 random bytes and now handles the `rand.Read` error (time-based fallback). | — |
| L4 | `fallbackChain` rewritten with explicit, documented `FallbackMaxAttempts` semantics (bounds fallback models beyond the primary). | existing runtime fallback tests still pass. |

## Phase 4 — Routing consolidation (completed 2026-11-13)

| ID | Fix | Evidence |
|----|-----|----------|
| H4 | Deleted the dead legacy `internal/router/router.go` (`Router.Select/Explain`); `Pipeline` is the single authoritative router. The five legacy `TestRouter*` tests were migrated onto `Pipeline.Route`/`ExplainDecision`, preserving behavioural coverage. | migrated `TestRouter*` pass; `internal/router/pipeline_test.go` unchanged and passing. |

## Phase 5 — Bounded cache (completed 2026-11-13)

| ID | Fix | Evidence |
|----|-----|----------|
| M1 | Both caches bounded by `maxCacheEntries` (1000) with eviction: expired entries first, then oldest. | `TestExactCacheBounded`, `TestSemanticCacheBounded`. |

## Phase 6 — Security, testing & release (completed 2026-11-13)

| ID | Fix | Evidence |
|----|-----|----------|
| L1 | Added a drift-guard test asserting the embedded dataset equals the public `evaluation/datasets/` copy, so the two cannot silently diverge. | `TestEmbeddedDatasetMatchesPublicCopy`. |
| L2 | Removed the stray `mybot/` scaffold. | file deleted. |
| L3 | CI adds race+coverage reporting, `govulncheck`, and `gitleaks` secret scanning; builds use `-trimpath` + `CGO_ENABLED=0` for reproducibility. Release workflow adds linux/arm64, a CycloneDX SBOM, `SHA256SUMS`, and version stamping across all OSes. Also fixed: `version` was a `const`, so `-X main.version` was silently ignored — now a `var`. | `.github/workflows/ci.yml`, `release.yml`. |
| M5 | `parseInterspersed` lets `build` accept `-output` before **or** after the spec path. | verified via CLI both orders. |

**Phases 2–6 verification:** `gofmt -l` clean · `go vet ./...` clean · `go test -race ./...` all packages pass.

---

## Structured logging & tracing pass (completed 2026-11-13)

Closes the remainder of **M4** and delivers the structured-logging / execution-ID / request-tracing requirements from §7, using the standard library's `log/slog` (zero new dependencies).

| Item | Fix | Evidence |
|------|-----|----------|
| Structured logging | New leaf package `internal/logging` wraps `log/slog`: JSON (default) or text to **stderr**, level from `AETHER_LOG_LEVEL` (default `warn`, so the CLI stays quiet). stdout — including `--json` results — is never polluted. | `TestParseLevel`, `TestUseOverridesLogger`; CLI check: default run emits 0 stderr lines, `AETHER_LOG_LEVEL=debug` emits `run.start`/`run.complete`. |
| Execution IDs / request tracing | `logging.WithExecutionID`/`ExecutionID` propagate a trace ID through context; `RunContext`/`RunStreamContext` tag a child logger with `execution_id`, `plan_id`, `spec` and record the ID on `result.Metadata["execution_id"]`. | `TestExecutionIDRoundTrip`, `TestRunEmitsTracedLifecycleLog`. |
| M4 completion | cache, memory, and observability `save()`/`load()` now log failures via `logging.L()` instead of discarding them (cold-start "file not found" stays silent); the runtime logs routing-record persistence failures and run lifecycle. | build + `-race` suite. |

Debug lifecycle log shape: `{"level":"INFO","msg":"run.complete","execution_id":"…","model":"mock","latency_ms":…}`.

**All audit findings are now resolved** (C1–C2, H1–H6, M1–M8, L1–L4). Beyond the audit, §7's structured logging + execution-ID tracing are in place. Natural follow-ons (not audit defects): native SSE streaming for cloud providers, a `govulncheck`-clean dependency policy as deps are added, and persisting cache/memory atomically (temp-file + rename) if durability under crash becomes a requirement.
