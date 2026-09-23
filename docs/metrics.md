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
