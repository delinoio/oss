#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
image="${DELIBASE_TEST_POSTGRES_IMAGE:-postgres:17-alpine}"
container="delibase-test-postgres-$$"
port="${DELIBASE_TEST_POSTGRES_PORT:-55432}"

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run --rm -d \
  --name "$container" \
  -e POSTGRES_USER=delibase \
  -e POSTGRES_PASSWORD=delibase_test \
  -e POSTGRES_DB=delibase \
  -p "127.0.0.1:${port}:5432" \
  "$image" >/dev/null

for _ in $(seq 1 60); do
  if docker exec "$container" pg_isready -U delibase -d delibase >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker exec "$container" pg_isready -U delibase -d delibase >/dev/null

cd "$repo_root"
DELIBASE_TEST_DATABASE_URL="postgres://delibase:delibase_test@127.0.0.1:${port}/delibase?sslmode=disable" \
  go test ./servers/delibase/...
