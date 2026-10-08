// This file holds the built-in starter policy: the document `metricgate init`
// writes and the values `config.Default` falls back to. It is kept apart from
// config.go so the loader stays about loading.
package config

// DefaultYAML is the policy emitted by `metricgate init` and used when no
// policy file is present.
const DefaultYAML = `# metricgate policy — the constraint model for this repository.
# All values below are the defaults; delete lines you do not need to override.
version: 1
# preset: agentic   # opt into a built-in gate tuning; list them with "metricgate rules presets"

gate:
  # Exit code 1 when any finding at or above this severity is present.
  fail_on: error

# Directory names to skip while walking the tree.
exclude: [".git", "vendor", "node_modules", "testdata"]

languages:
  go:
    rules:
      empty-block:
        severity: warning
      todo-comment:
        enabled: false
        severity: info
    metrics:
      cyclomatic-complexity: { warn: 10, error: 15 }
      cognitive-complexity:  { warn: 15, error: 25 }
      function-length:       { warn: 50, error: 100 }
      file-length:           { warn: 400, error: 800 }
      nesting-depth:         { warn: 4,  error: 6 }
      params-count:          { warn: 5,  error: 8 }
    # Organisation bans, evaluated by the same deterministic engine. A trailing
    # "*" is a name prefix; except_files lists file-name suffixes to skip;
    # kind: import bans a package import instead of a call. Override severity
    # or add allowed_files under rules.<id>, where <id> is "banned-" plus the
    # match with dots replaced by dashes (e.g. banned-fmt-Print*).
    banned-api:
      - match: fmt.Print*
        reason: direct fmt.Print* writes to stdout; use the project logger
      - match: panic
        reason: direct panic(); return an error so callers can decide
        except_files: ["_test.go"]

  java:
    rules:
      print-stack-trace:
        severity: warning
      empty-block:
        severity: warning
      todo-comment:
        enabled: false
        severity: info
    metrics:
      cyclomatic-complexity: { warn: 10, error: 15 }
      cognitive-complexity:  { warn: 15, error: 25 }
      function-length:       { warn: 50, error: 100 }
      file-length:           { warn: 400, error: 800 }
      nesting-depth:         { warn: 4,  error: 6 }
      params-count:          { warn: 5,  error: 8 }
      # CK class metrics (per class/interface):
      wmc:                   { warn: 40, error: 60 }
      nom:                   { warn: 20, error: 30 }
      lcom:                  { warn: 20, error: 40 }
      cbo:                   { warn: 15, error: 25 }
      dit:                   { warn: 5,  error: 8 }
    banned-api:
      - match: System.out.print*
        reason: direct System.out.print* writes to the console; use the project logger
      - match: System.err.print*
        reason: direct System.err.print* writes to the console; use the project logger


# Architecture: the dependency directions this repository must keep.
# Patterns match package names on their trailing segments, so "internal/rules"
# also matches "yourmodule/internal/rules":
#   internal/rules   that package only
#   internal/*       direct children of internal
#   internal/**      internal and everything below it
# A package in no layer, and any dependency on code outside every layer, is
# unconstrained: describe only the part you care about.
# architecture:
#   edges: error     # severity of layer / forbidden-edge violations (default warning)
#   cycles: error    # severity of import cycles (default warning)
#   sdp: warning     # severity of stable-dependencies violations; "off" records the
#                    # package metrics but emits no finding (default warning)
#   sdp_tolerance: 0.10  # ignore instability gaps at or below this: instability is a
#                        # ratio over integer coupling counts, so gaps below about
#                        # 1/(Ca+Ce) are rounding noise, not design debt. 0 = strict.
#   layers:
#     - name: handler
#       packages: ["internal/handler/**"]
#       may_depend_on: [service]
#     - name: service
#       packages: ["internal/service/**"]
#       may_depend_on: [storage]
#   forbid:
#     - from: internal/storage/**
#       to: internal/handler/**
#       reason: storage must not know about HTTP

store:
  enabled: true
  path: ".metricgate/history.db"
`
