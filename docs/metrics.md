# Metric definitions

Every metric below is computed from the tree-sitter AST in one pass. Decision
points are not priced off the AST directly, though: a language adapter lowers
the tree into a small language-agnostic IR (`internal/metrics/ir.go`) and one
engine (`internal/metrics/engine.go`) scores it, so a construct's cost is
written down exactly once for every language and adding a language means
writing an adapter — not touching the rules.

Thresholds compare with **strictly greater than** (`value > warn` → warning,
`value > error` → error). All definitions apply per Go function unless noted.

## cyclomatic-complexity

`1 +` the number of decision points:

- every `if` statement
- every `for` statement (all forms: cond, range, infinite)
- every case entry: `expression_case`, `type_case`, `communication_case`
  (`default` cases add nothing — they do not create a new path)
- every `&&` and `||` operator

## cognitive-complexity

Structural variant of the SonarSource white paper ("Cognitive Complexity: a
new way of measuring understandability", G. Ann Campbell):

- `if` / `for` / `expression_switch_statement` / `type_switch_statement` /
  `select_statement`: `+1 + current nesting`
- a run of consecutive identical logical operators (`&&` chain or `||` chain):
  `+1` per run, not per operator
- function literals add a nesting level for their body

Documented deviations from the white paper:

- `else if` chains pay the nesting increment (the white paper exempts them);
  prefer flat `switch` in hot paths — the metric now nudges you there
- labeled `break/continue` and `goto` are not counted in v0
- recursive calls are not counted in v0 either
- an exception handler is a flat `+1` and adds **no** cyclomatic path (see the
  Java notes below)

These are decisions, not gaps: `internal/metrics/engine_test.go` pins each one
with hand-computed numbers, and `legacy_diff_test.go` re-scores a corpus with
the pre-IR implementation to prove a refactor moved nothing. Changing a number
here changes every stored verdict, so treat it as a policy change.

## npath-complexity

NPATH — the number of acyclic execution paths through a function (J. M.
Nejmeh, "NPATH: a measure of execution path complexity and its applications",
CACM 31(2) 1988, doi 10.1145/42372.42379). Unlike the additive cyclomatic
score, sequential statements **multiply**: two independent ternaries are 2×2,
three 6-arm switches inside a loop are 6³+1. This is the blow-up cyclomatic
hides and why the AT&T empirical limit of 200 (the checkstyle default) flags
the "every part looks small" monolith that no single nesting/complexity
threshold catches.

Scoring table follows the checkstyle reference implementation:

- sequential statements: product of the statements' factors
- `if (c) A`: NP(A) + ops(c) + 1; `if/else`: NP(A) + NP(B) + ops(c)
- `for`/`while`/`do`: NP(body) + ops(header) + 1
- `switch`: Σ NP(arms) + ops(scrutinee), empty switch = 1
- ternary `e1 ? e2 : e3`: NP(e1) + NP(e2) + NP(e3) + 2
- expressions contribute the count of `&&`/`||` operators, scored when the
  expression sits in a condition slot (`if`/loop headers, switch scrutinee,
  ternary test)

Documented deviations (consistent with the cyclomatic/cognitive ones above):

- a `catch` handler adds no path (same convention as its flat cyclomatic +1)
- a lambda's paths multiply into the enclosing function (the body is scored
  as part of it)
- a ternary inside a condition slot contributes its 2 paths even though the
  strict table counts only logical operators there — a value selection is a
  real path
- the count saturates at 2⁶² so a pathological function cannot overflow;
  thresholds are three digits, the exact value past the cap is irrelevant

`TestNPathEndToEnd` reproduces the two worked examples from the checkstyle
page (128 = 2⁷ and 217 = 6³+1) plus a Go multiplication case, and
`TestEngineScoringRules` pins the IR-level table.

## fan-out

Number of **distinct callees** invoked inside one function — Yourdon &
Constantine's fan-out from *Structured Design* (1979): a unit that calls many
others coordinates many others, and high fan-out is the structural signature
of a function doing too much orchestration itself. The callee identity is
the whitespace-stripped spelling of the call target, so `pkg.Do` and
`obj.Method` stay distinct while a folded multi-line call compares equal to
its one-line spelling; Go function-literal invocations are excluded. Default
thresholds 10 / 20 (warn / error).

## boolean-params

Number of parameters declared `bool` (Go) or `boolean` (Java) — the
mechanical core of Fowler's *flag argument* refactoring smell: every bool
doubles the hidden states a caller must reason about, so a two-bool call
site already hides four modes that the signature does not name. Default
thresholds 2 / 4 (warn / error).

## nesting-depth

Deepest control-structure nesting reached inside the function (0 = straight
line). Function literals count as a level.

## function-length

`end_line - start_line + 1` of the declaration, blank and comment lines
included.

## file-length

Total physical lines of the file.

## params-count

Declared parameters of the function: `a, b int` counts as **2**; an unnamed
`(int)` counts as 1; a variadic `rest ...string` counts as 1. The receiver is
not counted.

## Language notes: Java

Node kinds come from the tree-sitter-java grammar:

- cyclomatic adds a point for `if`, all four loop forms (`for`,
  `enhanced_for`, `while`, `do`), each `switch` label (`switch_label`,
  excluding `default:` which opens no new path), the ternary `?:` and each
  `&&` / `||`
- cognitive: `if` / loops / `switch_expression` follow the `+1+nesting`
  rule, `catch` adds +1 flat, lambdas add a nesting level
- a `catch` clause opens **no** cyclomatic path. McCabe conventionally counts
  a handler as a branch, and this document used to say so, but the numbers
  stored to date never did — counting it now would move every Java verdict
  without a policy decision behind it, so the documentation follows the code
- the ternary is a cyclomatic path but no cognitive decision: it is an
  expression, not another layer of structure
- function units are methods and constructors; lambdas are not counted as
  separate functions
- `a, b` style multi-name parameters count per name; varargs count as 1

## wmc (Weighted Methods per Class) — Java

Sum of the cyclomatic complexity of every method and constructor declared by
the class or interface. A bodyless (abstract, or interface) method contributes
the minimum of 1: the class still pays for the dispatch it promises. Defaults:
warn >40, error >60.

## nom (Number of Methods) — Java

Methods plus constructors declared directly by the class (nested types belong
to themselves). Defaults: warn >20, error >30.

## lcom (Lack of Cohesion of Methods) — Java

The original Chidamber–Kemerer definition: over all method pairs, P is the
number of pairs sharing **no** field and Q the pairs sharing at least one;
the metric is max(0, P−Q). Field usage is name-based — a local shadowing a
field name counts as a share, which can only bias the number **down** (more
cohesive), the conservative direction. Classes with fewer than two methods or
no fields measure 0. Defaults: warn >20, error >40 (advisory: the strict CK
definition flags real designs often — tune before raising the gate).

## cbo (Coupling Between Objects) — Java

Distinct type names referenced by the class other than itself and the Java
primitives (`int`, `boolean`, …). Name-based: JDK types count too (coupling
to the platform is still coupling). Nested types are boundaries — their
references are theirs, not the outer class's. Defaults: warn >15, error >25.

## dit (Depth of Inheritance Tree) — Java

Depth of the inheritance chain: a superclass declared in the same file
contributes its own depth + 1; a superclass declared elsewhere counts as one
level (cross-file resolution belongs to the symbol index, not the per-file
pass). Implementing an interface never adds depth; an interface extending one
does. Cycles terminate defensively. Defaults: warn >5, error >8.

### Deviations from strict CK (all deliberate, all conservative)

- Everything is name-based: no type resolution, no symbol tables.
- `this.field` and bare `field` are equal field shares; shadowing can only
  *lower* reported LCOM.
- CBO includes the JDK by design — a name-based pass cannot tell a project
  class from a platform one, and coupling to the platform is still coupling.
- DIT outside the file under-counts (1 instead of the true depth), never
  over-counts.
