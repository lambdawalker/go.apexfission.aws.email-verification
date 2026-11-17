#!/usr/bin/env bash
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
mkdir -p build/testhost
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -tags lambda.norpc -o build/testhost/bootstrap ./cmd/testhost
python3 - <<'PY'
from pathlib import Path
import zipfile
binary=Path('build/testhost/bootstrap')
with zipfile.ZipFile('build/testhost.zip','w',zipfile.ZIP_DEFLATED) as z:
    info=zipfile.ZipInfo('bootstrap')
    info.create_system=3
    info.external_attr=0o100755 << 16
    info.compress_type=zipfile.ZIP_DEFLATED
    z.writestr(info,binary.read_bytes())
PY
