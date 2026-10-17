#!/bin/sh
# post-edit_test.sh — pins the D26-1..D26-5 acceptance criteria from
# docs/iterations/iter-26-post-edit-hook.md §8.
#
# Runs entirely inside a throwaway repository under $TMPDIR with the store
# disabled, so it never touches this repository's .metricgate/history.db (that
# would defeat the point of choosing the narrow scope in the first place).
#
# Usage: hooks/post-edit_test.sh   (exit 0 = all criteria hold)
set -u

hook_dir=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$hook_dir/.." && pwd)
hook=$hook_dir/post-edit.sh

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

pass=0
fail=0

check() {
    label=$1
    if [ "$2" = "1" ]; then
        printf 'PASS  %s\n' "$label"
        pass=$((pass + 1))
    else
        printf 'FAIL  %s\n' "$label"
        fail=$((fail + 1))
    fi
}

# A synthetic repository: the store off (so no run rows accumulate anywhere),
# one clean file, one file that trips an error-level rule. The 120-statement
# body is what makes function-length fire at the error threshold of 100.
#
# It must be a real git repository with at least one commit: the whole-tree
# fallback (D26-3/D26-4) runs `check . -diff`, and -diff needs a base to diff
# against. Without a commit that path errors out, which would test the error
# branch instead of the fallback. big.go is deliberately left untracked --
# -diff counts untracked files, so no staging is needed for it to be reported.
mkdir -p "$work/repo"
cat >"$work/repo/.metricgate.yaml" <<'YAML'
store:
  enabled: false
YAML
cat >"$work/repo/clean.go" <<'GO'
package probe

func Small() int { return 1 }
GO
{
    printf 'package probe\n\nfunc Big() {\n'
    i=0
    while [ $i -lt 120 ]; do printf '\t_ = %d\n' "$i"; i=$((i + 1)); done
    printf '}\n'
} >"$work/repo/big.go"

(cd "$work/repo" && git init -q && git config user.email hook@test && git config user.name hook && \
    git add .metricgate.yaml clean.go && git commit -qm 'fixture') || {
    echo "post-edit_test: could not create the fixture repository" >&2
    exit 1
}

# The hook resolves the binary from PATH, so put the dogfooded one there for
# every criterion except D26-4, which tests the fallback chain itself.
run_hook() {
    (cd "$work/repo" && PATH="$repo:$PATH" "$hook")
}

# --- D26-1: a clean file passes, and says what it checked ---------------------
out=$(printf '{"tool_name":"Edit","tool_input":{"file_path":"%s"}}' "$work/repo/clean.go" |
    run_hook 2>"$work/d1.err")
code=$?
check "D26-1 clean file exits 0" "$([ "$code" -eq 0 ] && echo 1 || echo 0)"

# --- D26-5: the scope line is the first line of stderr, in both scopes --------
check "D26-5 narrow scope line first" \
    "$(head -n 1 "$work/d1.err" | grep -q "checked $work/repo/clean.go only" && echo 1 || echo 0)"

# --- D26-2: a violation exits 2 and carries the finding text ------------------
printf '{"tool_name":"Edit","tool_input":{"file_path":"%s"}}' "$work/repo/big.go" |
    run_hook 2>"$work/d2.err"
code=$?
check "D26-2 violation exits 2" "$([ "$code" -eq 2 ] && echo 1 || echo 0)"
check "D26-2 stderr quotes the finding" \
    "$(grep -q 'function-length of "Big"' "$work/d2.err" && echo 1 || echo 0)"
check "D26-5 scope line first on violation" \
    "$(head -n 1 "$work/d2.err" | grep -q 'checked .*big.go only' && echo 1 || echo 0)"

# --- D26-3: no file path falls back to the whole tree, never silently ---------
(cd "$work/repo" && PATH="$repo:$PATH" "$hook" </dev/null 2>"$work/d3.err")
code=$?
check "D26-3 no stdin still produces a verdict" \
    "$([ "$code" -eq 2 ] && grep -q 'function-length of "Big"' "$work/d3.err" && echo 1 || echo 0)"
check "D26-3 whole-tree scope line" \
    "$(head -n 1 "$work/d3.err" | grep -q 'checked whole tree' && echo 1 || echo 0)"

# --- D26-4: no metricgate on PATH, but a repo-root binary present -------------
# $PWD/metricgate is the fall-forward target, so the repo needs one there.
ln -sf "$repo/metricgate" "$work/repo/metricgate"
(cd "$work/repo" && env -i PATH=/usr/bin:/bin HOME="$HOME" "$hook" </dev/null 2>"$work/d4.err")
code=$?
check "D26-4 falls forward to \$PWD/metricgate" \
    "$(grep -q 'checked whole tree' "$work/d4.err" && echo 1 || echo 0)"
check "D26-4 did not skip the gate" \
    "$(grep -q 'function-length of "Big"' "$work/d4.err" && echo 1 || echo 0)"
rm -f "$work/repo/metricgate"

# --- documented degradations: loud, and exit 0 -------------------------------
(cd "$work/repo" && env -i PATH=/usr/bin:/bin HOME="$HOME" "$hook" </dev/null 2>"$work/d5.err")
code=$?
check "missing binary degrades loudly, not silently" \
    "$([ "$code" -eq 0 ] && grep -q 'not found; skipping the gate' "$work/d5.err" && echo 1 || echo 0)"

printf '{"tool_input":{"file_path":"%s"}}' "$work/repo/deleted.go" | run_hook 2>"$work/d6.err"
code=$?
check "deleted file reports nothing to check, exit 0" \
    "$([ "$code" -eq 0 ] && grep -q 'no longer exists' "$work/d6.err" && echo 1 || echo 0)"

# --- a passing edit still states its scope (the R4 mitigation) ---------------
check "clean run still prints the scope line" \
    "$(grep -q 'cross-file rules' "$work/d1.err" && echo 1 || echo 0)"

printf '\n%s passed, %s failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
