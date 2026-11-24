#!/usr/bin/env sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output="$root/dist/discovery"
mkdir -p "$output"
stage=$(mktemp -d "$output/.build.XXXXXXXX")
trap 'rm -rf "$stage"' EXIT
trap 'exit 1' HUP INT TERM

(
  cd "$root/infrastructure"
  GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -mod=readonly -trimpath -tags lambda.norpc \
    -o "$stage/bootstrap" github.com/lambdawalker/go.infra.composer/adapters/awsdiscovery/cmd/registry
)
python3 - "$stage" <<'PYTHON'
import sys
from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile

stage = Path(sys.argv[1])
with ZipFile(stage / "index.zip", "w", ZIP_DEFLATED) as archive:
    archive.write(stage / "bootstrap", "bootstrap")
PYTHON
mv "$stage/index.zip" "$output/index.zip"
printf '%s\n' "$output/index.zip"
