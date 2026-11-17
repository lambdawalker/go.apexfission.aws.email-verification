#!/usr/bin/env bash
set -euo pipefail
if [ "$#" -lt 5 ] || [ "$#" -gt 6 ]; then
  echo 'usage: testhost-deploy.sh test-STACK AWS_REGION DOMAIN SENDER RECIPIENT [preview|up]' >&2
  exit 2
fi
stack=$1; region=$2; domain=$3; sender=$4; recipient=$5; action=${6:-preview}
case "$stack" in test-*) ;; *) echo 'Use a dedicated test-* stack.' >&2; exit 2;; esac
case "$action" in preview|up) ;; *) echo 'Choose preview or up.' >&2; exit 2;; esac
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
# Validate before stack/config mutation or any cloud deployment.
go run ./cmd/verify-config config/examples/default.json
bash scripts/testhost-build.sh
cd infrastructure/cmd/testhost
pulumi stack select "$stack" --create
pulumi config set aws:region "$region"
pulumi config set domain "$domain"
pulumi config set sender "$sender"
pulumi config set recipient "$recipient"
pulumi config set archive "$root/build/testhost.zip"
# Preserve explicit custom timing policy on repeated deployments.
if ! pulumi config get policyJSON >/dev/null 2>&1; then
  pulumi config set policyJSON "$(cat "$root/config/examples/default.json")"
fi
# The native entry point validates the actual stack policy again before registration.
pulumi "$action"
