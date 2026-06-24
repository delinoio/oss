#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Install nodeup with package manager or direct release artifact.

Usage:
  ./scripts/install/nodeup.sh [--version <semver|latest>] [--method <package-manager|direct>] [--install-dir <dir>]

Options:
  --version <semver|latest>      Version without v-prefix (default: latest)
  --method <package-manager|direct>
                                 Install method (default: package-manager)
  --install-dir <dir>            Binary install directory for direct mode (default: ~/.local/bin)
USAGE
}

repo="delinoio/oss"
tag_prefix="nodeup@v"
supported_platforms="macOS x64, macOS arm64, Linux x64, Linux arm64, Windows x64, and Windows arm64"
unsupported_platform_hint="Use an x64/arm64 host or a supported CI image: macOS x64/arm64, Linux x64/arm64, or Windows x64/arm64."

version="latest"
method="package-manager"
install_dir="${HOME}/.local/bin"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      version="${2:-}"
      shift 2
      ;;
    --method)
      method="${2:-}"
      shift 2
      ;;
    --install-dir)
      install_dir="${2:-}"
      shift 2
      ;;
    --help)
      usage
      exit 0
      ;;
    *)
      echo "[install.nodeup] unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

resolve_latest_tag() {
  if command -v gh >/dev/null 2>&1; then
    gh release list --repo "$repo" --limit 200 --json tagName \
      --jq "map(select(.tagName | startswith(\"${tag_prefix}\")))[0].tagName"
    return
  fi

  curl -fsSL "https://api.github.com/repos/${repo}/releases?per_page=200" \
    | awk -F '"' '/"tag_name"/ {print $4}' \
    | grep "^${tag_prefix}" \
    | head -n1
}

resolve_tag() {
  if [ "$version" = "latest" ]; then
    local latest_tag
    latest_tag="$(resolve_latest_tag)"
    if [ -z "$latest_tag" ] || [ "$latest_tag" = "null" ]; then
      echo "[install.nodeup] failed to resolve latest nodeup tag" >&2
      exit 1
    fi
    printf '%s\n' "$latest_tag"
    return
  fi

  printf '%s%s\n' "$tag_prefix" "$version"
}

install_via_package_manager() {
  if command -v brew >/dev/null 2>&1; then
    echo "[install.nodeup] installing via Homebrew" >&2
    brew install delinoio/tap/nodeup
    return 0
  fi

  echo "[install.nodeup] package-manager mode requested but Homebrew is not available; falling back to direct" >&2
  return 1
}

detect_direct_platform() {
  local uname_os
  uname_os="${NODEUP_INSTALL_TEST_UNAME_OS:-$(uname -s)}"
  uname_os="$(printf '%s' "$uname_os" | tr '[:upper:]' '[:lower:]')"

  local os=""
  case "$uname_os" in
    linux*)
      os="linux"
      ;;
    darwin*)
      os="darwin"
      ;;
    *)
      echo "[install.nodeup] unsupported host platform for direct installation: os=${uname_os}, arch=unknown" >&2
      echo "[install.nodeup] supported platforms: ${supported_platforms}; x86 hosts are unsupported" >&2
      echo "[install.nodeup] hint: ${unsupported_platform_hint}" >&2
      return 1
      ;;
  esac

  local uname_arch
  uname_arch="${NODEUP_INSTALL_TEST_UNAME_ARCH:-$(uname -m)}"

  local arch=""
  case "$uname_arch" in
    x86_64|amd64)
      arch="amd64"
      ;;
    arm64|aarch64)
      arch="arm64"
      ;;
    x86|i386|i486|i586|i686|ia32|386)
      echo "[install.nodeup] unsupported host platform for direct installation: os=${os}, arch=${uname_arch}" >&2
      echo "[install.nodeup] supported platforms: ${supported_platforms}; x86 hosts are unsupported" >&2
      echo "[install.nodeup] hint: ${unsupported_platform_hint}" >&2
      return 1
      ;;
    *)
      echo "[install.nodeup] unsupported host platform for direct installation: os=${os}, arch=${uname_arch}" >&2
      echo "[install.nodeup] supported platforms: ${supported_platforms}; x86 hosts are unsupported" >&2
      echo "[install.nodeup] hint: ${unsupported_platform_hint}" >&2
      return 1
      ;;
  esac

  printf '%s %s\n' "$os" "$arch"
}

install_direct() {
  local platform
  platform="$(detect_direct_platform)" || exit 1
  local os="${platform%% *}"
  local arch="${platform##* }"

  local tag
  tag="$(resolve_tag)"

  local ext="tar.gz"
  local asset_name="nodeup-${os}-${arch}.${ext}"
  local base_url="https://github.com/${repo}/releases/download/${tag}"

  local tmp_dir
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT

  pushd "$tmp_dir" >/dev/null

  echo "[install.nodeup] downloading artifact: $asset_name" >&2
  curl -fsSLO "${base_url}/${asset_name}"
  curl -fsSLO "${base_url}/SHA256SUMS"

  grep " ${asset_name}$" SHA256SUMS > SHA256SUMS.nodeup
  shasum -a 256 -c SHA256SUMS.nodeup

  tar -xzf "$asset_name"

  mkdir -p "$install_dir"
  install -m 0755 nodeup "$install_dir/nodeup"

  popd >/dev/null

  echo "[install.nodeup] installed nodeup to $install_dir/nodeup" >&2
}

case "$method" in
  package-manager)
    install_via_package_manager || install_direct
    ;;
  direct)
    install_direct
    ;;
  *)
    echo "[install.nodeup] unsupported method: $method" >&2
    exit 1
    ;;
esac
