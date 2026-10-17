#!/usr/bin/env bash
# cockpit-run.sh — build the cockpit and put it on a port, in one command.
#
#   scripts/cockpit-run.sh              build, then serve on 127.0.0.1:8787
#   scripts/cockpit-run.sh 8899         build, then serve on that port
#   scripts/cockpit-run.sh 8899 -db /tmp/x.db
#
# Why a script at all, when `go build` + `./metricgate cockpit serve` is two
# commands: the second one is where the mistakes are. Every failure below has
# already happened in this repo at least once.
#
#   * Stale binary. `internal/cockpit/dashboard.go` embeds web/{index.html,
#     app.js,style.css,spec.json} with go:embed, so the assets are baked in at
#     compile time. Edit style.css, forget to rebuild, restart, and the browser
#     serves the old CSS from a process that looks perfectly healthy. The build
#     is therefore unconditional, and the server prints its own build stamp on
#     startup (with a `+dirty` suffix when the tree is modified) so there is
#     always one line naming what is actually running.
#   * Port already held. A leftover server answers 200 on the old build and the
#     new one dies with "address already in use" in a log nobody reads. This
#     script refuses to start, and names the PID to kill.
#   * Missing -db parent. The database is created lazily, so a typo'd path
#     yields a server that starts, serves, and shows every view empty - with no
#     error anywhere.
#
# The build goes to ./metricgate, which .gitignore:1 already covers, so a build
# never shows up as an untracked file.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PORT="8787"
if [[ $# -gt 0 && "$1" != -* ]]; then
  PORT="$1"
  shift
fi
ADDR="127.0.0.1:$PORT"
BIN="$ROOT/metricgate"
DB=".metricgate/history.db"
LOG=".metricgate/mcp-calls.jsonl"

# -db / -log are read here as well as passed through, only so the parent
# directories can be created. A lazy-open database does not fail on a missing
# directory; it silently records nothing.
args=("$@")
for ((i = 0; i < ${#args[@]}; i++)); do
  case "${args[i]}" in
    -addr)
      # The address is this script's job (port from argv[1], loopback only), and
      # the port-collision check above already ran against it. A second -addr
      # would win inside the binary while the printed URL kept describing the
      # first one.
      echo "error: pass the port as the first argument, not -addr" >&2
      echo "       scripts/cockpit-run.sh ${PORT} ..." >&2
      exit 2
      ;;
    -db) DB="${args[i + 1]:-}" ;;
    -log) LOG="${args[i + 1]:-}" ;;
  esac
done
for dir in "$DB" "$LOG"; do
  [[ -n "$dir" ]] || continue
  parent="$(dirname "$dir")"
  [[ -d "$parent" ]] || mkdir -p "$parent"
done

# A lazily-created database means an empty -db is a supported state (history
# off), so say that rather than printing a blank where a path should be.
DB_NOTE="(history disabled)"
if [[ -n "$DB" ]]; then
  if [[ -f "$DB" ]]; then DB_NOTE="($(du -h "$DB" | cut -f1))"; else DB_NOTE="(new, empty)"; fi
fi
LOG_NOTE="(call log disabled)"
if [[ -n "$LOG" ]]; then LOG_NOTE="$LOG"; fi

echo "==> build"
go build -o "$BIN" ./cmd/metricgate
# The revision is not printed here: the binary already stamps itself from
# vcs.revision + vcs.modified (cmd/metricgate/main.go:110 buildStamp) and says
# so on its first line, dirty suffix included. Two sources for one fact is how
# they end up disagreeing.
echo "    $BIN"

# A server already on the port is the one thing that produces a confusing
# outcome rather than a failure: it answers, so nothing looks wrong, and it is
# running whatever was built last time.
if holder="$(lsof -nP -iTCP:"$PORT" -sTCP:LISTEN -t 2>/dev/null | head -1)" && [[ -n "$holder" ]]; then
  echo "error: $ADDR is already in use by pid $holder" >&2
  echo "       $(ps -o command= -p "$holder" 2>/dev/null || echo 'unknown process')" >&2
  echo "       kill it first:  kill $holder" >&2
  exit 1
fi

echo "==> serve"
echo "    url   http://$ADDR/"
echo "    db    ${DB:-—} $DB_NOTE"
echo "    log   $LOG_NOTE"
echo

# exec, so Ctrl-C reaches the server directly and no wrapper shell is left
# holding the port after the terminal closes.
exec "$BIN" cockpit serve -addr "$ADDR" "$@"
