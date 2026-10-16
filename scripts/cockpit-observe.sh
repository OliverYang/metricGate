#!/usr/bin/env bash
# cockpit-observe.sh — the single entry point for watching the cockpit UI.
#
# Every SonarQube-parity iteration runs the same checks, and every check is
# either a machine verdict or a PNG. This script is what a parallel goal calls
# instead of re-deriving the commands, so "done" means the same thing in every
# worktree.
#
#   scripts/cockpit-observe.sh quick  [root]   cheap tier: node render + web contract tests only
#   scripts/cockpit-observe.sh fast   [root]   node render + smoke probe + go build/test + gate
#   scripts/cockpit-observe.sh shots  [root] [name,...]  queue headless-Chrome PNGs (serial, detached)
#   scripts/cockpit-observe.sh serve  [root] [port]   live cockpit on real data + dev harness
#   scripts/cockpit-observe.sh status                 what is running and what the last verdict was
#
# Screenshots are the expensive observation point: each headless Chrome start
# costs 13s+ and the browser fights whatever else is on the machine. `shots`
# therefore takes a lock and runs one Chrome at a time, so three parallel goals
# queue instead of deadlocking each other at 0% CPU.
#
# Two verification tiers, because `fast` is too expensive to run per edit:
# `quick` is the loop tier (no whole-repo build, no gate walk, only the web
# contract tests) and `fast` is the wrap-up tier, once per task.
set -euo pipefail

CMD="${1:-fast}"
ROOT="${2:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
WEB="$ROOT/internal/cockpit/web"
SHOTS_HOME="${MG_SHOTS_HOME:-/tmp/shots}"
PORT="${3:-8899}"
LOCK="$SHOTS_HOME/.shoot.lock"
# The web contract tests are the ones an app.js/style.css/index.html edit can
# break, and they are the only ones `quick` pays for. Override with MG_QUICK_TESTS.
QUICK_TESTS="${MG_QUICK_TESTS:-TestAppJS|TestEvery|TestTemplate|TestMapped|TestFragments|TestEvents|TestRenderAll|TestCSS|TestViewTable|TestSidebar|TestAppShell}"

need_web() {
  [ -f "$WEB/app.js" ] || { echo "no $WEB/app.js — is $ROOT a worktree?" >&2; exit 2; }
}

quick() {
  need_web
  echo "== node --check =="
  node --check "$WEB/app.js"
  echo "== render_all (all views x all scopes, offline) =="
  (cd "$WEB" && node render_all.mjs)
  echo "== targeted go test (web contract only) =="
  local out
  out="$(cd "$ROOT" && go test ./internal/cockpit/ -run "$QUICK_TESTS" -count=1 2>&1)"
  echo "$out" | tail -5
  # A -run regex that matches nothing reports "ok ... [no tests to run]" and
  # exits 0, so a typo would look like a pass. It is not one.
  case "$out" in
    *"no tests to run"*) echo "quick: $QUICK_TESTS matched no test — not a pass" >&2; exit 1 ;;
  esac
}

fast() {
  need_web
  echo "== node --check =="
  node --check "$WEB/app.js"
  echo "== render_all (all views x all scopes, offline) =="
  (cd "$WEB" && node render_all.mjs)
  echo "== live smoke (real payloads through a DOM stub) =="
  # smoke.mjs reads its payloads from a file the Go test writes; run it directly
  # only in probe mode (print the requests the frontend would make) and let the
  # go test below do the assertive render against real handler payloads.
  (cd "$WEB" && node smoke.mjs --probe | tail -3)
  echo "== go build =="
  (cd "$ROOT" && go build ./...)
  echo "== go test ./internal/cockpit/... =="
  (cd "$ROOT" && go test ./internal/cockpit/...)
  echo "== gate (metricGate findings on the web tree) =="
  local bin="$ROOT/metricgate"
  [ -x "$bin" ] || (cd "$ROOT" && go build -o metricgate ./cmd/metricgate)
  (cd "$ROOT" && ./metricgate check internal/cockpit/web | tail -5)
}

serve() {
  echo "== live cockpit on real data: http://127.0.0.1:$PORT =="
  (cd "$ROOT" && nohup go run ./cmd/metricgate cockpit serve -addr "127.0.0.1:$PORT" \
    >"$SHOTS_HOME/cockpit-serve.log" 2>&1 &)
  # The embedded assets are baked at build time: a running server never sees an
  # edit to web/style.css. The shot harness serves the files over HTTP instead,
  # so a re-shoot after an edit needs no rebuild.
  mkdir -p "$SHOTS_HOME"
  cp "$WEB/app.js" "$WEB/style.css" "$WEB/spec.json" "$SHOTS_HOME/"
  cp "$WEB/index.html" "$SHOTS_HOME/base.html"
  pgrep -f "serve.py" >/dev/null || (cd "$SHOTS_HOME" && nohup python3 serve.py >"$SHOTS_HOME/serve.log" 2>&1 &)
  sleep 2
  python3 - "$PORT" <<'PY'
import sys, time, urllib.request
port = sys.argv[1]
for _ in range(30):
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{port}/api/dashboard", timeout=2) as r:
            print("cockpit", r.status, "on", port); break
    except Exception:
        time.sleep(1)
else:
    sys.exit("cockpit did not answer on " + port)
PY
}

shots() {
  need_web
  # ${1:-} are the shot names. The script's $2 is the worktree root, which this
  # function does not need (it is resolved once at the top of the script), so
  # it reads $1 here. Reading $3 earlier was a double bug: `shots` is dispatched
  # with no arguments of its own, so $3 was always empty and every call silently
  # ran all 16 jobs — a 3-shot request became a 16-shot one.
  # The 9333 port is the CDP endpoint the detached batch picks up.
  local names="${1:-}"
  local port="${2:-9333}"
  mkdir -p "$SHOTS_HOME"
  cp "$WEB/app.js" "$WEB/style.css" "$WEB/spec.json" "$SHOTS_HOME/"
  cp "$WEB/index.html" "$SHOTS_HOME/base.html"
  python3 "$SHOTS_HOME/mk.py" >/dev/null
  local tag; tag="$(basename "$ROOT")-$(date +%s)"
  local log="$SHOTS_HOME/$tag.log"
  # Printed before the lock is waited for. This function used to open with a
  # silent `until mkdir` loop, so a caller watching it had no way to tell
  # "queued" from "hung on a lock whose holder aborted".
  echo "== shots -> $SHOTS_HOME/$tag (batch: ${names:-all 13}, serial) =="
  # mkdir is the only lock primitive that behaves the same on macOS and Linux.
  # It is taken with an owner written inside it, because the lock used to be
  # unowned: a run that was killed left the directory behind and every later
  # run then waited on it forever. That wait was unbounded (`until mkdir` with
  # no escape), and with no pid recorded there was nothing to inspect either,
  # so the second caller looked exactly like a working one.
  #
  # The release is `rm -f $LOCK/pid` and THEN `rmdir $LOCK`, in that order.
  # Removing the file first is what makes the directory removable at all: a
  # bare `rmdir` on a lock that holds its own pid fails, which is why the
  # first version of this function could neither release the lock nor reclaim
  # a stale one, and a caller queued behind debris waited out its timeout.
  release_lock() {
    rm -f "$LOCK/pid" 2>/dev/null || true
    rmdir "$LOCK" 2>/dev/null || true
  }
  local waited=0
  until mkdir "$LOCK" 2>/dev/null; do
    # A pid that is no longer alive makes the directory debris: reclaim it
    # rather than waiting out the timeout.
    local owner; owner="$(cat "$LOCK/pid" 2>/dev/null || true)"
    if [ -n "$owner" ] && ! kill -0 "$owner" 2>/dev/null; then
      echo "lock: pid $owner is gone — reclaiming" >&2
      release_lock
      continue
    fi
    # A lock with no pid inside is not a lock: the holder writes its pid the
    # moment it wins the mkdir, so an empty one is debris from a run that died
    # between the two. It is reclaimable once too old to be that in-flight
    # write.
    if [ -z "$owner" ]; then
      local age; age=$(( $(date +%s) - $(stat -f %m "$LOCK" 2>/dev/null || date +%s) ))
      if [ "$age" -ge 15 ]; then
        echo "lock: pid-less lock is ${age}s old — reclaiming" >&2
        release_lock
        continue
      fi
    fi
    if [ "${MG_SHOTS_FORCE:-0}" = "1" ]; then
      echo "lock: MG_SHOTS_FORCE=1 — taking it from ${owner:-?}" >&2
      release_lock
      continue
    fi
    if [ "$waited" -ge "${MG_SHOTS_WAIT:-180}" ]; then
      # A bounded wait that says what to do next, rather than an unbounded
      # one that says nothing. "hung on someone else's lock" and "nobody is
      # taking shots" are different problems and the caller has to be able to
      # tell them apart.
      echo "lock: still held by pid ${owner:-?} after ${waited}s (dir $LOCK)." >&2
      echo "      Reclaim with MG_SHOTS_FORCE=1, or inspect with: pgrep -fl shoot.py" >&2
      exit 3
    fi
    sleep 3
    waited=$((waited + 3))
  done
  # The lock belongs to the batch, not to this shell. The batch is detached so
  # that an aborted call cannot take a half-finished browser down with it, and
  # so the caller is never blocked for the length of a batch — the browser
  # timing out used to hold the caller's own command until its timeout, which
  # is the part that looked like a hang.
  #
  # `python3 -u` because stdout to a file is block-buffered: without it the
  # per-shot lines reach the log only at exit, so tailing the log looked like
  # a dead batch while it worked.
  nohup bash -c '
    lock="$1"; dir="$2"; names="$3"; log="$4"
    echo $$ > "$lock/pid"
    # The CDP runner owns one browser for the whole batch and closes its
    # per-shot targets, so a page that never renders costs one shot instead of
    # every shot after it. `shoot.py` (one chrome --screenshot process per
    # shot, 120s timeout each) is what made this function look hung: a single
    # under-provisioned start burned the batch and left an empty PNG, and its
    # retry path raised before the log line was written, so the only evidence
    # was a traceback.
    # No `-u` for node: the flag python takes (unbuffered stdout) is rejected
    # by node as a bad option, and the batch died with `node: bad option: -u`
    # before a single shot ran. The runner already writes its own log lines,
    # which is what makes the batch observable.
    if [ -n "$names" ]; then
      node /tmp/shots/cdp_shoot.mjs "$dir" "$names" "$port"
    else
      node /tmp/shots/cdp_shoot.mjs "$dir" "" "$port"
    fi
    status=$?
    rm -f "$lock/pid" 2>/dev/null
    rmdir "$lock" 2>/dev/null || true
    exit $status
    # The runner appends its own timestamped lines to "$log", so the shell
    # redirect lands in a separate file: a line written twice into the log
    # `status` prints reads as two batches that ran.
  ' _ "$LOCK" "$SHOTS_HOME/$tag" "$names" "$log" >"$log.raw" 2>&1 &
  echo "batch pid $! (owns the lock and survives an aborted caller)
log   $log"
  echo "verdict: tail -5 $log"
  echo "status:  bash scripts/cockpit-observe.sh status"
}

status() {
  echo "== processes =="
  pgrep -fl "cockpit serve|serve.py|shoot.py|http.server 8811" || echo "  (none)"
  echo "== last shot batches =="
  ls -1d "$SHOTS_HOME"/v[0-9]* 2>/dev/null | tail -5 || echo "  (none)"
  for log in "$SHOTS_HOME"/*.log; do
    [ -s "$log" ] || continue
    echo "-- $(basename "$log")"; tail -3 "$log"
  done
  echo "== load =="
  uptime
}

case "$CMD" in
  quick) quick ;;
  fast) fast ;;
  serve) serve ;;
  shots) shots "${3:-}" ;;
  status) status ;;
  *) echo "usage: cockpit-observe.sh {quick|fast|serve|shots|status} [root] [arg]" >&2; exit 2 ;;
esac
