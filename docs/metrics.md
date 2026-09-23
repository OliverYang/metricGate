# Metric definitions

Every metric below is computed from the tree-sitter AST in one pass.
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
  excluding `default:` which opens no new path), the ternary `?:`,
  every `catch` clause and each `&&` / `||`
- cognitive: `if` / loops / `switch_expression` follow the `+1+nesting`
  rule, `catch` adds +1 flat, lambdas add a nesting level
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
