#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

if ! command -v sqlc >/dev/null 2>&1; then
  echo "sqlc is required; install the version pinned by CI" >&2
  exit 1
fi

sqlc generate -f servers/delibase/db/sqlc.yaml
