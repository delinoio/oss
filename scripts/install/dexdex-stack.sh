#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Install DexDex desktop + server stack with package manager or direct release artifacts.

Usage:
  ./scripts/install/dexdex-stack.sh [--version <semver|latest>] [--method <package-manager|direct>] [--install-dir <dir>]

Options:
  --version <semver|latest>      Version without v-prefix (default: latest)
  --method <package-manager|direct>
                                 Install method (default: package-manager)
  --install-dir <dir>            Binary install directory for direct server install (default: ~/.local/bin)
USAGE
}

repo="delinoio/oss"
tag_prefix="dexdex/v"
workflow_identity="https://github.com/delinoio/oss/.github/workflows/release-dexdex.yml@"

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
      echo "[install.dexdex] unknown argument: $1" >&2
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
      echo "[install.dexdex] failed to resolve latest dexdex tag" >&2
      exit 1
    fi
    printf '%s\n' "$latest_tag"
    return
  fi

  printf '%s%s\n' "$tag_prefix" "$version"
}

verify_signature() {
  local artifact="$1"

  if ! command -v cosign >/dev/null 2>&1; then
    echo "[install.dexdex] cosign is required for direct install verification" >&2
    exit 1
  fi

  cosign verify-blob \
    --certificate "${artifact}.pem" \
    --signature "${artifact}.sig" \
    --certificate-identity-regexp "$workflow_identity" \
    --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
    "$artifact"
}

download_and_verify() {
  local base_url="$1"
  local asset_name="$2"

  curl -fsSLO "${base_url}/${asset_name}"
  curl -fsSLO "${base_url}/${asset_name}.sig"
  curl -fsSLO "${base_url}/${asset_name}.pem"

  grep " ${asset_name}$" SHA256SUMS > "SHA256SUMS.${asset_name}"
  shasum -a 256 -c "SHA256SUMS.${asset_name}"
  verify_signature "$asset_name"
}

install_via_package_manager() {
  if command -v brew >/dev/null 2>&1; then
    echo "[install.dexdex] installing desktop app and servers via Homebrew" >&2
    brew install --cask delinoio/tap/dexdex
    brew install delinoio/tap/dexdex-main-server
    brew install delinoio/tap/dexdex-worker-server
    return 0
  fi

  echo "[install.dexdex] package-manager mode requested but Homebrew is unavailable; falling back to direct" >&2
  return 1
}

install_direct() {
  local tag
  tag="$(resolve_tag)"

  local uname_os
  uname_os="$(uname -s | tr '[:upper:]' '[:lower:]')"

  local os=""
  case "$uname_os" in
    linux*)
      os="linux"
      ;;
    darwin*)
      os="darwin"
      ;;
    *)
      echo "[install.dexdex] unsupported OS for this installer: $uname_os" >&2
      exit 1
      ;;
  esac

  local uname_arch
  uname_arch="$(uname -m)"

  local arch=""
  case "$uname_arch" in
    x86_64|amd64)
      arch="amd64"
      ;;
    arm64|aarch64)
      arch="arm64"
      ;;
    *)
      echo "[install.dexdex] unsupported architecture: $uname_arch" >&2
      exit 1
      ;;
  esac

  local base_url="https://github.com/${repo}/releases/download/${tag}"
  local tmp_dir
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT

  local main_asset="dexdex-main-server-${os}-${arch}.tar.gz"
  local worker_asset="dexdex-worker-server-${os}-${arch}.tar.gz"
  local desktop_asset=""

  if [ "$os" = "linux" ]; then
    desktop_asset="dexdex-desktop-linux-${arch}.AppImage"
  else
    desktop_asset="dexdex-desktop-darwin-universal.dmg"
  fi

  pushd "$tmp_dir" >/dev/null

  echo "[install.dexdex] downloading checksums" >&2
  curl -fsSLO "${base_url}/SHA256SUMS"

  echo "[install.dexdex] downloading server artifacts" >&2
  download_and_verify "$base_url" "$main_asset"
  download_and_verify "$base_url" "$worker_asset"

  mkdir -p "$install_dir"

  tar -xzf "$main_asset"
  tar -xzf "$worker_asset"

  install -m 0755 dexdex-main-server "$install_dir/dexdex-main-server"
  install -m 0755 dexdex-worker-server "$install_dir/dexdex-worker-server"

  echo "[install.dexdex] downloading desktop installer artifact" >&2
  download_and_verify "$base_url" "$desktop_asset"

  if [ "$os" = "linux" ]; then
    install -m 0755 "$desktop_asset" "$install_dir/dexdex-desktop"
    echo "[install.dexdex] installed Linux desktop AppImage to $install_dir/dexdex-desktop" >&2
  else
    local desktop_target="${HOME}/Downloads/${desktop_asset}"
    mkdir -p "$(dirname -- "$desktop_target")"
    cp "$desktop_asset" "$desktop_target"
    echo "[install.dexdex] downloaded macOS desktop DMG to $desktop_target" >&2
    echo "[install.dexdex] open the DMG and drag DexDex.app to Applications" >&2
  fi

  popd >/dev/null

  echo "[install.dexdex] installed server binaries to $install_dir" >&2
}

case "$method" in
  package-manager)
    install_via_package_manager || install_direct
    ;;
  direct)
    install_direct
    ;;
  *)
    echo "[install.dexdex] unsupported method: $method" >&2
    exit 1
    ;;
esac
