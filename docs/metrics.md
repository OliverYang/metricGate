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
  separate functions; `static` and instance initializer blocks are counted
  nowhere (see "the file-scope function census")
- `a, b` style multi-name parameters count per name; varargs count as 1

## wmc (Weighted Methods per Class) — class-bearing languages

Sum of the cyclomatic complexity of every method and constructor declared by
the class or interface. A bodyless (abstract, or interface) method contributes
the minimum of 1: the class still pays for the dispatch it promises. Defaults:
warn >40, error >60.

## nom (Number of Methods) — class-bearing languages

Methods plus constructors declared directly by the class (nested types belong
to themselves). Defaults: warn >20, error >30.

## lcom (Lack of Cohesion of Methods) — class-bearing languages

The original Chidamber–Kemerer definition: over all method pairs, P is the
number of pairs sharing **no** field and Q the pairs sharing at least one;
the metric is max(0, P−Q). Field usage is name-based — a local shadowing a
field name counts as a share, which can only bias the number **down** (more
cohesive), the conservative direction. Classes with fewer than two methods or
no fields measure 0. Defaults: warn >20, error >40 (advisory: the strict CK
definition flags real designs often — tune before raising the gate).

## cbo (Coupling Between Objects) — class-bearing languages

Distinct type names referenced by the class other than itself and the Java
primitives (`int`, `boolean`, …). Name-based: JDK types count too (coupling
to the platform is still coupling). Nested types are boundaries — their
references are theirs, not the outer class's. Defaults: warn >15, error >25.

## dit (Depth of Inheritance Tree) — class-bearing languages

Depth of the inheritance chain: a superclass declared in the same file
contributes its own depth + 1; a superclass declared elsewhere counts as one
level (cross-file resolution belongs to the symbol index, not the per-file
pass). Implementing an interface never adds depth; an interface extending one
does. Cycles terminate defensively. Defaults: warn >5, error >8.

### Which languages produce the CK family

Java, TypeScript, TSX, JavaScript, Python, C++, C, C# and Rust each declare
classes or structs in their own grammar; `internal/metrics/classes_lang.go` holds
the per-language spelling table and `HasClasses(lang)` decides whether the five
are advertised for a language. **Go produces none** — it has no class construct,
so listing WMC there would tell a Go project it has rules it can never fire.
The caveats below were written for the Java implementation and still hold; where a
language has no distinct type-reference node kind (JavaScript, Python, C#) CBO is
restricted to what the grammar actually names.

### Language notes: SQL / Verilog / VB.NET (added with the 13-language pass)

The three are supported but **deliberately not** given the CK family, and their
function-level units differ from the C-family shape. Each gap below is a
statement about the grammar, not a missing feature to be filed.

- **SQL** — function units are `create_function` and `create_procedure`
  (`internal/metrics/kindset.go`). A table, view, index or schema is a schema
  declaration, not behaviour, so it is measured by `facts` but never as a
  function. There is no class construct, so **no CK family**. The name lives in
  a child `object_reference` and the body in `function_body`/`procedure_body`
  — neither is a `body` field — so the metrics layer resolves both by child
  kind. The grammar's control flow is only `WHILE` and `CASE` (there is no `IF`
  in routine bodies; it parses as an ERROR), and the dialects that ship in
  real migrations (CockroachDB's `SETOF`/`DO $a$ … END $a$`, MySQL's
  `DELIMITER`) fall outside the grammar's coverage and surface as
  `parse-error` findings rather than partial numbers.
- **Verilog** — function units are `function_declaration`, `task_declaration`
  and a class's `class_constructor_declaration`. **A `module` is a file-level
  container, not a function** (same call a SQL table gets). SystemVerilog
  `class`/`interface`/`package`/`program` are recorded by `facts` as containers
  with their functions as children. No `body` field and no `name` field exists
  on any node, so the adapter reads both by child kind; a `class_method`'s
  statements hang directly off the constructor, which the body resolver has to
  special-case. **No CK family** — the grammar gives no member-visibility or
  inheritance model to build LCOM/DIT from. The call spelling is `tf_call`
  (callee = first named child); `method_call` is scored but the upstream grammar
  turns most `obj.method()` spellings into ERRORs, so fan-out under-reports
  receiver-chained calls.
- **VB.NET** — function units are `method_declaration` and
  `constructor_declaration` (the latter has no name field and is reported as
  `New`). There is **no CK family**: `class_block` exists but the grammar gives
  no member-visibility or inheritance model for the CK family. **Member names
  are normalised to lower case** (`Me.Value` = `me.value`) so a rename of a
  case-only difference does not read as two symbols. `boolean-params` counts
  `As Boolean` parameters (`countBoolParamsVB`) rather than falling through to
  the Go/Java-shaped default, which cannot see VB's `as_clause` type spelling.
  Five upstream-grammar gaps are real, each reproducible as a minimal file and
  asserted in `internal/metrics/vb_metrics_test.go` instead of papered over:
  generic type arguments (`List(Of Boolean)`); any `Implements` clause, at
  class level or on a member, bare or dotted (`Implements IBar` and
  `Implements IBar.F` both fail while the interface-less class parses); an
  attribute on a member (`<Obsolete> Sub F()`, where a file-level
  `<Assembly: …>` is fine); a class-level `#Region` (the same directive inside
  a method body is fine); and a file with no trailing newline. Separately,
  `AndAlso`/`OrElse` emit no operator token at all, so VB logical operators are
  **not** scored (a documented cyclomatic under-count, not a parse error).
  Files hit by a gap surface as `parse-error` findings, never as partial
  numbers.

### Deviations from strict CK (all deliberate, all conservative)

- Everything is name-based: no type resolution, no symbol tables.
- `this.field` and bare `field` are equal field shares; shadowing can only
  *lower* reported LCOM.
- CBO includes the platform by design — a name-based pass cannot tell a project
  class from a platform one, and coupling to the platform is still coupling.
- DIT outside the file under-counts (1 instead of the true depth), never
  over-counts.

### Language notes: the file-scope function census (iter-30)

JS/TS needed a structural fix (iter-29) because a module's function is usually a
*binding* (`export const handler = (x) => {…}`), and no kind set can see a
binding. The obvious follow-up question was whether the other languages have the
same hole. They do not, and `internal/metrics/func_unit_census_test.go` pins the
answer per language.

- **No language shares the JS hole**, because in every other supported language a
  file-scope `name = <closure>` is a *closure*, and closures fold into the
  enclosing unit site-wide — the rule Go's `func_literal` follows. Java's
  `static final Function<…> HELPER = x -> {…}`, C#'s `static Func<int,int> Helper =
  (x) => {…}`, C++'s `auto helper = [](int x) {…}`, Python's `helper = lambda x: …`
  and Rust's `const H: fn(i32) -> i32 = |x| {…}` all report zero units, which is
  correct under that rule, not blindness. Go reports zero for
  `var helper = func(x int) int {…}` for the same reason, and Go is in the test on
  purpose: if someone promotes Java's lambda fields without touching Go, the site
  says one thing about closures and another about fields.
- **The gap that is real is a different shape.** A C# `accessor_declaration` (a
  property `get`/`set` body) and a Java `static_initializer` / instance `block` in
  class body are code that runs, they hold decision points, and they live inside
  no unit — so their branches contribute to no cyclomatic, cognitive, npath or
  nesting snapshot anywhere. A C# file written only as properties reports zero
  functions, and zero functions suppresses even the maintainability index
  (`AnalyzeFile` sets MI to 0 with no units; `engine.go` emits a maintainability
  snapshot only when MI > 0).
- **Why this is not fixed by copying the JS rule.** Promoting `accessor_declaration`
  into `csFuncKinds` was tried and measures: it yields units named `get` and `set`,
  i.e. the same fake-identifier problem iter-29 refused for `export default`, plus
  a threshold change for every Java and C# file in every existing snapshot. A real
  fix has to name them `P.get` first and be reviewed as a policy change.
- **Only four `.java` files and no `.cs` file exist in this repository**, so the
  gap cannot be measured against real local code — the census test is the evidence.

### Language notes: JavaScript / TypeScript / TSX

- function units are `function_declaration`, `generator_function_declaration`,
  `method_definition` and the class/interface declarations themselves, **plus a
  module-level binding of a function expression**: `export const handler =
  (x) => {…}` is a unit named `handler`, and so are the `function (x) {…}` and
  `async (x) => {…}` spellings of the same shape.
- The binding rule is structural, not kind-based, because one kind serves two
  purposes. `arr.map((y) => y * 2)` is an inline closure and must stay folded
  into its enclosing unit — the same rule Go's `func_literal` and Java's
  `lambda_expression` follow, and it is why a kind set alone cannot express
  this. A node is a unit only when a `const`/`let`/`var` at the top of the
  module (bare, or inside `export_statement`) binds it.
- `export default () => {…}` is **not** a unit: it has no name, and inventing
  one would put a fake identifier into every snapshot of a file that has none.
- A local binding inside a function (`function f() { const g = () => 1; }`) is
  a local and belongs to `f`, exactly like an inline closure.
- The name comes from the declarator's `name` field, since the grammar keeps it
  there and not on the function node.
- `x => x + 1` holds its single parameter in the `parameter` field as a bare
  pattern with no list node; it counts as one parameter and can never carry a
  boolean type annotation, because TypeScript requires parentheses to annotate.
- TSX is the same census as TypeScript: JSX elements appear inside function
  bodies and are walked as ordinary subtrees, so they add fan-out (the `.map`
  call) but no decision points.
- **History**: until iter-29 the module-level arrow spelling was not measured at
  all, and this file recorded that as a design note. The note was wrong about
  its own consequence — a file written entirely in that shape reported zero
  functions, zero complexity and passed every function-level threshold by being
  invisible. Fixing it exposed 27 previously unmeasured functions in this
  repository's own `internal/cockpit/web/app.js`, three of which were real
  threshold breaches.
