#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
printf '{"retention_seconds":1}\n' > "$work/invalid.json"
if bash scripts/preflight.sh "$work/invalid.json" touch "$work/mutated"; then
  echo 'invalid config passed deployment gate' >&2; exit 1
fi
test ! -e "$work/mutated"
bash scripts/preflight.sh config/examples/default.json touch "$work/valid"
test -e "$work/valid"
