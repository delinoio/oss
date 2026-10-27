#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"
go test ./scripts/aiprouter/fixture
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s "scripts/aiprouter" -p 'test_*.py'
node --test "scripts/aiprouter/installer.test.cjs"
# Schema validation needs no daemon. Do not print the resolved environment.
docker compose --env-file "packaging/aiprouter/.env.example" -f "packaging/aiprouter/compose.yml" -f "packaging/aiprouter/compose.fixture.yml" config --no-env-resolution --quiet
