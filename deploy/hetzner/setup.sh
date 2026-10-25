#!/bin/sh
# On the server, as root, run by deploy.sh on every deploy: Docker, the
# folders, and, the first time only, the secrets in /opt/ga3da/secrets. The
# passwords and the sign-in code are printed once, then only readable there.
#
#   sh setup.sh ga3da.fun
set -eu
domain="${1:?usage: setup.sh <domain>}"
root=/opt/ga3da
secrets="$root/secrets"

command -v docker >/dev/null || curl -fsSL https://get.docker.com | sh

# The images are built here, and a Go or npm build wants more than a 2 GB
# server has: a swap file, once, makes such a build slow instead of killed.
if [ -z "$(swapon --show --noheadings)" ]; then
  fallocate -l 4G /swapfile && chmod 600 /swapfile && mkswap /swapfile >/dev/null && swapon /swapfile
  echo '/swapfile none swap sw 0 0' >>/etc/fstab
fi

# The api's container runs as uid 10001: its pictures folder and its key are
# that user's.
mkdir -p "$secrets/firebase" "$root/data/files"
chmod 700 "$secrets"
chown 10001:10001 "$root/data/files" "$secrets/firebase"

# The console's password as the api checks it (CONSOLE_PASSWORD_HASH): a
# bcrypt hash, single-quoted so compose takes its dollars as they are.
console_hash() {
  printf "CONSOLE_PASSWORD_HASH='%s'\n" \
    "$(docker run --rm caddy:2.10-alpine caddy hash-password --plaintext "$(cat "$secrets/console.password")")"
}

if [ -f "$secrets/ga3da.env" ]; then
  # A server set up while Caddy asked for the console's password: the same
  # password, now asked by the console's own sign-in.
  if ! grep -q '^CONSOLE_PASSWORD_HASH=' "$secrets/ga3da.env"; then
    console_hash >>"$secrets/ga3da.env"
    rm -f "$secrets/console.caddy"
  fi
  exit 0
fi

pg="$(openssl rand -hex 24)"
jwt="$(openssl ecparam -name prime256v1 -genkey -noout | openssl pkcs8 -topk8 -nocrypt -outform DER | base64 | tr -d '\n')"
otp="$(printf '%06d' "$(($(od -An -N4 -tu4 /dev/urandom | tr -d ' ') % 1000000))")"
docs_pw="$(openssl rand -hex 12)"
console_pw="$(openssl rand -hex 12)"

umask 077
cat >"$secrets/ga3da.env" <<EOF
DOMAIN=$domain
SECRETS_DIR=$secrets
DATA_DIR=$root/data
POSTGRES_PASSWORD=$pg
JWT_PRIVATE_KEY=$jwt
OTP_FIXED_CODE=$otp
DOCS_USER=docs
DOCS_PASSWORD=$docs_pw
FCM_PROJECT_ID=gathering-supergame
FCM_CREDENTIALS_FILE=
APP_LINKS_SHA256=
APPLE_TEAM_ID=
APP_STORE_URL=
PLAY_STORE_URL=
PARTNER_CLIENT_SECRET=
EOF
# games.sh signs in to the console with it.
printf '%s\n' "$console_pw" >"$secrets/console.password"
console_hash >>"$secrets/ga3da.env"

cat <<EOF

  Keep these; they are not printed again (all in $secrets):
    console   https://console.$domain   any name     password $console_pw
    docs      https://docs.$domain      user docs    password $docs_pw
    sign-in code for every phone number: $otp

EOF
