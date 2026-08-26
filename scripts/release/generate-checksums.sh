#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Generate SHA256 checksums for release artifacts and optionally sign each artifact with cosign.

Usage:
  ./scripts/release/generate-checksums.sh --artifacts-dir <dir> [--sigstore-dir <dir>]

Options:
  --artifacts-dir <dir>  Directory containing release artifacts.
  --sigstore-dir <dir>   Separate destination for Sigstore bundles. Defaults to
                         the artifact directory for backwards compatibility.

Environment:
  REQUIRE_COSIGN         When "1" (default), fail if cosign is unavailable.
USAGE
}

artifacts_dir=""
sigstore_dir=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --artifacts-dir)
      artifacts_dir="${2:-}"
      shift 2
      ;;
    --sigstore-dir)
      sigstore_dir="${2:-}"
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

artifacts_dir="$(cd "$artifacts_dir" && pwd -P)"
if [ -z "$sigstore_dir" ]; then
  sigstore_dir="$artifacts_dir"
else
  mkdir -p "$sigstore_dir"
  sigstore_dir="$(cd "$sigstore_dir" && pwd -P)"
fi

pushd "$artifacts_dir" >/dev/null

artifacts=()
while IFS= read -r artifact; do
  case "$artifact" in
    *$'\n'*|*$'\r'*)
      echo "[release.checksum] artifact names must not contain newlines" >&2
      exit 1
      ;;
  esac
  artifacts+=("$artifact")
done < <(
  find . -type f \
    ! -name 'SHA256SUMS' \
    ! -name '*.sigstore.json' \
    -print | sed 's#^\./##' | LC_ALL=C sort
)

# When Sigstore output is nested below the artifact directory, exclude it from
# the checksum input without treating unrelated .sig or .pem files as ours.
if [[ "$sigstore_dir/" == "$artifacts_dir/"* ]] && [ "$sigstore_dir" != "$artifacts_dir" ]; then
  sigstore_relative="${sigstore_dir#"$artifacts_dir/"}"
  filtered=()
  for artifact in "${artifacts[@]}"; do
    case "$artifact" in "$sigstore_relative"/*) ;; *) filtered+=("$artifact") ;; esac
  done
  artifacts=("${filtered[@]}")
fi

if [ "${#artifacts[@]}" -eq 0 ]; then
  echo "[release.checksum] no artifacts found in $artifacts_dir" >&2
  exit 1
fi

: > SHA256SUMS
for artifact in "${artifacts[@]}"; do
  if [ ! -f "$artifact" ]; then
    continue
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -- "$artifact" >> SHA256SUMS
  else
    shasum -a 256 -- "$artifact" >> SHA256SUMS
  fi
  echo "[release.checksum] checksum generated for $artifact" >&2
done

echo "[release.checksum] wrote SHA256SUMS" >&2

if command -v cosign >/dev/null 2>&1; then
  for artifact in "${artifacts[@]}"; do
    bundle="$sigstore_dir/${artifact}.sigstore.json"
    mkdir -p "$(dirname "$bundle")"
    rm -f "$bundle"
    echo "[release.checksum] signing $artifact with cosign" >&2
    cosign sign-blob --yes \
      --bundle "$bundle" \
      "$artifact"
  done

  checksum_bundle="$sigstore_dir/SHA256SUMS.sigstore.json"
  rm -f "$checksum_bundle"
  echo "[release.checksum] signing SHA256SUMS with cosign" >&2
  cosign sign-blob --yes \
    --bundle "$checksum_bundle" \
    SHA256SUMS
elif [ "$require_cosign" = "1" ]; then
  echo "[release.checksum] cosign is required but not available" >&2
  exit 1
else
  echo "[release.checksum] cosign unavailable; signing skipped" >&2
fi

popd >/dev/null
