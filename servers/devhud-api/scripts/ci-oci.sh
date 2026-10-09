#!/usr/bin/env bash
set -euo pipefail
artifact="$RUNNER_TEMP/devhud-$OCI_TARGET.oci.tar"
image="devhud-$OCI_TARGET:ci"
version=$(node -p "require('./packaging/devhud/release-metadata.json').version")
docker buildx build --platform linux/amd64,linux/arm64 --target "$OCI_TARGET" --build-arg VERSION="$version" --build-arg REVISION="$GITHUB_SHA" --output "type=oci,dest=$artifact" -f servers/devhud-api/Dockerfile .
tar -tf "$artifact" | grep -Fx index.json
for arch in amd64 arm64; do
  skopeo inspect --override-arch "$arch" --config "oci-archive:$artifact" | jq -e '.config.User == "65532" and .config.Labels["io.delino.devhud.migrations"] == "embedded" and .config.Labels["io.delino.devhud.administrator-assets"] == "embedded"'
done
docker buildx build --platform linux/amd64 --target "$OCI_TARGET" --build-arg VERSION="$version" --build-arg REVISION="$GITHUB_SHA" --load --tag "$image" -f servers/devhud-api/Dockerfile .
container_database_url="postgres://devhud:devhud@host.docker.internal:5432/devhud_api_test?sslmode=disable"
docker_args=(--rm --user 65532:65532 --add-host host.docker.internal:host-gateway -e "DEVHUD_DATABASE_URL=$container_database_url")
if [ "$OCI_TARGET" = api ]; then
  docker run "${docker_args[@]}" "$image" migrate
else
  DEVHUD_DATABASE_URL="$DEVHUD_TEST_DATABASE_URL" go run ./servers/devhud-api/cmd/devhud-api migrate
  docker run "${docker_args[@]}" \
    -e DEVHUD_ENVIRONMENT=development \
    -e DEVHUD_R2_ENDPOINT=http://127.0.0.1:9000 \
    -e DEVHUD_R2_ACCESS_KEY_ID=ci-access-key \
    -e DEVHUD_R2_SECRET_ACCESS_KEY=01234567890123456789012345678901 \
    -e DEVHUD_R2_STAGING_BUCKET=devhud-staging \
    -e DEVHUD_R2_PUBLIC_BUCKET=devhud-public \
    -e DEVHUD_PUBLIC_ASSET_BASE_URL=http://127.0.0.1:9000 \
    -e DEVHUD_CLOUDFLARE_API_TOKEN=ci-token \
    -e DEVHUD_CLOUDFLARE_ZONE_ID=ci-zone \
    "$image" --once
fi
"$CI_SYFT_COMMAND" scan "docker:$image" --output "spdx-json=$RUNNER_TEMP/devhud-$OCI_TARGET.spdx.json"
jq -e '.spdxVersion == "SPDX-2.3" and (.packages | length > 0)' "$RUNNER_TEMP/devhud-$OCI_TARGET.spdx.json"
