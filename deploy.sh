#!/usr/bin/env sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
command -v go >/dev/null 2>&1 || { echo "Install Go 1.25+ and add it to PATH." >&2; exit 1; }
cd "$ROOT"
GOWORK=off exec go run -modfile=tools/deploy/go.mod -mod=readonly github.com/lambdawalker/go.infra.composer/deploycli/cmd/infra-deploy "$@"
