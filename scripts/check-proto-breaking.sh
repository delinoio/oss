#!/usr/bin/env bash

set -euo pipefail

baseline="${DEVHUD_PROTO_BASELINE:-origin/main}"

if git cat-file -e "${baseline}:buf.yaml" 2>/dev/null && \
  git cat-file -e "${baseline}:protos/devhud/v1/common.proto" 2>/dev/null; then
  pnpm exec buf breaking --against ".git#ref=${baseline}"
  exit 0
fi

echo "DevHud protocol has no schema baseline at ${baseline}; treating this change as the v1 baseline."
