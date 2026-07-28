#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

if ! command -v sqlc >/dev/null 2>&1; then
  echo "sqlc v1.30.0 is required" >&2
  exit 1
fi

check_root="$(mktemp -d)"
trap 'rm -rf "$check_root"' EXIT
cp -R servers/devhud-realqa/internal/database/dbgen "$check_root/dbgen-before"

servers/devhud-realqa/scripts/generate-sqlc.sh
diff -ru "$check_root/dbgen-before" servers/devhud-realqa/internal/database/dbgen
