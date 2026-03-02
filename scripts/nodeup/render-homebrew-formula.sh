#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
TEMPLATE_FILE="${REPO_ROOT}/crates/nodeup/packaging/homebrew/nodeup.rb.tmpl"

VERSION=""
SHA_DARWIN_X64=""
SHA_DARWIN_ARM64=""
OUTPUT_FILE=""

print_usage() {
  cat <<'USAGE'
Usage: render-homebrew-formula.sh \
  --version <vX.Y.Z> \
  --sha-darwin-x64 <sha256> \
  --sha-darwin-arm64 <sha256> \
  [--output <path>]

Render Homebrew Formula content from the nodeup template.
USAGE
}

parse_args() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --version)
        [ "$#" -ge 2 ] || { echo "Missing value for --version" >&2; exit 1; }
        VERSION="$2"
        shift 2
        ;;
      --sha-darwin-x64)
        [ "$#" -ge 2 ] || { echo "Missing value for --sha-darwin-x64" >&2; exit 1; }
        SHA_DARWIN_X64="$2"
        shift 2
        ;;
      --sha-darwin-arm64)
        [ "$#" -ge 2 ] || { echo "Missing value for --sha-darwin-arm64" >&2; exit 1; }
        SHA_DARWIN_ARM64="$2"
        shift 2
        ;;
      --output)
        [ "$#" -ge 2 ] || { echo "Missing value for --output" >&2; exit 1; }
        OUTPUT_FILE="$2"
        shift 2
        ;;
      --help|-h)
        print_usage
        exit 0
        ;;
      *)
        echo "Unknown argument: $1" >&2
        exit 1
        ;;
    esac
  done

  if [ -z "$VERSION" ] || [ -z "$SHA_DARWIN_X64" ] || [ -z "$SHA_DARWIN_ARM64" ]; then
    print_usage
    exit 1
  fi

  case "$VERSION" in
    v[0-9]*.[0-9]*.[0-9]*) ;;
    *)
      echo "Invalid --version value: ${VERSION}" >&2
      exit 1
      ;;
  esac
}

main() {
  parse_args "$@"

  local version_no_v
  local rendered

  version_no_v="${VERSION#v}"

  rendered="$(sed \
    -e "s|__NODEUP_VERSION__|${VERSION}|g" \
    -e "s|__NODEUP_VERSION_NO_V__|${version_no_v}|g" \
    -e "s|__SHA_DARWIN_X64__|${SHA_DARWIN_X64}|g" \
    -e "s|__SHA_DARWIN_ARM64__|${SHA_DARWIN_ARM64}|g" \
    "$TEMPLATE_FILE")"

  if [ -n "$OUTPUT_FILE" ]; then
    mkdir -p "$(dirname -- "$OUTPUT_FILE")"
    printf '%s\n' "$rendered" >"$OUTPUT_FILE"
    echo "Rendered Homebrew formula to ${OUTPUT_FILE}" >&2
    return
  fi

  printf '%s\n' "$rendered"
}

main "$@"
