#!/bin/sh
# Moves the laptop's games to the Mac's Wi-Fi address, for after the Mac joins
# another network: restarts the fixtures on :8093 with PUBLIC_URL at that
# address, then points every game in the registry there. The api must be up on
# :8080 with DEBUG=true and DEV_ADMIN=true.
#
#   scripts/games-on-wifi.sh              # the address of en0
#   scripts/games-on-wifi.sh 10.0.0.5     # or this one
set -eu
cd "$(dirname "$0")/.."

ip="${1:-$(ipconfig getifaddr en0 || true)}"
[ -n "$ip" ] || { echo "games-on-wifi: no address on en0; name one"; exit 1; }
port="${PORT:-8093}"
api="${API_URL:-http://localhost:8080}"
public="http://$ip:$port"
games="$(cd ../gathering_games && pwd)"
log="${TMPDIR:-/tmp}/gathering-fixtures.log"

# The process listening on the port, nothing else: the api keeps connections
# to the fixtures open, and a plain `lsof -ti tcp:8093` names it too. Its
# `go run` exits with it.
pids="$(lsof -ti "tcp:$port" -sTCP:LISTEN || true)"
# shellcheck disable=SC2086 # one word per pid
[ -z "$pids" ] || kill $pids
while lsof -ti "tcp:$port" -sTCP:LISTEN >/dev/null; do sleep 1; done

(cd backend && FIXTURES_DIR="$games" PORT="$port" PUBLIC_URL="$public" API_URL="$api" \
  nohup go run ./cmd/fixtures >"$log" 2>&1 &)
i=0
until curl -fs -o /dev/null "$public/xo/"; do
  i=$((i + 1))
  [ "$i" -lt 60 ] || { echo "games-on-wifi: the fixtures did not start; see $log"; exit 1; }
  sleep 1
done
echo "fixtures: $public (log $log)"

jar="$(mktemp)"
trap 'rm -f "$jar"' EXIT
curl -fs -o /dev/null -c "$jar" -H 'Origin: http://localhost:8085' -H 'Content-Type: application/json' \
  -X POST "$api/v1/admin/session/dev" -d '{"name":"Dev"}' ||
  { echo "games-on-wifi: no console session from $api (DEBUG and DEV_ADMIN on?)"; exit 1; }

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
