#!/usr/bin/env bash

set -euo pipefail

baseline="${DEVHUD_PROTO_BASELINE:-origin/main}"

if git cat-file -e "${baseline}:buf.yaml" 2>/dev/null && \
  git cat-file -e "${baseline}:protos/devhud/v1/common.proto" 2>/dev/null; then
  # Buf clones the local Git baseline even though it compares only proto files.
  # Skip LFS smudging in that clone: its file:// remote may lack unrelated assets.
  # Remove this when Buf can clone only the proto tree or guarantees LFS objects.
  GIT_LFS_SKIP_SMUDGE=1 pnpm exec buf breaking --against ".git#ref=${baseline}"
  exit 0
fi

echo "DevHud protocol has no schema baseline at ${baseline}; treating this change as the v1 baseline."
