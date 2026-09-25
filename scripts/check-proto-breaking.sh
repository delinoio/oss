#!/usr/bin/env bash

set -euo pipefail

baseline="${DEVHUD_PROTO_BASELINE:-origin/main}"

if git cat-file -e "${baseline}:buf.yaml" 2>/dev/null && \
  git cat-file -e "${baseline}:protos/devhud/v1/common.proto" 2>/dev/null; then
  # Buf clones the baseline repository, but only reads schemas. Keep unrelated
  # LFS assets as pointers: a pointer-only CI checkout has no local LFS objects
  # for that file:// clone to fetch. Scope this to Buf so asset builds hydrate.
  # Remove the override if Buf stops checking out unrelated baseline files.
  GIT_LFS_SKIP_SMUDGE=1 pnpm exec buf breaking --against ".git#ref=${baseline}"
  exit 0
fi

echo "DevHud protocol has no schema baseline at ${baseline}; treating this change as the v1 baseline."
