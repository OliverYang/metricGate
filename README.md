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
- 8 pattern rules (tree-sitter query + Go filter) —
  Go: `no-fmt-print`, `empty-block`, `panic-call`, `todo-comment`;
  Java: `system-out-print`, `print-stack-trace`, `empty-block`, `todo-comment`
- Policy file (`policy.yaml`) = the project's constraint model
- SARIF 2.1.0 output (GitHub code scanning compatible)
- Local SQLite trend store (write-only in v0)
- CI gate: exit code 1 on violations

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
```

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
    rules:
      no-fmt-print:
        severity: warning
        options:
          allowed_files: ["cmd/tool/main.go"]   # suffix allow-list
    metrics:
      cyclomatic-complexity: { warn: 10, error: 15 }
```

## Architecture

```
policy.yaml（约束模型）
     │
     ▼
  Engine（纯库：无打印、无文件写、无网络）
  ├── parser   tree-sitter grammar per language
  ├── metrics  AST visitors → complexity / size / nesting
  ├── rules    tree-sitter query + Go filter（禁用 API / 空块 / panic …）
  └── thresholds → findings（source: rule | metric）
     │
     ▼
  CLI (check/metrics/init) · SARIF · SQLite trends · (v2: HTTP/SDK, LLM loop)
```

Design invariants:

1. **Core is a pure library.** CLI, service and future LLM stages are shells.
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
| v1  | diff-only mode ✅ · next: suppression comments, Python/TS rule packs, trend report |
| v2  | HTTP/gRPC service (SDK), LLM adapter interface + context generator |
| v3  | Skeleton generator (IR + render backends), end-to-end LLM repair loop demo |

## Development

```bash
go test ./...      # unit + engine e2e tests
go vet ./...
gofmt -l .
```

The engine dogfoods itself: `metricgate check .` on this repository must
pass the gate (violations live under `internal/engine/testdata`, which the
default policy excludes).
