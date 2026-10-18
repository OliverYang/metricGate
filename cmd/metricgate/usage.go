package main

import (
	"fmt"
	"os"
)

// usageText is the help body. It lives in a constant rather than inside the
// function so that adding a verb is a one-line edit in a list, not a change to
// a function that must stay short.
const usageText = `metricgate %s — deterministic code metrics and constraint gates

usage:
  metricgate init [file] [flags]      write a starter policy (default .metricgate.yaml)
                                      -preset NAME starts from a built-in gate
                                      tuning (e.g. agentic)
  metricgate bundle [path] [flags]     deterministic, budgeted review input for a
                                      host agent (diff + constraints + facts)
  metricgate check [path] [flags]     run rules + metrics, enforce the gate
  metricgate check -diff [-base ref]  only fail on violations in changed lines
  metricgate check -diff -regress     also report measurements that got worse
                                      than -base without crossing a threshold
  metricgate context [path] [flags]   dense constraint pack for LLM prompts
  metricgate constraints digest [-pkg P]  task-scoped constraint digest (-format json = provenance)
  metricgate metrics [path] [flags]   print the metric table only
  metricgate stats [path] [flags]     metric distributions (min/p50/p90/p95/max)
                                      plus the worst scopes per metric
  metricgate symbols [path] -q NAME   look a symbol up in the cross-file
                                      definition index (near-miss suggestions
                                      when the exact name is unknown)
  metricgate impact [path] -q NAME    what uses this symbol: every call and type
                                      reference, and the files it would affect;
                                      -depth N walks N hops (2 = who calls the
                                      callers) with per-name tiers, and
                                      -max-affected caps the file width
  metricgate reachability [path]        symbols nothing references, and ones no test names (report)
  metricgate map [path] [flags]       rank files by reference importance: the
                                      whole-repo "read these first" list
  metricgate rules explain list       every rule/metric and where its value came
                                      from (builtin | policy_file | preset:NAME)
  metricgate rules explain <ruleID>   the effective config of one rule or metric
  metricgate rules presets            the built-in gate presets and every value
                                      each one changes
  metricgate rules gaps [path]        audit policy completeness: which rules can
                                      fire, which are silent, and which packages
                                      no declared layer covers
  metricgate explain [path]           preview what would be scanned (no rule execution)
  metricgate api snapshot -out F      capture the exported API surface
  metricgate api diff -base F [path]  breaking-change gate against a snapshot
                                      (removed/changed signatures exit 1)
  metricgate arch snapshot -out F     capture the package dependency shape
  metricgate arch diff -base F [path] shape-drift gate against a snapshot
                                      (any added/removed edge exits 1)
  metricgate rank F1 F2 [...]         order candidate implementations by findings
                                      (deterministic best-of-N discriminator)
  metricgate nearclones [path]        functions sharing >=80%% of their normalised
                                      tokens (copy+rename+small edit)
  metricgate hotspots [path]          churn × maintainability-debt hotspots
  metricgate reachability [path]        symbols nothing references, and ones no test names (report)
  metricgate coupling [path]          files that change together without a
                                      dependency — cross-layer pairs are
                                      architectural erosion in progress
  metricgate clonedrift [path]         flag a change that edits one copy of a clone
                                      but not its twin (needs -base, default HEAD)
  metricgate trends [flags]           severity trend + top rules from the local
                                      history.db (P2 v1; needs store.enabled)
                                      -trace/-model/-prompt-digest scope the window
  metricgate trace <traceID> [path]   one trace in full: its runs, findings,
                                      attributes and MCP calls joined by trace id
  metricgate calls [flags]            reduce the local MCP call log to call
                                      counts, intercept rate and fix rate — the
                                      evidence the gate is used in an agent loop
  metricgate accept add/remove/list   manage the concession ledger
  metricgate suppressions [path]       list both concession systems: inline directives + the ledger
                                      (.metricgate/accepted.yaml): accepted
                                      warnings stay visible but pass the gate
  metricgate deps scan [flags]        draft dependencies.allow from the import
                                      face (what the code imports minus what
                                      manifests/policy already declare)
  metricgate cockpit serve [flags]    serve the local audit dashboard on loopback
  metricgate cockpit update [flags]   check the release feed manually
  metricgate store prune [-dry-run]   apply store.retain to the history database:
                                      retire old runs with their findings and
                                      metric snapshots (-runs/-days override;
                                      DELETE frees pages without shrinking the
                                      file, -vacuum returns them; see
                                      docs/store-retention.md)
  metricgate version                  print the version
`

func usage() {
	fmt.Fprintf(os.Stderr, usageText, toolVersion)
}
