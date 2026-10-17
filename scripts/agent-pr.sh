#!/usr/bin/env bash
# agent-pr.sh — gate, commit, push. The PR opens itself.
#
# WHY THE GATE RUNS HERE TOO
#   hooks/pre-push already gates a push, so this call is redundant on a machine
#   with hooks installed. It is here for the machines where they are not: a
#   fresh clone has no core.hooksPath set, and a hook that was never installed
#   cannot save you. Running the verdict explicitly makes the script correct on
#   its own rather than correct only by inheritance.
#
# WHY IT REFUSES TO PUSH A RED TREE
#   Pushing a known-red branch moves the cost of the verdict to CI, where it is
#   slower, noisier, and blocks someone else's merge queue. A local stop costs
#   one command. `--force-push` exists for the case where CI's environment
#   genuinely differs, and it prints why that is usually wrong.
#
# USAGE
#   ./scripts/agent-pr.sh <issue-number> <commit subject>
#   ./scripts/agent-pr.sh <issue-number> <commit subject> --force-push
set -euo pipefail

if [ $# -lt 2 ]; then
    sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
    echo
    echo "usage: $0 <issue-number> <commit subject> [--force-push]"
    exit 2
fi

n=$1
subject=$2
force=0
[ "${3:-}" = "--force-push" ] && force=1

branch=$(git rev-parse --abbrev-ref HEAD)
case "$branch" in
    main)
        echo "refusing to commit to main: work on a branch (./scripts/agent-work.sh $n <slug>)" >&2
        exit 1
        ;;
    fix/issue-*|feat/issue-*) ;;
    *)
        echo "warning: branch '$branch' does not match fix/issue-* or feat/issue-*;" >&2
        echo "         autopr.yml will still open a PR, but it cannot link issue #$n." >&2
        ;;
esac

if [ -z "$(git status --porcelain)" ]; then
    echo "nothing to commit; pushing the branch as it stands"
else
    git add -A
    # The issue number goes in the trailer so the PR body and the commit agree
    # on which work item this belongs to.
    git commit -m "$subject" -m "Refs #$n"
fi

echo
echo "── gate ──────────────────────────────────────────────────────────"
set +e
./metricgate check . -diff -base origin/main
gate=$?
set -e
echo "──────────────────────────────────────────────────────────────────"
echo

if [ "$gate" -ne 0 ]; then
    if [ "$force" -eq 0 ]; then
        cat >&2 <<'EOF'
gate is red; not pushing.

Fix the findings above, or if the verdict is environment-specific, say so
explicitly:

    ./scripts/agent-pr.sh <issue> "<subject>" --force-push

CI runs the same command in the same shape, so a red result here is almost
always red there too — pushing it just moves the failure somewhere slower.
EOF
        exit "$gate"
    fi
    echo "gate is red; pushing anyway because --force-push was given." >&2
fi

git push -u origin "$branch"

cat <<EOF

pushed $branch

.github/workflows/autopr.yml opens the PR within a minute. Watch it with:

    gh pr list --repo \${METRICGATE_REPO:-OliverYang/metricGate} --head $branch

or, without gh, in the browser:
    https://github.com/\${METRICGATE_REPO:-OliverYang/metricGate}/compare/main...$branch?expand=1
EOF
