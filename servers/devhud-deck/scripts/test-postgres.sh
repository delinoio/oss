#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
image="${DECK_TEST_POSTGRES_IMAGE:-postgres:17-alpine}"
container="deck-test-postgres-$$"
port="${DECK_TEST_POSTGRES_PORT:-55433}"

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run --rm -d \
  --name "$container" \
  -e POSTGRES_USER=deck \
  -e POSTGRES_PASSWORD=deck_test \
  -e POSTGRES_DB=deck \
  -p "127.0.0.1:${port}:5432" \
  "$image" >/dev/null

for _ in $(seq 1 60); do
  if docker exec "$container" pg_isready -U deck -d deck >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker exec "$container" pg_isready -U deck -d deck >/dev/null

cd "$repo_root"
DECK_TEST_DATABASE_URL="postgres://deck:deck_test@127.0.0.1:${port}/deck?sslmode=disable" \
  go test -count=1 ./servers/devhud-deck/...

