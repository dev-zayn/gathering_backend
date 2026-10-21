#!/bin/sh
# Writes the password file the app section is behind, from the
# environment, before nginx starts. Refusing to start without one beats
# starting open.
set -eu

if [ -z "${DOCS_USER:-}" ] || [ -z "${DOCS_PASSWORD:-}" ]; then
    echo "docs: DOCS_USER and DOCS_PASSWORD are required" >&2
    exit 1
fi

hash=$(openssl passwd -6 "$DOCS_PASSWORD")
printf '%s:%s\n' "$DOCS_USER" "$hash" > /etc/nginx/docs.htpasswd
chown root:nginx /etc/nginx/docs.htpasswd
chmod 0640 /etc/nginx/docs.htpasswd
