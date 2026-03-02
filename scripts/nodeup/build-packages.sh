#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
TEMPLATE_FILE="${REPO_ROOT}/crates/nodeup/packaging/nfpm.yaml"

VERSION=""
ARCH=""
BINARY_PATH=""
OUT_DIR=""

print_usage() {
  cat <<'USAGE'
Usage: build-packages.sh --version <vX.Y.Z> --arch <x64|arm64> --binary <path> --out-dir <path>

Build nodeup Linux package artifacts (.deb, .rpm, .pkg.tar.zst) from a release binary.
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
      --arch)
        [ "$#" -ge 2 ] || { echo "Missing value for --arch" >&2; exit 1; }
        ARCH="$2"
        shift 2
        ;;
      --binary)
        [ "$#" -ge 2 ] || { echo "Missing value for --binary" >&2; exit 1; }
        BINARY_PATH="$2"
        shift 2
        ;;
      --out-dir)
        [ "$#" -ge 2 ] || { echo "Missing value for --out-dir" >&2; exit 1; }
        OUT_DIR="$2"
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

  if [ -z "$VERSION" ] || [ -z "$ARCH" ] || [ -z "$BINARY_PATH" ] || [ -z "$OUT_DIR" ]; then
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

  case "$ARCH" in
    x64|arm64) ;;
    *)
      echo "Invalid --arch value: ${ARCH}" >&2
      exit 1
      ;;
  esac

  if [ ! -f "$BINARY_PATH" ]; then
    echo "Binary file not found: ${BINARY_PATH}" >&2
    exit 1
  fi

  if ! command -v nfpm >/dev/null 2>&1; then
    echo "nfpm is required but was not found in PATH" >&2
    exit 1
  fi
}

map_nfpm_arch() {
  case "$1" in
    x64)
      printf '%s\n' "amd64"
      ;;
    arm64)
      printf '%s\n' "arm64"
      ;;
    *)
      return 1
      ;;
  esac
}

main() {
  parse_args "$@"

  local version_no_v
  local nfpm_arch
  local binary_abs
  local work_dir
  local config_file
  local base_name

  version_no_v="${VERSION#v}"
  nfpm_arch="$(map_nfpm_arch "$ARCH")"
  binary_abs="$(cd -- "$(dirname -- "$BINARY_PATH")" && pwd)/$(basename -- "$BINARY_PATH")"

  mkdir -p "$OUT_DIR"

  work_dir="$(mktemp -d)"
  trap 'rm -rf "${work_dir}"' EXIT
  config_file="${work_dir}/nfpm.yaml"

  sed \
    -e "s|__NFPM_ARCH__|${nfpm_arch}|g" \
    -e "s|__NODEUP_VERSION__|${version_no_v}|g" \
    -e "s|__NODEUP_BINARY_PATH__|${binary_abs}|g" \
    "$TEMPLATE_FILE" >"$config_file"

  base_name="nodeup-${VERSION}-linux-${ARCH}"

  nfpm package --config "$config_file" --packager deb --target "${OUT_DIR}/${base_name}.deb"
  nfpm package --config "$config_file" --packager rpm --target "${OUT_DIR}/${base_name}.rpm"
  nfpm package --config "$config_file" --packager archlinux --target "${OUT_DIR}/${base_name}.pkg.tar.zst"

  echo "Built package artifacts in ${OUT_DIR}" >&2
}

main "$@"
