#!/bin/sh
# Gathering is a separate product from the one it is modelled on. Nothing of
# that brand may land here: not a name, not the mascot, not the font, not a
# colour. CI fails on a hit. CLAUDE.md is excused: it names the reference
# repositories this one is modelled on.
set -eu
here="$(pwd)"
cd "$(dirname "$0")/.."

# Every file is read for the names and the colours, pictures included: a name
# can ride in a picture's metadata.
names='(^|[^a-z])gini|(^|[^a-z])genie|جني|rubik|B8410F|FBF6F0|supergini'
# The reference's three-letter prefix is read in text files only. It is an
# identifier, and identifiers are text; a picture's compressed bytes spell
# three letters by chance (one PNG held "sG_").
prefix='(^|[^a-z])sg_'

# This repo by default; `scripts/brand-check.sh ../gathering_games` reads the
# games, which live outside it (a folder named is read from where it was run).
roots=""
for d in "$@"; do
  case $d in
    /*) roots="$roots $d" ;;
    *) roots="$roots $here/$d" ;;
  esac
done
roots="${roots:-.}"

scan() {
  # shellcheck disable=SC2086 # one word per folder
  grep -rniE "$@" \
    --exclude-dir=node_modules --exclude-dir=.git --exclude-dir=dist --exclude-dir=dist-app \
    --exclude-dir=bin --exclude='package-lock.json' --exclude='brand-check.sh' --exclude='CLAUDE.md' $roots || true
}

hits="$(scan "$names")"
prefixed="$(scan -I "$prefix")"
if [ -n "$prefixed" ]; then
  hits="${hits:+$hits
}$prefixed"
fi
if [ -n "$hits" ]; then
  echo "brand-check: GINI identifiers found:"
  echo "$hits"
  exit 1
fi
echo "brand-check: clean"
