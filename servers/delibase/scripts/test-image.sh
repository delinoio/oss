#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
image="${DELIBASE_TEST_IMAGE:-delibase:test}"
container="delibase-test-image-$$"
port="${DELIBASE_TEST_IMAGE_PORT:-58080}"
database_url="${DELIBASE_IMAGE_TEST_DATABASE_URL:-postgres://delibase:delibase_test@host.docker.internal:5432/delibase?sslmode=disable}"

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
}
trap cleanup EXIT

cd "$repo_root"
docker build \
  --file servers/delibase/Dockerfile \
  --tag "$image" \
  .

image_user="$(docker image inspect --format '{{.Config.User}}' "$image")"
if [ "$image_user" != "65532:65532" ]; then
  echo "delibase image must run as uid/gid 65532" >&2
  exit 1
fi

docker run --rm -d \
  --name "$container" \
  --add-host host.docker.internal:host-gateway \
  -p "127.0.0.1:${port}:8080" \
  -e DELIBASE_API_ORIGIN=https://delibase.deli.dev \
  -e DELIBASE_CORS_ALLOWED_ORIGINS=https://deli.dev \
  -e DELIBASE_CATALOG_PATH=/etc/delibase/catalog.json \
  -e "DELIBASE_DATABASE_URL=${database_url}" \
  -e DELIBASE_LOGTO_ISSUER=https://identity.example.com/oidc \
  -e DELIBASE_LOGTO_AUDIENCE=https://delibase.deli.dev \
  -e DELIBASE_LOGTO_JWKS_URL=https://identity.example.com/oidc/jwks \
  -e DELIBASE_LOGTO_M2M_CLIENT_ID=image-test-service \
  -e DELIBASE_LOGTO_M2M_CLIENT_SECRET=image-test-secret \
  -e DELIBASE_POLAR_ACCESS_TOKEN=image-test-token \
  -e DELIBASE_POLAR_WEBHOOK_SECRET=image-test-webhook-secret \
  -e DELIBASE_LOG_PSEUDONYM_KEY=0123456789abcdef0123456789abcdef \
  "$image" >/dev/null

for _ in $(seq 1 30); do
  if curl --fail --silent "http://127.0.0.1:${port}/healthz" >/dev/null &&
    curl --fail --silent "http://127.0.0.1:${port}/readyz" >/dev/null; then
    exit 0
  fi
  if ! docker inspect --format '{{.State.Running}}' "$container" 2>/dev/null | grep -qx true; then
    docker logs "$container" >&2
    exit 1
  fi
  sleep 1
done

docker logs "$container" >&2
echo "delibase image did not become healthy and ready" >&2
exit 1
