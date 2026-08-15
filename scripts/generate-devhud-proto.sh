#!/usr/bin/env bash

set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "${repo_root}"

# Both locations below are generator-owned. Removing only recognized output
# prevents deleted proto symbols from surviving as orphaned generated files.
find protos/devhud/v1 -type f \
  \( -name '*.pb.go' -o -name '*.connect.go' \) \
  -delete

generated_ts="packages/devhud-api-client/src/gen"
if [ -d "${generated_ts}" ]; then
  find "${generated_ts}" -type f -delete
fi

pnpm exec buf generate
