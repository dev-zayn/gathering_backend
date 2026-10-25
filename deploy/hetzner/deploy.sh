#!/bin/sh
# From the Mac: builds the arcade games, sends this repo and ../gathering_games
# to the server's /opt/ga3da, sets the server up the first time (setup.sh),
# builds and starts the stack there, then registers and publishes the games
# (games.sh). Run it again for every change; it is the same command.
#
#   deploy/hetzner/deploy.sh root@<server-ip>
#   FIREBASE_KEY=/path/to/key.json deploy/hetzner/deploy.sh root@<server-ip>
#
# FIREBASE_KEY is the Firebase service account's JSON key (the file
# FCM_CREDENTIALS_FILE names in backend/.env); given once, it stays there.
set -eu
host="${1:?usage: deploy.sh root@<server-ip>}"
domain="${DOMAIN:-ga3da.fun}"
root=/opt/ga3da
if [ -n "${FIREBASE_KEY:-}" ] && [ ! -f "$FIREBASE_KEY" ]; then
  echo "deploy: FIREBASE_KEY=$FIREBASE_KEY is not a file (FCM_CREDENTIALS_FILE in backend/.env names it)"
  exit 1
fi
cd "$(dirname "$0")/../.."

make games

ssh "$host" "command -v rsync >/dev/null || (apt-get update -qq && apt-get install -y -qq rsync) && mkdir -p $root/gathering $root/gathering_games"
# The source, without what the images build themselves or what is secret.
rsync -az --delete \
  --exclude .git --exclude node_modules --exclude .env --exclude .files --exclude '*.log' \
  --exclude dist --exclude dist-app --exclude /backend/bin --exclude /.claude --exclude '*-firebase-adminsdk-*.json' \
  ./ "$host:$root/gathering/"
# The games, each arcade game with its dist/ (what the games server answers).
rsync -az --delete --exclude node_modules ../gathering_games/ "$host:$root/gathering_games/"

ssh "$host" "sh $root/gathering/deploy/hetzner/setup.sh $domain"

if [ -n "${FIREBASE_KEY:-}" ]; then
  scp -q "$FIREBASE_KEY" "$host:$root/secrets/firebase/firebase.json"
  ssh "$host" "chown 10001:10001 $root/secrets/firebase/firebase.json && chmod 600 $root/secrets/firebase/firebase.json &&
    sed -i 's|^FCM_CREDENTIALS_FILE=.*|FCM_CREDENTIALS_FILE=/run/firebase/firebase.json|' $root/secrets/ga3da.env"
fi

ssh "$host" "cd $root/gathering/deploy/hetzner &&
  docker compose --env-file $root/secrets/ga3da.env up -d --build --remove-orphans &&
  docker compose --env-file $root/secrets/ga3da.env exec -T caddy caddy reload --config /etc/caddy/ga3da/Caddyfile --adapter caddyfile &&
  docker image prune -f >/dev/null"
ssh "$host" "sh $root/gathering/deploy/hetzner/games.sh"

echo "deploy: https://console.$domain, https://api.$domain/health, https://games.$domain/xo/"
