#!/usr/bin/env bash

set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "${repo_root}"

against="${BUF_BREAKING_AGAINST:-.git#branch=main,subdir=protos}"

# The first protocol change has no schema to compare. Once a Git baseline has
# any .proto file, Buf's FILE policy applies to every subsequent change.
if [[ "${against}" == .git#* ]]; then
  git_selector="${against#*#}"
  git_selector="${git_selector%%,*}"
  git_ref="${git_selector#branch=}"
  git_ref="${git_ref#ref=}"
  baseline_files="$(git ls-tree -r --name-only "${git_ref}" -- protos 2>/dev/null || true)"
  if ! grep -qE '\.proto$' <<<"${baseline_files}"; then
    echo "Skipping Buf breaking check: ${git_ref} has no protobuf baseline"
    exit 0
  fi
fi

pnpm exec buf breaking --against "${against}"
