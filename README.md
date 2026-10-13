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

## Capabilities

The full inventory — every CLI subcommand, every MCP tool, every rule and
metric, the whole policy schema, and an honest list of what it does *not* do —
lives in **[docs/capabilities.md](docs/capabilities.md)**. The highlights:

- **Languages**: 10, all via tree-sitter — Go, Java, TypeScript, TSX,
  JavaScript, Python, C, C++, C#, Rust. `.tsx` gets its own grammar (the
  TypeScript grammar has no JSX nodes) and shares every layer above the
  parse with TypeScript. Parsing is fault-tolerant: a syntax
  error degrades to an info finding instead of failing the run, so
  half-written generated code still gets a verdict.
- **Rules**: `empty-block`, `todo-comment`, `print-stack-trace`,
  `unknown-dependency`; policy-compiled `banned-*`; whole-graph
  `layer-violation` / `forbidden-edge` / `import-cycle` / `sdp-violation`;
  cross-file `function-clone` / `clone-drift` / `metric-regression`.
- **Metrics**: 6 language-level + 5 CK class metrics (class-bearing
  languages) + a function-level maintainability index + 5 package metrics
  ([definitions](docs/metrics.md)).
- **Data-driven bans**: `banned-api:` entries in `policy.yaml` compile into the
  same deterministic pipeline — an organisation adds a constraint by editing
  YAML, not Go ([details](#banned-api-as-policy-data)).
- **Architecture constraints**: layer boundaries, forbidden edges, import
  cycles and stable-dependency violations, anchored at the import line
  ([details](#architecture-constraints)).
- **Duplicate code**: Type-2 clone gate, near-clone scan, and clone-drift
  detection ([details](#duplicate-code-gate-clone-detection)).
- **Completeness audit**: `list_stubs` / `metricgate`'s stub scan finds empty
  bodies, `panic("not implemented")` and `UnsupportedOperationException` with
  their exact definition sites — a repair worklist for generated code.
- **Navigation at 0 tokens**: `symbols` / `impact` / `map` (PageRank importance)
  / `hotspots` (churn × debt) / `coupling` (files that change together) / `stats`
  (min/p50/p90/p95/max + top-N). `map` and `hotspots` also carry a 0–100 static
  risk score.
- **Incremental adoption**: `check -diff` gates only new code; `-regress` also
  reports measurements that got worse than `-base`; `api snapshot` / `api diff`
  gates the exported API surface; `arch snapshot` / `arch diff` gates the
  package dependency shape.
- **One policy, with provenance**: `policy.yaml` is the constraint model;
  `rules explain` reports where every value came from
  (`builtin | policy_file | preset:NAME`), the `agentic` preset retunes the gate
  for model-written code, and `metricgate: ignore` suppresses in place. Without
  `-config` the policy is discovered: `.metricgate.yaml` →
  `.metricgate/config.yaml` → `policy.yaml`, nearest ancestor first, stopping at
  the git worktree root, falling back to built-in defaults. The cockpit's
  configuration view shows the chain, the effective keys with provenance, and
  saves edits back atomically.
- **Outputs**: text, JSON, SARIF 2.1.0 (GitHub code scanning), local SQLite
  trend store.
- **Integration**: 18 CLI subcommands and an MCP stdio server exposing 11 tools
  ([details](#mcp-server-call-it-from-your-coding-agent)).
- **CI gate**: exit 1 on violations.

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

# 6. where to read first, at 0 tokens
./metricgate map .                          # files ranked by PageRank importance (+ risk)
./metricgate hotspots . -since "90.days ago"  # churn × maintainability debt
./metricgate coupling . -since "90.days ago"   # files that change together (cross-layer = erosion)
./metricgate stats . -top 5                 # per-metric distribution + worst scopes
./metricgate impact . -q ClonePolicy        # every call site of a symbol
```

## CI gate (60-second setup)

Add to `.github/workflows/gate.yml` in any Go/Java/TS/JS/Python/C/C++/C#/Rust
repository — no API key, no secrets, no data ever leaves the runner:

```yaml
name: gate
on: { pull_request: { branches: [main] } }
permissions: { contents: read }
jobs:
  gate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }   # metricgate needs the merge-base
      - uses: <this-repo>/actions/metricgate-check@v1
        with: { base: origin/main }
```

What you get:

- **Diff-scoped by default**: only lines your PR adds are gated. Legacy debt
  never turns the gate red — the first run on an old codebase is green, and
  the gate can only ratchet.
- **Blocking check**: error-severity findings on changed lines fail the job;
  wire it into branch protection as a required check.
- **Machine-readable output**: `findings-json` / `sarif-file` / `errors` /
  `warnings` action outputs for downstream steps (code scanning, dashboards).

The full-featured version this repo runs on itself — check-run annotations,
a self-updating PR comment, artifact upload, and a weekly whole-tree scan —
lives in [`.github/workflows/`](.github/workflows/metricgate-pr.yml).

## MCP server (call it from your coding agent)

metricgate ships an MCP (Model Context Protocol) stdio server so agents can
read this repository's own constraints before generating, and check what they
actually produced afterwards — the facts and the verdicts come from the same
deterministic engine either way. What that is worth in an agent loop is
[measured, not asserted](docs/evaluations/evaluation-hint-and-cost.md): the honest scorecard
is in [docs/claims.md](docs/claims.md).

```bash
go build -o metricgate-mcp ./cmd/metricgate-mcp
claude mcp add metricgate -- /path/to/metricgate-mcp -config /path/to/policy.yaml
```

To also find out whether the gate is *actually called* in a loop, turn on the
local call log (off by default, local file only, never records the code
argument) and read it back:

```bash
metricgate-mcp -config policy.yaml -calllog .metricgate/mcp-calls.jsonl
metricgate calls          # call counts by phase, intercept rate, converge rate
```

Wiring instructions — a paste-ready agent-instruction template, a pre-commit
hook, a CI job, and what the instrumentation can and cannot tell you — are in
**[docs/workflow.md](docs/workflow.md)**.

Tools exposed:

| Tool | Purpose |
|------|---------|
| `get_constraints` | dense fact+constraint pack to paste into the prompt BEFORE generating (signatures, imports, rules, limits) |
| `check_code` | deterministic check of in-memory code after EVERY edit — 0 tokens, milliseconds |
| `check_path` | check files on disk (optionally diff-only) |
| `list_stubs` | repair worklist: empty bodies, `panic("not implemented")`, `UnsupportedOperationException` — each with its exact definition site |
| `find_symbols` | deterministic cross-file symbol index: exact lookups + near-miss suggestions (typos, case twins), 0 tokens |
| `get_impact` | what uses this symbol: every call site and type reference, plus the files affected if it changes — call before a rename |
| `get_repo_map` | every file ranked by how much the code leans on it (reference graph + PageRank) — call first to decide what to read |
| `get_hotspots` | files ranked by churn × maintainability debt over a git window, each annotated with its risk score |
| `get_metric_stats` | distribution of every metric (count/min/p50/p90/p95/max/mean) + worst scopes, e.g. the most complex functions |
| `get_trends` | the local trend database: per-run severity series, time range, and the rules that fired most — read-only |
| `get_review_bundle` | deterministic, budgeted review input (diff + selection + constraints + facts) for a host agent; metricgate never calls an LLM |

Each tool shares its implementation with the matching CLI verb
(`get_repo_map`↔`map`, `get_hotspots`↔`hotspots`, `get_metric_stats`↔`stats`,
`get_trends`↔`trends`, `get_review_bundle`↔`bundle`), so the two surfaces
cannot drift. Full argument lists and result schemas are in
[docs/capabilities.md §10.2](docs/capabilities.md).

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
([docs/research/roadmap-research.md](docs/research/roadmap-research.md)), not AST territory.

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

`metricgate suppressions` lists every directive in the tree together with the
author's reason, flags the ones written without one, and shows the
acceptance ledger (named, expiring concessions) beside them — the two systems
are complementary and neither is visible from the other's output.

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
  when the touched range intersects the function **and** the value rose
  since the base revision — see below
- **file-length**: only brand-new files are flagged; growing legacy files do
  not re-alarm
- **untracked (non-ignored) files** count as fully new

The gate verdict uses the filtered result; the header shows how much was
hidden. Add `-hidden` to list the hidden findings and the metrics dropped
for an unchanged value, so "0 new findings" is a claim you can audit. Add
`-format json` for the same verdict as machine-readable fields
(`errors`, `warnings`, `verdict.accepted`, `verdict.suppressed`) — the gate
line and the JSON come from one function and cannot disagree. Metric
snapshots stored for trends are always the full measurements.

When the gate fails, the output lists every sanctioned path out — fix,
suppress with a `metricgate: ignore` directive, concede with
`metricgate accept add`, adjust the threshold, or list what was hidden.
`metricgate suppressions` shows both concession systems side by side.

### Why the value test exists

A line-range test alone cannot tell "you made this worse" from "you touched
something that was already bad". Class-level metrics span the whole class
body, so editing any method inside a class re-reports all five CK values —
with numbers that may not have moved at all. Measured on a legacy Java
service (a three-line null guard added to two methods):

```
before  diff-only: base=HEAD~1, 14 new findings, 9665 pre-existing hidden
        10 errors, gate FAILED

after   diff-only: base=HEAD~1, 11 new findings, 9665 pre-existing hidden,
                    3 unchanged metric(s) dropped
         7 errors, gate FAILED
```

Across five commits of that repository the errors dropped from 31 to 16, and
one commit went from 4 errors to none — the three CK values it re-reported
were byte-identical before and after. The metric findings that survive are
the ones whose number actually rose, which is the claim a reviewer can act
on. Findings whose scope does not exist at the base revision (new code) are
always kept: a threshold crossed by new code is the case the gate exists for.

The header reports dropped unchanged metrics separately from pre-existing
hidden findings, because "untouched" and "touched but unchanged" are
different facts about a change.

This requires the base revision's measurements, so `-diff` runs the engine
over a `git archive` of the touched files only. When that is not possible
(no git repository, or the MCP server's in-memory path), the value test is
skipped and the line-range behaviour above applies unchanged.

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
  CLI (check/metrics/context/bundle/map/stats/hotspots/…) · MCP stdio · SARIF · SQLite trends
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
| v0  | Checker core + CLI + SARIF + gate + SQLite schema ✅ |
| v1  | diff-only mode ✅ · banned-api as policy data ✅ · architecture constraints ✅ · duplicate-code gate ✅ · CK class metrics (Java) ✅ |
| v1+ | symbol index ✅ · impact ✅ · repo map (PageRank) ✅ · hotspots ✅ · metric distribution ✅ · static risk score ✅ · 9-language packs ✅ · **next: 审美回路补全 — [docs/roadmap.md](docs/roadmap.md)（accept 账本 / 规则 DSL / metric trend / 修复建议 / deps scan）** |
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
metricgate symbols . -q ClonePolicy      # exact: internal/config/config.go:312
metricgate symbols . -q ClonePolcy       # near-miss suggestion (typo)
```

The engine dogfoods itself: `metricgate check .` on this repository must
pass the gate (violations live under `internal/engine/testdata`, which the
default policy excludes). Exit codes: `0` gate passed, `1` gate failed,
`2` usage or environment error (unknown flag, no git repository for `-diff`,
an unresolvable `-base`). There is no `doctor` verb — a fresh checkout is
diagnosed by `metricgate check . -diff -base HEAD`.
