#!/bin/sh
# Gathering is a separate product from the one it is modelled on. Nothing of
# that brand may land here: not a name, not the mascot, not the font, not a
# colour. CI fails on a hit. CLAUDE.md is excused: it names the reference
# repositories this one is modelled on.
set -eu
cd "$(dirname "$0")/.."
hits="$(grep -rniE '(^|[^a-z])gini|(^|[^a-z])genie|جني|rubik|B8410F|FBF6F0|supergini|(^|[^a-z])sg_' \
  --exclude-dir=node_modules --exclude-dir=.git --exclude-dir=dist --exclude-dir=dist-app \
  --exclude-dir=bin --exclude='package-lock.json' --exclude='brand-check.sh' --exclude='CLAUDE.md' . || true)"
if [ -n "$hits" ]; then
  echo "brand-check: GINI identifiers found:"
  echo "$hits"
  exit 1
fi
echo "brand-check: clean"
