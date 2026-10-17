#!/usr/bin/env bash
# agent-work.sh — turn an issue number into a branch and a brief.
#
# WHAT THIS DOES NOT DO
#   It does not write the fix. The work itself is done by whatever agent or
#   person is at the keyboard, and pretending otherwise would mean guessing at
#   intent. This script removes the friction around the work: a branch named
#   after the issue, the issue's own text as a brief, and the exact command
#   that decides whether the work is done.
#
# WHY THE BRANCH NAME MATTERS
#   `fix/issue-<n>-<slug>` is what .github/workflows/autopr.yml parses to link
#   the PR back to the issue ("Closes #n"). Rename the branch and the link is
#   lost, so the pattern is not cosmetic.
#
# USAGE
#   ./scripts/agent-work.sh <issue-number> <short-slug>
#
#   ./scripts/agent-work.sh 42 complexity-in-registry-test
#     -> branch fix/issue-42-complexity-in-registry-test
set -euo pipefail

REPO=${METRICGATE_REPO:-OliverYang/metricGate}

if [ $# -lt 2 ]; then
    sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
    echo
    echo "usage: $0 <issue-number> <short-slug>"
    exit 2
fi

n=$1
slug=$2

if ! printf '%s' "$slug" | grep -qE '^[a-z0-9][a-z0-9-]*$'; then
    echo "slug must be lowercase letters, digits and dashes: $slug" >&2
    exit 2
fi

branch="fix/issue-${n}-${slug}"

# Never branch from a stale base: the diff gate compares against origin/main,
# so a base that is behind turns unrelated merges into "your" new findings.
echo "fetching origin..." >&2
git fetch origin --quiet

if git rev-parse -q --verify "refs/heads/$branch" >/dev/null; then
    echo "branch $branch already exists; switching to it" >&2
    git switch "$branch"
else
    git switch -c "$branch" origin/main
fi

echo
echo "branch: $branch"
echo

# The issue body is the brief. Without gh we still make the branch — the
# agent may already have the issue open in a browser, and refusing to branch
# because a credential is missing helps nobody.
if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    echo "──────────────────────────── issue #$n ────────────────────────────"
    gh issue view "$n" --repo "$REPO" --json number,title,body,labels \
        --template '{{.title}}

{{.body}}'
    echo "───────────────────────────────────────────────────────────────────"
else
    echo "issue #$n (body not fetched: gh is not authenticated)"
fi

cat <<EOF

The work is done when this is clean:

    ./metricgate check . -diff -base origin/main

That is the same command .github/workflows/gate.yml runs, so a local pass
predicts a green PR check.

Then:

    ./scripts/agent-pr.sh "$n" "<commit subject>"

It gates, commits, and pushes; autopr.yml opens the PR.
EOF
