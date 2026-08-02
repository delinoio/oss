#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
image="${REALQA_TEST_IMAGE:-devhud-realqa:fixture}"
build_image="${REALQA_TEST_BUILD_IMAGE:-true}"
expected_version="${REALQA_TEST_EXPECTED_VERSION:-fixture}"
expected_revision="${REALQA_TEST_EXPECTED_REVISION:-$(git rev-parse HEAD)}"
expected_source="https://github.com/delinoio/oss"
container="devhud-realqa-image-contents-$$"
filesystem_listing="$(mktemp)"

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  rm -f "$filesystem_listing"
}
trap cleanup EXIT

cd "$repo_root"
if [ "$build_image" = "true" ]; then
  docker build \
    --build-arg "VERSION=${expected_version}" \
    --build-arg "REVISION=${expected_revision}" \
    --build-arg "SOURCE=${expected_source}" \
    --file servers/devhud-realqa/Dockerfile \
    --tag "$image" \
    .
elif [ "$build_image" != "false" ]; then
  echo "REALQA_TEST_BUILD_IMAGE must be true or false" >&2
  exit 1
fi

test "$(docker image inspect --format '{{.Config.User}}' "$image")" = "65532:65532"
test "$(docker image inspect --format '{{json .Config.Entrypoint}}' "$image")" = '["/devhud-realqa"]'
test "$(docker image inspect --format '{{json .Config.ExposedPorts}}' "$image")" = '{"8080/tcp":{}}'
if docker image inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$image" |
  grep -Eq '^(REALQA_|DEVHUD_|GITHUB_|R2_)'; then
  echo "RealQA image must not bake runtime configuration or credentials" >&2
  exit 1
fi

check_label() {
  name="$1"
  expected="$2"
  actual="$(docker image inspect --format "{{ index .Config.Labels \"${name}\" }}" "$image")"
  test "$actual" = "$expected"
}
check_label "org.opencontainers.image.title" "devhud-realqa"
check_label "org.opencontainers.image.version" "$expected_version"
check_label "org.opencontainers.image.revision" "$expected_revision"
check_label "org.opencontainers.image.source" "$expected_source"
check_label "org.opencontainers.image.licenses" "Apache-2.0"

docker create --name "$container" "$image" >/dev/null
docker export "$container" | tar -tf - >"$filesystem_listing"
grep -qx devhud-realqa "$filesystem_listing"
if grep -Eq '^(src|go\.mod|go\.sum|\.git|Dockerfile|scripts)(/|$)' "$filesystem_listing"; then
  echo "RealQA image contains build-only material" >&2
  exit 1
fi
