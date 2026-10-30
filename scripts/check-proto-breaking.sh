#!/usr/bin/env bash

set -euo pipefail

baseline="${PROTO_BASELINE:-origin/main}"

if git cat-file -e "${baseline}:buf.yaml" 2>/dev/null; then
  # Buf clones the baseline repository, but only reads schemas. Keep unrelated
  # LFS assets as pointers: a pointer-only CI checkout has no local LFS objects
  # for that file:// clone to fetch. Scope this to Buf so asset builds hydrate.
  # Remove the override if Buf stops checking out unrelated baseline files.
  node "$(dirname "${BASH_SOURCE[0]}")/check-proto-breaking.mjs" "${baseline}"
  exit 0
fi

echo "Protocol module has no schema baseline at ${baseline}; treating this change as the v1 baseline."
