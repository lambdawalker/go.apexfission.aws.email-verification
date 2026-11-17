#!/usr/bin/env bash
set -euo pipefail
# Call before any infrastructure mutation. Remaining arguments are the deployment
# command, invoked only after effective configuration passes validation.
if [ "$#" -lt 1 ]; then echo 'usage: preflight.sh CONFIG.json [DEPLOY_COMMAND ...]' >&2; exit 2; fi
config_file=$1
shift
go run ./cmd/verify-config "$config_file"
if [ "$#" -gt 0 ]; then exec "$@"; fi
