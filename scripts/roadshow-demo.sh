#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/dist/otoha"
if [[ ! -x "$BIN" ]]; then
  echo "Building Otoha CLI…" >&2
  npm --prefix "$ROOT" run build:cli
fi

DEMO_HOME="$(mktemp -d "${TMPDIR:-/tmp}/otoha-roadshow.XXXXXX")"
TEAM_PID=""
cleanup() {
  if [[ -n "$TEAM_PID" ]]; then kill "$TEAM_PID" 2>/dev/null || true; fi
  rm -rf "$DEMO_HOME"
}
trap cleanup EXIT INT TERM

export OTOHA_HOME="$DEMO_HOME/home"
export OTOHA_REPO_ROOT="$ROOT"
mkdir -p "$OTOHA_HOME"

echo "Otoha roadshow fixture: $DEMO_HOME"
"$BIN" team init roadshow --min-free-bytes 0
"$BIN" team status roadshow
"$BIN" team doctor roadshow

TEAM_PID="$(python3 - "$OTOHA_HOME/teams/roadshow/team.json" <<'PY'
import json, sys
try:
    print(json.load(open(sys.argv[1]))['server_pid'])
except (FileNotFoundError, KeyError, TypeError):
    print('')
PY
)"

echo
echo "Starting the compact terminal story…"
"$BIN" demo --slow
echo
echo "Open showcase/index.html for the browser version of the same story."
