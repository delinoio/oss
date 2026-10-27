#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Install pinned generators outside the repository; never modify the root Go module.
set -euo pipefail
repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
tool_dir="${AIPROUTER_PROTO_TOOLS:-/tmp/aiprouter-tools}"
if [[ "$(protoc --version)" != "libprotoc 33.0" ]]; then
  echo "Generation requires protoc 33.0" >&2
  exit 1
fi
mkdir -p "$tool_dir"
GOBIN="$tool_dir" go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
GOBIN="$tool_dir" go install connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.21.1
es_plugin="$repo_root/packages/aiprouter-client/node_modules/.bin/protoc-gen-es"
if [[ ! -x "$es_plugin" ]]; then
  echo "Install workspace dependencies before generation (pnpm install)." >&2
  exit 1
fi
if [[ "$("$es_plugin" --version)" != "protoc-gen-es v2.16.0" ]]; then
  echo "Generation requires protoc-gen-es v2.16.0" >&2
  exit 1
fi
out_dir="$(mktemp -d)"
trap 'rm -rf "$out_dir"' EXIT
mkdir -p "$out_dir/go" "$out_dir/ts"
protoc -I "$repo_root/protos" \
  --plugin="protoc-gen-go=$tool_dir/protoc-gen-go" \
  --plugin="protoc-gen-connect-go=$tool_dir/protoc-gen-connect-go" \
  --plugin="protoc-gen-es=$es_plugin" \
  --go_out="$out_dir/go" --go_opt=paths=source_relative \
  --connect-go_out="$out_dir/go" --connect-go_opt=paths=source_relative \
  --es_out="$out_dir/ts" --es_opt=target=ts,import_extension=js \
  "$repo_root"/protos/aiprouter/v1/*.proto
# Compare the exact generated file sets, including stale outputs, without rewriting during checks.
python3 - "$repo_root" "$out_dir" "${1:-}" <<'PY'
from pathlib import Path
import shutil
import sys
root, out, mode = Path(sys.argv[1]), Path(sys.argv[2]), sys.argv[3]
if mode not in ('', '--check'):
    raise SystemExit('Usage: generate.sh [--check]')
pairs = [(out/'go/aiprouter/v1', root/'protos/aiprouter/v1', '*.pb.go'),
         (out/'go/aiprouter/v1/aiprouterv1connect', root/'protos/aiprouter/v1/aiprouterv1connect', '*.connect.go'),
         (out/'ts', root/'packages/aiprouter-client/src/gen', '*.ts')]
drift = []
for src, dst, pattern in pairs:
    generated = {p.relative_to(src): p for p in src.rglob(pattern)}
    existing = {p.relative_to(dst): p for p in dst.rglob(pattern)}
    for rel in sorted(generated.keys() | existing.keys()):
        if rel not in generated:
            drift.append(str(dst/rel))
            if not mode: existing[rel].unlink()
        elif rel not in existing or generated[rel].read_bytes() != existing[rel].read_bytes():
            drift.append(str(dst/rel))
            if not mode:
                (dst/rel).parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(generated[rel], dst/rel)
if mode and drift:
    raise SystemExit('Generated output drift:\n' + '\n'.join(drift))
print('Generated output matches.' if mode else 'Generated bindings updated.')
PY
