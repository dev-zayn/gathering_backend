#!/bin/sh
# On the server, run by deploy.sh after the stack is up: every folder of
# /opt/ga3da/gathering_games with a manifest is registered in the registry at
# https://games.<domain>/<folder>/ (or moved there), given its art/icon.png
# when it has none, and published. A game the registry will not publish yet
# (no icon, no tagline) is named with what it lacks; the console finishes it.
set -eu
root=/opt/ga3da
. "$root/secrets/ga3da.env"
console="https://console.$DOMAIN"
games="https://games.$DOMAIN"
# Straight to this machine's Caddy, whatever the DNS says from here.
here="--resolve console.$DOMAIN:443:127.0.0.1 --resolve games.$DOMAIN:443:127.0.0.1 --resolve api.$DOMAIN:443:127.0.0.1"

i=0
# shellcheck disable=SC2086 # $here is several flags
until curl -fs -o /dev/null $here "https://api.$DOMAIN/health"; do
  i=$((i + 1))
  [ "$i" -lt 90 ] || { echo "games: https://api.$DOMAIN/health never answered (DNS pointing here? docker compose logs caddy)"; exit 1; }
  sleep 2
done

jar="$(mktemp)"
trap 'rm -f "$jar"' EXIT
# admin calls: the console's host, its origin, the session cookie
admin() {
  # shellcheck disable=SC2086
  curl -s $here -b "$jar" -c "$jar" -H "Origin: $console" "$@"
}
# Signed in as "Deploy" with the console's password: the activity log says so.
code="$(admin -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -X POST "$console/v1/admin/session/password" \
  -d "{\"name\":\"Deploy\",\"password\":\"$(cat "$root/secrets/console.password")\"}")"
[ "$code" = 200 ] || { echo "games: the console refused the deploy's sign-in ($code)"; exit 1; }

for dir in "$root"/gathering_games/*/; do
  d="$(basename "$dir")"
  # shellcheck disable=SC2086
  id="$(curl -fs $here "$games/$d/.well-known/gathering-game.json" | sed -n 's/^ *"id": *"\([^"]*\)".*/\1/p' | head -n 1)" || true
  [ -n "$id" ] || continue

  code="$(admin -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
    -X POST "$console/v1/admin/games" -d "{\"url\":\"$games/$d/\"}")"
  case $code in
    2??) state="registered" ;;
    409)
      admin -o /dev/null -H 'Content-Type: application/json' -X PUT "$console/v1/admin/games/$id/manifest" -d "{\"url\":\"$games/$d/\"}"
      state="there" ;;
    *) echo "games: $id not registered ($code)"; continue ;;
  esac

  if [ -f "$dir/art/icon.png" ] && admin "$console/v1/admin/games/$id" | grep -q '"icon":""'; then
    admin -o /dev/null -X PUT -F "file=@$dir/art/icon.png;type=image/png" "$console/v1/admin/games/$id/icon"
  fi

  out="$(admin -w '\n%{http_code}' -X POST "$console/v1/admin/games/$id/publish")"
  case "$(printf '%s' "$out" | tail -n 1)" in
    2??) echo "games: $id $state, published" ;;
    *) echo "games: $id $state, not published: $(printf '%s' "$out" | head -n 1)" ;;
  esac
done
