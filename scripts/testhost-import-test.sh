#!/usr/bin/env bash
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
cd "$dir"
go mod init example.com/external-host >/dev/null
go mod edit -go=1.25.0 -require=github.com/lambdawalker/go.apexfission.aws.email-verification@v0.0.0 -replace="github.com/lambdawalker/go.apexfission.aws.email-verification=$root"
cat > main.go <<'GO'
package main
import _ "github.com/lambdawalker/go.apexfission.aws.email-verification/internal/testhost"
func main() {}
GO
if go build -mod=mod . >result.txt 2>&1; then echo 'External import unexpectedly succeeded' >&2; exit 1; fi
if ! grep -q 'use of internal package .* not allowed' result.txt; then cat result.txt >&2; exit 1; fi
echo 'External test-host import rejected as expected.'
