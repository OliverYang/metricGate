#!/bin/sh
# osv2policy.sh — supply-chain compatibility layer: external CLI orchestration.
#
# The metricgate binary never grows a feed subcommand; external tools stay
# external. This wrapper runs osv-scanner (the dependency-tree authority),
# captures its JSON report, and hands it to the adapter (scripts/osv2policy,
# Go) which plans/merges candidate ban rows against the policy. Humans review,
# fixture-smoke (SOP steps 2-5) and commit — automation stops at the patch
# file, never auto-promoting into the live policy.
#
# Usage:
#   scripts/osv2policy.sh <project-dir> [policy.yaml] [patch-out.yaml]
#
# Env overrides:
#   OSV_SCANNER  path to the osv-scanner binary (default: osv-scanner on PATH)
#   SKIP_SCAN    path to an existing osv-scanner JSON report (offline mode)
set -eu

DIR="${1:?usage: osv2policy.sh <project-dir> [policy.yaml] [patch-out.yaml]}"
POLICY="${2:-policy.yaml}"
OUT="${3:-}"

OSV_SCANNER="${OSV_SCANNER:-osv-scanner}"

if [ -n "${SKIP_SCAN:-}" ]; then
  REPORT="$SKIP_SCAN"
else
  if ! command -v "$OSV_SCANNER" >/dev/null 2>&1; then
    echo "osv2policy.sh: $OSV_SCANNER not found." >&2
    echo "  install: brew install osv-scanner   (or)   go install github.com/google/osv-scanner/v2/cmd/osv-scanner@latest" >&2
    echo "  offline: OSV_SCANNER=... or SKIP_SCAN=/path/to/report.json ./scripts/osv2policy.sh ..." >&2
    exit 127
  fi
  REPORT="$(mktemp /tmp/osv-report.XXXXXX.json)"
  trap 'rm -f "$REPORT"' EXIT
  # --format json puts the report on stdout, everything else on stderr.
  "$OSV_SCANNER" scan --format json "$DIR" > "$REPORT"
fi

ADAPTER="go run ./scripts/osv2policy"
if [ -x "$(dirname "$0")/osv2policy-bin" ]; then
  ADAPTER="$(dirname "$0")/osv2policy-bin"
fi

if [ -n "$OUT" ]; then
  $ADAPTER -in "$REPORT" -config "$POLICY" -out "$OUT"
else
  exec $ADAPTER -in "$REPORT" -config "$POLICY"
fi
