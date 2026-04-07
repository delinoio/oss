#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Generate SHA256 checksums for release artifacts and optionally sign each artifact with cosign.

Usage:
  ./scripts/release/generate-checksums.sh --artifacts-dir <dir>

Options:
  --artifacts-dir <dir>  Directory containing release artifacts.

Environment:
  REQUIRE_COSIGN         When "1" (default), fail if cosign is unavailable.
USAGE
}

artifacts_dir=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --artifacts-dir)
      artifacts_dir="${2:-}"
      shift 2
      ;;
    --help)
      usage
      exit 0
      ;;
    *)
      echo "[release.checksum] unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [ -z "$artifacts_dir" ]; then
  echo "[release.checksum] --artifacts-dir is required" >&2
  exit 1
fi

if [ ! -d "$artifacts_dir" ]; then
  echo "[release.checksum] artifact directory does not exist: $artifacts_dir" >&2
  exit 1
fi

require_cosign="${REQUIRE_COSIGN:-1}"

pushd "$artifacts_dir" >/dev/null

find . -maxdepth 1 -type f \
  \( -name '*.sigstore.json' -o -name '*.sig' -o -name '*.pem' \) \
  -delete

artifacts=()
while IFS= read -r artifact; do
  artifacts+=("$artifact")
done < <(
  find . -maxdepth 1 -type f \
    ! -name 'SHA256SUMS' \
    ! -name 'SHA256SUMS.sigstore.json' \
    ! -name 'SHA256SUMS.sig' \
    ! -name 'SHA256SUMS.pem' \
    ! -name '*.sigstore.json' \
    ! -name '*.sig' \
    ! -name '*.pem' \
    -print | sed 's#^\./##' | LC_ALL=C sort
)

if [ "${#artifacts[@]}" -eq 0 ]; then
  echo "[release.checksum] no artifacts found in $artifacts_dir" >&2
  exit 1
fi

rm -f SHA256SUMS
for artifact in "${artifacts[@]}"; do
  if [ ! -f "$artifact" ]; then
    continue
  fi
  shasum -a 256 "$artifact" >> SHA256SUMS
  echo "[release.checksum] checksum generated for $artifact" >&2
done

echo "[release.checksum] wrote SHA256SUMS" >&2

if command -v cosign >/dev/null 2>&1; then
  for artifact in "${artifacts[@]}"; do
    echo "[release.checksum] signing $artifact with cosign" >&2
    cosign sign-blob --yes \
      --bundle "${artifact}.sigstore.json" \
      "$artifact"
  done

  echo "[release.checksum] signing SHA256SUMS with cosign" >&2
  cosign sign-blob --yes \
    --bundle SHA256SUMS.sigstore.json \
    SHA256SUMS
elif [ "$require_cosign" = "1" ]; then
  echo "[release.checksum] cosign is required but not available" >&2
  exit 1
else
  echo "[release.checksum] cosign unavailable; signing skipped" >&2
fi

popd >/dev/null
