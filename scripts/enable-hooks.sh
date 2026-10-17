#!/usr/bin/env bash
# enable-hooks.sh — install the three hooks, without editing files by hand.
#
# WHY A SCRIPT AND NOT JUST A DOC
#   The post-edit wiring in .claude/settings.json.example needs an absolute
#   path, because the harness runs the hook from an unspecified working
#   directory. That means the example cannot be copied verbatim; it has to be
#   substituted. A sed one-liner in a README is a step people skip, and a hook
#   that was never installed is indistinguishable from a hook that passes.
#
# WHAT IT TOUCHES
#   .claude/settings.json   created from the example, absolute path substituted
#   git core.hooksPath      pointed at ./hooks, so pre-commit and pre-push fire
#
#   Nothing else. In particular it does not modify hooks/ themselves.
#
# USAGE
#   ./scripts/enable-hooks.sh            install
#   ./scripts/enable-hooks.sh --check    report what is installed, change nothing
#   ./scripts/enable-hooks.sh --remove   undo both
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

check_only=0
remove=0
case "${1:-}" in
    --check)  check_only=1 ;;
    --remove) remove=1 ;;
    -h|--help) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    "") ;;
    *) echo "unknown argument: $1 (try --help)" >&2; exit 2 ;;
esac

report() {
    local label=$1 state=$2
    printf '  %-22s %s\n' "$label" "$state"
}

echo "metricGate hooks"
echo

# ── git hooks ────────────────────────────────────────────────────────────
current=$(git config --get core.hooksPath || true)
if [ "$remove" -eq 1 ]; then
    git config --unset core.hooksPath 2>/dev/null || true
    report "core.hooksPath" "unset"
elif [ "$check_only" -eq 1 ]; then
    if [ "$current" = "hooks" ]; then
        report "core.hooksPath" "hooks (installed)"
    else
        report "core.hooksPath" "${current:-<unset>} (NOT installed)"
    fi
else
    git config core.hooksPath hooks
    report "core.hooksPath" "hooks (installed)"
fi

# ── post-edit hook (agent harness) ───────────────────────────────────────
settings=".claude/settings.json"
example=".claude/settings.json.example"

if [ "$remove" -eq 1 ]; then
    rm -f "$settings"
    report ".claude/settings.json" "removed"
elif [ "$check_only" -eq 1 ]; then
    if [ -f "$settings" ]; then
        if grep -q "$root/hooks/post-edit.sh" "$settings"; then
            report ".claude/settings.json" "installed"
        else
            report ".claude/settings.json" "present but points elsewhere"
        fi
    else
        report ".claude/settings.json" "absent (NOT installed)"
    fi
else
    if [ ! -f "$example" ]; then
        echo "  $example is missing; cannot generate $settings" >&2
        exit 1
    fi
    # The example carries a "<ABS_PATH>" placeholder because the harness runs
    # the hook from an unspecified directory. Substituting it is the whole job.
    sed "s|<ABS_PATH>|$root|g" "$example" > "$settings"
    report ".claude/settings.json" "installed (path: $root)"
fi

# ── verify the hooks actually work ───────────────────────────────────────
if [ "$remove" -eq 0 ]; then
    echo
    echo "self-test"
    if [ -x hooks/post-edit_test.sh ]; then
        if ./hooks/post-edit_test.sh >/dev/null 2>&1; then
            report "hooks/post-edit_test.sh" "pass"
        else
            report "hooks/post-edit_test.sh" "FAILED (run it directly to see why)"
        fi
    else
        report "hooks/post-edit_test.sh" "not present"
    fi

    for h in pre-commit pre-push; do
        if [ -x "hooks/$h" ]; then
            report "hooks/$h" "executable"
        else
            report "hooks/$h" "MISSING or not executable"
        fi
    done
fi

echo
if [ "$check_only" -eq 1 ]; then
    echo "check only; nothing changed. Run without --check to install."
elif [ "$remove" -eq 1 ]; then
    echo "removed. Hooks no longer run."
else
    echo "installed. The three touchpoints now fire:"
    echo "  post-edit  ~1.3s, the file just edited    (agent harness)"
    echo "  pre-commit ~1-3s, the diff                (git commit)"
    echo "  pre-push   ~1-3s, the diff vs origin/main (git push)"
fi
