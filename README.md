# metricgate

Deterministic code metrics and constraint gates, built on Go + tree-sitter.

metricgate answers one question locally, in milliseconds and at zero token
cost: **does this code satisfy our structural constraints?** It is the
deterministic layer that LLM-based review tools are missing — and the same
policy file that powers the gate later drives LLM code-generation constraints
and skeleton generation (v2/v3).

```
LLM 生成代码 → tree-sitter 解析（容错解析）
            → metricgate 闸门（0 token，确定性规则+度量）
            → 违规 JSON 反馈给 LLM → 自修复 → 复检全绿
```

## Status: v0.1.0 (MVP)

- Go + Java language support (tree-sitter grammars, incremental-friendly)
- 6 metrics: cyclomatic complexity, cognitive complexity, function length,
  file length, nesting depth, parameter count
- Built-in structural rules (tree-sitter query + Go filter): `empty-block`,
  `todo-comment`, and Java `print-stack-trace`
- **Data-driven bans**: `banned-api:` entries in `policy.yaml` compile into the
  same deterministic pipeline — an organisation adds a constraint by editing
  YAML, not Go (see [Banned API as policy data](#banned-api-as-policy-data))
- **Architecture constraints**: the package dependency graph — layer boundaries,
  forbidden edges and import cycles (Go + Java), anchored at the import line and
  reported through the same gate ([details](#architecture-constraints))
- Policy file (`policy.yaml`) = the project's constraint model
- SARIF 2.1.0 output (GitHub code scanning compatible)
- Local SQLite trend store (write-only in v0)
- `metricgate context <path>`: token-budgeted constraint pack (signatures + active policy) for LLM prompts
- CI gate: exit code 1 on violations
- MCP stdio server (`metricgate-mcp`): get_constraints / check_code / check_path / list_stubs / find_symbols / get_review_bundle
- Stub detection (completeness audit) for Go and Java
- Clone drift: `metricgate clonedrift` flags a change that edits one copy of a
  duplicate but not its twin (needs git; see [clone drift](#clone-drift-metricgate-clonedrift))

## Quick start

```bash
go build -o metricgate ./cmd/metricgate

# 1. write the default policy into your project
./metricgate init

# 2. check the current directory
./metricgate check .            # text report + gate verdict
./metricgate check . -sarif out.sarif -format json

# 3. metric table only
./metricgate metrics .

# 4. constraint pack for an LLM prompt — facts + policy, budget-capped
./metricgate context internal/parser

# 5. deterministic, budgeted review bundle for a host agent — no LLM call
./metricgate bundle . -max-chars 6000
./metricgate bundle . -base origin/main -out review.json
```

## MCP server (call it from your coding agent)

metricgate ships an MCP (Model Context Protocol) stdio server so agents can
ground themselves in real constraints instead of hallucinating APIs:

```bash
go build -o metricgate-mcp ./cmd/metricgate-mcp
claude mcp add metricgate -- /path/to/metricgate-mcp -config /path/to/policy.yaml
```

Tools exposed:

| Tool | Purpose |
|------|---------|
| `get_constraints` | dense fact+constraint pack to paste into the prompt BEFORE generating (signatures, imports, rules, limits) |
| `check_code` | deterministic check of in-memory code after EVERY edit — 0 tokens, milliseconds |
| `check_path` | check files on disk (optionally diff-only) |
| `list_stubs` | repair worklist: empty bodies, `panic("not implemented")`, `UnsupportedOperationException` — each with its exact definition site |
| `find_symbols` | deterministic cross-file symbol index: exact lookups + near-miss suggestions (typos, case twins), 0 tokens |
| `get_review_bundle` | deterministic, budgeted review input (diff + selection + constraints + facts) for a host agent; metricgate never calls an LLM |

## Banned API as policy data

Organisational constraints ("no direct stdout", "never import X") change far
faster than the checker does, so they live in the policy file instead of in Go.
The engine compiles each entry into the same tree-sitter rule pipeline:
identical findings, severities, SARIF catalogue, suppression syntax, and the
same lines show up in `metricgate context` / `get_constraints` — a prompt can
never be told a different rule set from the one the gate enforces.

```yaml
languages:
  go:
    banned-api:
      - match: fmt.Print*            # trailing "*" = name prefix
        reason: use the project logger
      - match: panic
        except_files: ["_test.go"]   # file-name suffixes out of scope
      - kind: import                 # ban a package and its subpackages
        match: os/exec
        reason: only the git adapter may shell out
```

Every ban gets a stable id — `banned-` plus the pattern with dots and slashes
folded to dashes (`banned-fmt-Print*`, `banned-os-exec`,
`banned-System-out-print*`) — so it can be retuned like any other rule:

```yaml
    rules:
      banned-fmt-Print*:
        severity: error
        options:
          allowed_files: ["cmd/tool/main.go"]
```

Both grammars are covered: Go matches `pkg.Name(...)` and unqualified
`name(...)`; Java matches `receiver.method(...)`, `Object.field.method(...)`
and unqualified `method(...)`. Deeper Java receivers need type resolution and
are deliberately out of scope — that is adapter territory
([docs/roadmap-research.md](docs/roadmap-research.md)), not AST territory.

## Architecture constraints

A per-file rule can never see a dependency direction, so every run also builds
the package graph — from the same parse pass, no external tooling — and checks
it against the policy:

```yaml
architecture:
  cycles: error          # import cycles (Tarjan SCC over the package graph)
  edges: warning         # severity of layer / forbidden-edge findings
  layers:
    - name: handler
      packages: ["internal/handler/**"]
      may_depend_on: [service]                      # layer names or patterns
    - name: service
      packages: ["internal/service/**"]
      may_depend_on: [storage, "pkg/logging"]
    - name: storage
      packages: ["internal/storage/**"]
  forbid:
    - from: internal/storage/**
      to: internal/handler/**
      reason: storage must not know about HTTP
```

Semantics chosen to work on a real codebase:

- **Packages are directories (Go) or package clauses (Java).** Imports of
  standard-library and third-party code are not nodes, so the graph describes
  *your* architecture. Patterns match on trailing segments, so `internal/rules`
  also names `yourmodule/internal/rules`; `/**` spans depth, `*` is one segment.
- **Only layered packages are constrained.** A package in no layer, and any edge
  pointing outside every layer, stays legal: describe the part you own.
- **A `forbid` match wins over the layer verdict** for that edge — one finding
  per illegal dependency, never two.
- **Findings anchor at the import line**, so `-diff` mode gates brand-new edges
  and `metricgate: ignore import-cycle …` silences exactly one of them.
- The same constraints are rendered into `metricgate context` and
  `get_constraints`, so a prompt states the layering the gate will enforce.

Dogfooding note: switching this on for metricgate itself reported that the
checker core depended on the package that shells out to git. The fix was a real
split — `internal/diff` (pure line-range arithmetic, core) versus
`internal/git` (the git adapter) — which is exactly the kind of fact this check
exists to surface.

## Duplicate-code gate (clone detection)

The "reuse before you generate" rule, made deterministic: every run also
fingerprints every top-level function body (Go) / method and constructor body
(Java). Identifiers, type names and literals are normalised away; operators
and punctuation stay exact — so a renamed, reformatted copy of an existing
function is caught (Type-2 clones), while `a+b` versus `a-b` never is. Comments
and layout never affect the fingerprint.

```yaml
clones:
  enabled: true      # the zero value / false disables the pass entirely
  min_lines: 10      # functions shorter than this are never reported
  min_tokens: 60     # ...nor are token-poor ones
```

- Reported as `function-clone` (warning) on the **duplicate**, pointing at the
  original: `function is a Type-2 clone of internal/x/x.go:12 (Run); reuse or
  parameterise it instead of duplicating`.
- The anchor (first member of the class) is never reported; the finding anchors
  on the duplicate's body, so `-diff` mode gates brand-new duplicates.
- `metricgate: ignore function-clone …` silences one duplicate in place.
- `metricgate context` and MCP `get_constraints` render the same classes as a
  **"## reuse before you generate"** section — the model sees the original
  before it writes a copy.

Dogfooding note: the first run of this gate on metricgate itself reported four
real clones, including a test helper copied between two packages. The fix was a
real extraction (`internal/parsetest`), not a threshold bump — which is the
point of the check.

### Clone drift (`metricgate clonedrift`)

A related failure mode: a change fixes one copy of a clone but not its twin, so
the two silently diverge (research on consistent vs inconsistent clone changes).
`metricgate clonedrift` compares the **base revision** against the working tree
and reports each pair where one side's body was edited and the twin's was not:

```bash
metricgate clonedrift .               # vs HEAD
metricgate clonedrift . -base origin/main -format json
```

It only fires when both copies still exist, exactly one side intersects the
diff, and the bodies have diverged — removing a copy (de-duplication) or
updating both the same way is not drift. Reported as `clone-drift` (warning),
anchored on the changed side and pointing at the untouched twin.

## Suppression (inline ignore directives)

Legitimate exceptions are silenced in place, visibly (the report counts what
was suppressed):

```go
fmt.Println("boot")        // metricgate: ignore banned-fmt-Print* CLI entrypoint

// metricgate: ignore params-count legacy signature, do not break callers
func Parse(a, b, c, d, e, f, g int) int { ... }

// metricgate: ignore-begin cyclomatic-complexity
... generated table dispatch ...
// metricgate: ignore-end
```

Scope: same line (+ the next line when the comment starts its own line),
or a begin/end range. Rule list is comma-separated; `all` or omitted means
everything; the rest is a free-form reason. Suppressed findings never trip
the gate but are counted in the report ("N findings suppressed").

## Diff-only mode (incremental adoption)

Legacy codebase with thousands of pre-existing violations? Gate only the new
code — the old debt stays visible in full runs but never blocks the gate:

```bash
./metricgate check -diff                    # working tree vs HEAD
./metricgate check -diff -base origin/main  # PR semantics (diffs from merge-base)
```

Semantics:

- **rule findings**: kept when their line is part of the diff
- **function-scoped metrics** (complexity, length, params, nesting): kept
  when the touched range intersects the function — "you touched it, you own it"
- **file-length**: only brand-new files are flagged; growing legacy files do
  not re-alarm
- **untracked (non-ignored) files** count as fully new

The gate verdict uses the filtered result; the header shows how much was
hidden. Metric snapshots stored for trends are always the full measurements.

Policy example (full reference in the generated `policy.yaml`):

```yaml
gate:
  fail_on: error          # exit 1 on any error-severity finding
languages:
  go:
    banned-api:                      # 组织约束：改配置，不改代码
      - match: fmt.Print*
        reason: use the project logger
    rules:
      banned-fmt-Print*:             # 生成的规则 id，可单独调
        severity: error
        options:
          allowed_files: ["cmd/tool/main.go"]   # suffix allow-list
    metrics:
      cyclomatic-complexity: { warn: 10, error: 15 }
```


## Review bundle (delegate mode for a host agent)

`metricgate bundle` turns a change set into one deterministic, versioned,
budgeted JSON envelope: the current diff, path selection, active policy
constraints, and compact facts. metricgate itself never calls an LLM; the host
agent reasons over the envelope. The same builder backs MCP
`get_review_bundle`, so the CLI and MCP schemas cannot drift.

```bash
metricgate bundle . -max-chars 6000            # working tree vs HEAD
metricgate bundle . -base origin/main          # PR semantics (merge-base)
metricgate bundle . -from base -to head        # explicit range
metricgate bundle . -commit <sha>              # single commit
metricgate bundle . -token-budget 1500         # approximate token budget
metricgate bundle . -include-source -out r.json  # opt-in full source (0600)
```

- Budget applies to the **complete serialized JSON envelope**, not an inner
  fragment. Atomic items are packed with required items first (constraints and
  the current diff are never silently dropped); optional facts are removed
  deterministically until it fits, else the build returns `budget_exhausted`.
- Only the diff and selected facts are sent by default; full source is opt-in
  (`-include-source`). Absolute machine paths never enter the bundle (`run.root`
  is relative), and `-out` writes a `0600` file.
- `-max-chars` reports an exact UTF-8 byte count; `-token-budget` uses a bytes/4
  **estimate** and the envelope marks `"exact": false` — it never claims a hard
  model-token guarantee.

## Architecture

```
policy.yaml（约束模型）
     │
     ▼
  Engine（纯库：无打印、无文件写、无网络）
  ├── parser   tree-sitter grammar per language
  ├── metrics  AST visitors → complexity / size / nesting
  ├── rules    tree-sitter query + Go filter（结构规则 + policy 编译出的 banned-api）
  ├── arch     包依赖图：分层边界 / 禁止边 / 循环依赖（同一次 parse，whole-run pass）
  └── thresholds → findings（source: rule | metric）
     │
     ▼
  CLI (check/metrics/init) · SARIF · SQLite trends · (v2: HTTP/SDK, LLM loop)
```

Design invariants:

1. **Core is a pure library.** CLI, service and future LLM stages are shells —
   and the policy's `architecture:` section proves it on every run.

2. **One policy, many consumers.** The checker validates code against the
   constraint model today; the context generator (v2) and skeleton generator
   (v3) produce code that must pass the same gate.
3. **Findings are unified.** Deterministic and (future) LLM findings share one
   model, tagged with `source`.

## Metric definitions

See [docs/metrics.md](docs/metrics.md). Definitions follow SonarSource's
published specs where possible; deviations are documented inline.

## Roadmap

| Ver | Scope |
|-----|-------|
| v0  | Checker core + CLI + SARIF + gate + SQLite schema (this release) |
| v1  | diff-only mode ✅ · banned-api as policy data ✅ · architecture constraints ✅ · duplicate-code gate ✅ · CK class metrics (Java) ✅ · next: symbol index, Python/TS packs |
| v2  | HTTP/gRPC service (SDK), LLM adapter interface + context generator |
| v3  | Skeleton generator (IR + render backends), end-to-end LLM repair loop demo |

## Development

```bash
go test ./...      # unit + engine e2e tests
go vet ./...
gofmt -l .
```

The cross-file symbol index is also a CLI verb — "does this helper already
exist?" answered deterministically:

```bash
metricgate symbols . -q ClonePolicy      # exact: internal/config/config.go:306
metricgate symbols . -q ClonePolcy       # near-miss suggestion (typo)
```

The engine dogfoods itself: `metricgate check .` on this repository must
pass the gate (violations live under `internal/engine/testdata`, which the
default policy excludes).
