#!/bin/sh
# post-edit.sh — put the gate's verdict in front of the model, right after it edits.
#
# WHAT THIS IS NOT
#   It is not an interceptor. By the time a harness fires a post-edit hook the
#   edit has already happened; nothing here can undo it. `exit 2` does exactly
#   one thing: it makes the harness show this script's stderr to the model on
#   its next turn. That is the whole point -- the model edited, we want it to
#   see the verdict and go fix the code. Do not advertise it as "blocking".
#
# SCOPE (decided in docs/iterations/iter-26-post-edit-hook.md §4)
#   Default: check only the file that was just edited. Measured 1.26-1.31 s and
#   10 metric snapshots on this repository, versus 4.40-4.93 s and ~24k
#   snapshots for a whole-tree `check . -diff`. Fast feedback is the entire
#   value of a post-edit hook; a multi-second pause per edit is what makes
#   people (and agents) route around it.
#   The narrow scope does NOT cover cross-file rules -- layering, fan-out,
#   clone detection need the whole tree. Those are covered by pre-commit, which
#   runs `check . -diff`. The trade is deliberate and is printed in the verdict
#   line below so a passing file never reads as "the whole repo is clean".
#   When the harness gives us no file path we fall back to the whole tree
#   rather than skipping: a slow verdict beats no verdict.
#
# STORE GROWTH
#   Every `check` stores a run in .metricgate/history.db. Narrow scope keeps
#   that to 10 snapshots per edit instead of ~24k. See iter-25 for the retention
#   policy; this hook deliberately does not grow a store flag of its own.
#
# Exit codes: 0 nothing to report, 2 the gate found something (verdict on
# stderr). See docs/workflow.md §3.2 for the harness wiring.
set -u

bin=${METRICGATE:-metricgate}
if ! command -v "$bin" >/dev/null 2>&1; then
    # Same fall-forward chain as hooks/pre-commit: a fresh clone with the
    # dogfooded binary at the repo root works with no PATH setup, while an
    # explicitly named binary that is missing stays an error.
    if [ -x "$PWD/metricgate" ]; then
        bin=$PWD/metricgate
    else
        echo "post-edit: $bin not found; skipping the gate. Set METRICGATE to its path." >&2
        exit 0
    fi
fi

# The harness passes the tool call as JSON on stdin, e.g.
#   {"tool_name":"Edit","tool_input":{"file_path":"/abs/path/to/file.go"}}
# Only read it when stdin is a pipe: `cat` on an interactive terminal would
# hang the editor forever, and a hook that hangs is worse than no hook.
payload=""
if [ ! -t 0 ]; then
    payload=$(cat)
fi

# Pull file_path out with sed rather than a JSON parser. The payload is a flat
# object with no nesting inside the value we want, and the alternative is
# depending on python/jq being installed wherever the harness runs.
file=$(printf '%s' "$payload" | sed -n 's/.*"file_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)

status=0
if [ -n "$file" ]; then
    if [ -e "$file" ]; then
        scope="post-edit: checked $file only (cross-file rules: layering, fan-out, clones run at pre-commit)"
        out=$("$bin" check "$file" 2>&1) || status=$?
    else
        # The path is gone (rename, delete). Checking a missing path makes
        # metricgate exit 2 with a stat error, which would read as a violation
        # and send the model chasing a ghost. Say so instead.
        echo "post-edit: $file no longer exists; nothing to check" >&2
        exit 0
    fi
else
    scope="post-edit: checked whole tree with -diff"
    out=$("$bin" check . -diff 2>&1) || status=$?
fi

# Only a real verdict may become exit 2. Two ways `check` exits non-zero are
# NOT violations: the store being locked by a concurrent writer (SQLITE_BUSY,
# which is routine when another agent or a CI job is checking at the same
# time) and infrastructure errors such as a missing config. Reporting those as
# violations would send the model hunting for a defect that does not exist.
# They still get printed -- a silent skip is the failure mode R1 warns about --
# but the exit stays 0.
case "$out" in
    *"gate: FAILED"*)
        printf '%s\n' "$scope" >&2
        printf '%s\n' "$out" >&2
        exit 2
        ;;
esac

if [ "$status" -ne 0 ]; then
    printf '%s\n' "$scope" >&2
    printf 'post-edit: the gate could not produce a verdict (exit %s); not reporting a violation\n' "$status" >&2
    printf '%s\n' "$out" >&2
    exit 0
fi

# Passing edits get the scope line too: it is one short line, and it is the
# only thing standing between "PASSED" and the model concluding the whole
# repository is clean.
printf '%s\n' "$scope" >&2
exit 0
