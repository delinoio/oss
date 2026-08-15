#!/usr/bin/env bash

set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "${repo_root}"

./scripts/generate-devhud-proto.sh

git diff --exit-code -- protos/devhud/v1 packages/devhud-api-client/src/gen

untracked_generated="$(
  git ls-files --others --exclude-standard -- \
    protos/devhud/v1 packages/devhud-api-client/src/gen
)"
if [ -n "${untracked_generated}" ]; then
  echo "Generated output contains untracked files:" >&2
  printf '%s\n' "${untracked_generated}" >&2
  exit 1
fi
