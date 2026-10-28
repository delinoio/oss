#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"
go test ./scripts/aiprouter/fixture
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s "scripts/aiprouter" -p 'test_*.py'
node --test "scripts/aiprouter/installer.test.cjs"
node --test "scripts/aiprouter/local.test.mjs"
# Schema validation needs no daemon. Do not print the resolved environment.
# Some Compose versions still check env_file existence with --no-env-resolution.
# Replace the deployment file only for this schema check; never create or read a
# real deployment .env. Remove the override when supported versions skip that check.
docker compose --env-file "packaging/aiprouter/.env.example" -f "packaging/aiprouter/compose.yml" -f "packaging/aiprouter/compose.fixture.yml" -f - config --no-env-resolution --quiet <<'YAML'
services:
  aiprouter:
    env_file: !override [.env.example]
YAML
AIPROUTER_LOCAL_ENV_FILE="$root/packaging/aiprouter/.env.example" docker compose --env-file "packaging/aiprouter/.env.example" -f "packaging/aiprouter/compose.local.yml" config --no-env-resolution --quiet
