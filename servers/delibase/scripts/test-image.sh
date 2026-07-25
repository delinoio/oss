#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
image="${DELIBASE_TEST_IMAGE:-delibase:test}"
container="delibase-test-image-$$"
port="${DELIBASE_TEST_IMAGE_PORT:-}"
database_url="${DELIBASE_IMAGE_TEST_DATABASE_URL:-postgres://delibase:delibase_test@host.docker.internal:5432/delibase?sslmode=disable}"
polar_tls_dir=""
polar_pid=""

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  if [ -n "$polar_pid" ]; then
    kill "$polar_pid" >/dev/null 2>&1 || true
    wait "$polar_pid" >/dev/null 2>&1 || true
  fi
  if [ -n "$polar_tls_dir" ]; then
    rm -f \
      "$polar_tls_dir/cert.pem" \
      "$polar_tls_dir/key.pem" \
      "$polar_tls_dir/server.log"
    rmdir "$polar_tls_dir"
  fi
}
trap cleanup EXIT

cd "$repo_root"
polar_tls_dir="$(mktemp -d)"
polar_port="$(python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "$polar_tls_dir/key.pem" \
  -out "$polar_tls_dir/cert.pem" \
  -days 1 \
  -subj "/CN=host.docker.internal" \
  -addext "subjectAltName=DNS:host.docker.internal" \
  >/dev/null 2>&1
python3 servers/delibase/scripts/mock-polar-server.py \
  "$polar_port" \
  "$polar_tls_dir/cert.pem" \
  "$polar_tls_dir/key.pem" \
  >"$polar_tls_dir/server.log" 2>&1 &
polar_pid=$!

for _ in $(seq 1 30); do
  if curl --fail --silent \
    --cacert "$polar_tls_dir/cert.pem" \
    --noproxy "*" \
    --resolve "host.docker.internal:${polar_port}:127.0.0.1" \
    "https://host.docker.internal:${polar_port}/v1/products/product_monthly_10_usd" \
    >/dev/null; then
    break
  fi
  if ! kill -0 "$polar_pid" >/dev/null 2>&1; then
    cat "$polar_tls_dir/server.log" >&2
    exit 1
  fi
  sleep 1
done
curl --fail --silent \
  --cacert "$polar_tls_dir/cert.pem" \
  --noproxy "*" \
  --resolve "host.docker.internal:${polar_port}:127.0.0.1" \
  "https://host.docker.internal:${polar_port}/v1/products/product_monthly_10_usd" \
  >/dev/null

docker build \
  --file servers/delibase/Dockerfile \
  --tag "$image" \
  .

image_user="$(docker image inspect --format '{{.Config.User}}' "$image")"
if [ "$image_user" != "65532:65532" ]; then
  echo "delibase image must run as uid/gid 65532" >&2
  exit 1
fi

port_mapping="127.0.0.1::8080"
if [ -n "$port" ]; then
  port_mapping="127.0.0.1:${port}:8080"
fi

docker run -d \
  --name "$container" \
  --add-host host.docker.internal:host-gateway \
  -p "$port_mapping" \
  -v "$polar_tls_dir/cert.pem:/etc/delibase/image-test-polar-ca.pem:ro" \
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
  -e DELIBASE_POLAR_PRODUCT_ID=product_monthly_10_usd \
  -e "DELIBASE_POLAR_API_URL=https://host.docker.internal:${polar_port}/v1" \
  -e DELIBASE_LOG_PSEUDONYM_KEY=0123456789abcdef0123456789abcdef \
  -e SSL_CERT_FILE=/etc/delibase/image-test-polar-ca.pem \
  "$image" >/dev/null

if [ -z "$port" ]; then
  port="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "8080/tcp") 0).HostPort}}' "$container")"
fi

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
