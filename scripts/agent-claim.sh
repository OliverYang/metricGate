#!/usr/bin/env bash
# agent-claim.sh — find the work, and say clearly what you can and cannot do.
#
# THE AUTHENTICATION SPLIT
#   Pushing code needs an SSH key. Reading issues needs the GitHub API. They are
#   different credentials and this script only needs the second one, so it
#   degrades instead of failing: with `gh` authenticated it lists and claims;
#   without it, it prints the one-line manual path. A missing credential is a
#   configuration state, not an error, and an agent that treats it as an error
#   will loop on a problem it cannot solve.
#
# WHAT CLAIMING MEANS
#   Applying the `in-progress` label. That is the whole protocol: the label is
#   the lock. It is not atomic — two people can claim at once — but it is
#   visible, reversible with one command, and needs no infrastructure. For a
#   single-developer repository that is the right amount of machinery.
#
# USAGE
#   ./scripts/agent-claim.sh              list what is claimable
#   ./scripts/agent-claim.sh --claim N    claim issue N
#   ./scripts/agent-claim.sh --release N  give it back
set -euo pipefail

REPO=${METRICGATE_REPO:-OliverYang/metricGate}
LABEL_WORK=agent
LABEL_CLAIMED=in-progress

usage() {
    sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
    cat <<'EOF'

Options:
  --claim N     apply the in-progress label to issue N
  --release N   remove it again
  --all         list every open issue, not just claimable ones
  -h, --help    this text
EOF
}

have_gh_auth() {
    command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1
}

# The labels are part of the protocol, so they are created here rather than
# assumed. `gh label create --force` updates an existing label instead of
# failing, which makes this idempotent without parsing a 404: the ordinary case
# (label already there) costs one API call and prints nothing.
#
# Why this matters: `gh issue edit --add-label in-progress` on a repository that
# has never had that label fails, and it fails on the claim -- after the agent
# already decided what to work on.
ensure_label() {
    local name=$1 color=$2 desc=$3
    gh label create "$name" --repo "$REPO" --color "$color" \
        --description "$desc" --force >/dev/null
}

ensure_labels() {
    ensure_label "$LABEL_WORK"    1d76db 'Claimable by a local agent'
    ensure_label "$LABEL_CLAIMED" fbca04 'Claimed; do not start a second agent on it'
}

print_manual_path() {
    cat <<'EOF'
agent-claim: `gh` is not authenticated, so the issue list cannot be read here.

  Pushing code still works: that uses your SSH key, not this.

  Two ways forward:

  1. Authenticate once (a browser window, the token stays in ~/.config/gh):
       gh auth login

  2. Skip the API entirely — open the issue list in a browser, then:
       ./scripts/agent-work.sh <issue-number> <short-slug>

     Example:
       ./scripts/agent-work.sh 42 complexity-in-registry-test
EOF
}

cmd_list() {
    local scope=${1:-claimable}
    if ! have_gh_auth; then
        print_manual_path
        exit 0
    fi

    local -a args=(issue list --repo "$REPO" --state open --limit 50
                   --json number,title,labels,createdAt)
    if [ "$scope" = claimable ]; then
        # Claimable = filed by triage, not yet taken. Listing claimed issues as
        # available is how two agents end up on the same file.
        args+=(--label "$LABEL_WORK")
    fi

    local json
    json=$(gh "${args[@]}")

    python3 - "$json" "$LABEL_CLAIMED" <<'PY'
import json, sys

rows = json.loads(sys.argv[1])
claimed_label = sys.argv[2]

def labels(row):
    return {l["name"] for l in row.get("labels", [])}

claimable = [r for r in rows if claimed_label not in labels(r)]
claimed = [r for r in rows if claimed_label in labels(r)]

if not rows:
    print("No open issues carry the `agent` label.")
    print("Triage files them; run it with:  gh workflow run triage.yml")
    raise SystemExit(0)

if claimable:
    print(f"Claimable ({len(claimable)}):")
    for r in claimable:
        print(f"  #{r['number']:<5} {r['title']}")
else:
    print("Nothing claimable right now.")

if claimed:
    print(f"\nAlready claimed ({len(claimed)}):")
    for r in claimed:
        print(f"  #{r['number']:<5} {r['title']}")

print("\nClaim one with:  ./scripts/agent-claim.sh --claim <number>")
PY
}

cmd_claim() {
    local n=$1
    if ! have_gh_auth; then
        print_manual_path
        exit 0
    fi
    ensure_labels
    gh issue edit "$n" --repo "$REPO" --add-label "$LABEL_CLAIMED"
    echo "claimed #$n"
    echo
    echo "next:  ./scripts/agent-work.sh $n <short-slug>"
}

cmd_release() {
    local n=$1
    if ! have_gh_auth; then
        print_manual_path
        exit 0
    fi
    gh issue edit "$n" --repo "$REPO" --remove-label "$LABEL_CLAIMED"
    echo "released #$n"
}

case ${1:-} in
    -h|--help) usage ;;
    --claim)   [ $# -ge 2 ] || { echo "--claim needs an issue number" >&2; exit 2; }; cmd_claim "$2" ;;
    --release) [ $# -ge 2 ] || { echo "--release needs an issue number" >&2; exit 2; }; cmd_release "$2" ;;
    --all)     cmd_list all ;;
    "")        cmd_list claimable ;;
    *)         echo "unknown argument: $1 (try --help)" >&2; exit 2 ;;
esac
