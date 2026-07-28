#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

if ! command -v sqlc >/dev/null 2>&1; then
  echo "sqlc v1.30.0 is required" >&2
  exit 1
fi

sqlc generate -f servers/devhud-realqa/db/sqlc.yaml
