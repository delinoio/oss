#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"
go test ./scripts/tairouter/fixture
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s "scripts/tairouter" -p 'test_*.py'
node --test "scripts/tairouter/installer.test.cjs"
node --test "scripts/tairouter/local.test.mjs"
# Schema validation needs no daemon. Do not print the resolved environment.
# Some Compose versions still check env_file existence with --no-env-resolution.
# Replace the deployment file only for this schema check; never create or read a
# real deployment .env. Remove the override when supported versions skip that check.
docker compose --env-file "packaging/tairouter/.env.example" -f "packaging/tairouter/compose.yml" -f "packaging/tairouter/compose.fixture.yml" -f - config --no-env-resolution --quiet <<'YAML'
services:
  tairouter:
    env_file: !override [.env.example]
YAML
TAIROUTER_LOCAL_ENV_FILE="$root/packaging/tairouter/.env.example" docker compose --env-file "packaging/tairouter/.env.example" -f "packaging/tairouter/compose.local.yml" config --no-env-resolution --quiet
