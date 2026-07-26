#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
image="${DELIBASE_TEST_IMAGE:-delibase:test}"
build_image="${DELIBASE_TEST_BUILD_IMAGE:-true}"
expected_version="${DELIBASE_TEST_EXPECTED_VERSION:-test}"
expected_revision="${DELIBASE_TEST_EXPECTED_REVISION:-$(git rev-parse HEAD)}"
expected_source="https://github.com/delinoio/oss"
container="delibase-test-image-$$"
contents_container="${container}-contents"
port="${DELIBASE_TEST_IMAGE_PORT:-}"
database_url="${DELIBASE_IMAGE_TEST_DATABASE_URL:-postgres://delibase:delibase_test@host.docker.internal:5432/delibase?sslmode=disable}"
polar_tls_dir=""
polar_pid=""
filesystem_listing=""

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  docker rm -f "$contents_container" >/dev/null 2>&1 || true
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
  if [ -n "$filesystem_listing" ]; then
    rm -f "$filesystem_listing"
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

if [ "$build_image" = "true" ]; then
  docker build \
    --build-arg "VERSION=${expected_version}" \
    --build-arg "REVISION=${expected_revision}" \
    --build-arg "SOURCE=${expected_source}" \
    --file servers/delibase/Dockerfile \
    --tag "$image" \
    .
elif [ "$build_image" != "false" ]; then
  echo "DELIBASE_TEST_BUILD_IMAGE must be true or false" >&2
  exit 1
fi

image_user="$(docker image inspect --format '{{.Config.User}}' "$image")"
if [ "$image_user" != "65532:65532" ]; then
  echo "delibase image must run as uid/gid 65532" >&2
  exit 1
fi
if [ "$(docker image inspect --format '{{json .Config.Entrypoint}}' "$image")" != '["/delibase"]' ]; then
  echo "delibase image entrypoint must contain only /delibase" >&2
  exit 1
fi
if [ "$(docker image inspect --format '{{json .Config.ExposedPorts}}' "$image")" != '{"8080/tcp":{}}' ]; then
  echo "delibase image must expose only port 8080" >&2
  exit 1
fi
if docker image inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$image" |
  grep -Eq '^(DELIBASE_|PUBLIC_)'; then
  echo "delibase image must not bake runtime configuration or secrets into its environment" >&2
  exit 1
fi

check_label() {
  name="$1"
  expected="$2"
  actual="$(docker image inspect --format "{{ index .Config.Labels \"${name}\" }}" "$image")"
  if [ "$actual" != "$expected" ]; then
    echo "delibase image label ${name} is ${actual}, expected ${expected}" >&2
    exit 1
  fi
}
check_label "org.opencontainers.image.title" "delibase"
check_label "org.opencontainers.image.version" "$expected_version"
check_label "org.opencontainers.image.revision" "$expected_revision"
check_label "org.opencontainers.image.source" "$expected_source"
check_label "org.opencontainers.image.licenses" "MIT"

filesystem_listing="$(mktemp)"
docker create --name "$contents_container" "$image" >/dev/null
docker export "$contents_container" | tar -tf - >"$filesystem_listing"
for required_path in "delibase" "etc/delibase/catalog.json"; do
  if ! grep -qx "$required_path" "$filesystem_listing"; then
    echo "delibase image is missing ${required_path}" >&2
    exit 1
  fi
done
# Docker exports relative paths, so anchor build-context names at the image
# root instead of rejecting standard base-image directories such as usr/src.
if grep -Eq '^(src|go\.mod|go\.sum|\.git|Dockerfile|scripts)(/|$)' "$filesystem_listing"; then
  echo "delibase image contains build-only material" >&2
  grep -E '^(src|go\.mod|go\.sum|\.git|Dockerfile|scripts)(/|$)' "$filesystem_listing" >&2
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
