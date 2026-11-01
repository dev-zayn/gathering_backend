#!/bin/sh
# Serves the laptop's games and points every game in the local registry at
# them: restarts the fixtures on :8091 with PUBLIC_URL at that address, then
# moves every game there. The api must be up on :8080 with DEBUG=true and
# DEV_ADMIN=true.
#
# localhost by default, so nothing breaks when the Mac changes network: an iOS
# simulator reaches the Mac's localhost as is, and an Android emulator through
# `adb reverse` (done here for every device adb sees). A phone on the Wi-Fi
# needs the Mac's address instead, and the move again after every network
# change.
#
#   scripts/games-local.sh              # http://localhost:8091
#   scripts/games-local.sh wifi         # the address of en0
#   scripts/games-local.sh 10.0.0.5     # or this one
set -eu
cd "$(dirname "$0")/.."

host="${1:-localhost}"
if [ "$host" = wifi ]; then
  host="$(ipconfig getifaddr en0 || true)"
  [ -n "$host" ] || { echo "games-local: no address on en0; name one"; exit 1; }
fi
port="${PORT:-8091}"
api="${API_URL:-http://localhost:8080}"
public="http://$host:$port"
games="$(cd ../gathering_games && pwd)"
log="${TMPDIR:-/tmp}/gathering-fixtures.log"

# The process listening on the port, nothing else: the api keeps connections
# to the fixtures open, and a plain `lsof -ti tcp:8091` names it too. Its
# `go run` exits with it.
pids="$(lsof -ti "tcp:$port" -sTCP:LISTEN || true)"
# shellcheck disable=SC2086 # one word per pid
[ -z "$pids" ] || kill $pids
while lsof -ti "tcp:$port" -sTCP:LISTEN >/dev/null; do sleep 1; done

# The shell that waits on `go run` writes to the log too, or it holds this
# script's output open for as long as the server runs.
(cd backend && FIXTURES_DIR="$games" PORT="$port" PUBLIC_URL="$public" API_URL="$api" \
  nohup go run ./cmd/fixtures &) >"$log" 2>&1
i=0
until curl -fs -o /dev/null "$public/xo/"; do
  i=$((i + 1))
  [ "$i" -lt 60 ] || { echo "games-local: the fixtures did not start; see $log"; exit 1; }
  sleep 1
done
echo "fixtures: $public (log $log)"

# An emulator's localhost is its own: send the api's and the games' ports to
# the Mac's. Lasts until the emulator restarts; run this again after.
if [ "$host" = localhost ] && command -v adb >/dev/null; then
  for serial in $(adb devices | awk 'NR > 1 && $2 == "device" { print $1 }'); do
    adb -s "$serial" reverse "tcp:${api##*:}" "tcp:${api##*:}" >/dev/null &&
      adb -s "$serial" reverse "tcp:$port" "tcp:$port" >/dev/null &&
      echo "adb reverse: $serial (:${api##*:}, :$port)"
  done
fi

jar="$(mktemp)"
trap 'rm -f "$jar"' EXIT
curl -fs -o /dev/null -c "$jar" -H 'Origin: http://localhost:8085' -H 'Content-Type: application/json' \
  -X POST "$api/v1/admin/session/dev" -d '{"name":"Dev"}' ||
  { echo "games-local: no console session from $api (DEBUG and DEV_ADMIN on?)"; exit 1; }

# Every folder with a manifest is a game; its id is the manifest's.
for dir in "$games"/*/; do
  d="$(basename "$dir")"
  id="$(curl -fs "$public/$d/.well-known/gathering-game.json" | sed -n 's/^ *"id": *"\([^"]*\)".*/\1/p' | head -n 1)" || true
  [ -n "$id" ] || continue
  code="$(curl -s -o /dev/null -w '%{http_code}' -b "$jar" -H 'Origin: http://localhost:8085' \
    -H 'Content-Type: application/json' -X PUT "$api/v1/admin/games/$id/manifest" -d "{\"url\":\"$public/$d/\"}")"
  case $code in
    200) echo "moved: $id → $public/$d/" ;;
    404) echo "not registered: $id (Games, Register by URL: $public/$d/)" ;;
    *) echo "failed: $id ($code)" ;;
  esac
done
