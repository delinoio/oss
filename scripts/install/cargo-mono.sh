#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Install cargo-mono with direct release artifacts.

Usage:
  ./scripts/install/cargo-mono.sh [--version <semver|latest>] [--method <package-manager|direct>] [--install-dir <dir>]

Options:
  --version <semver|latest>      Version without v-prefix (default: latest)
  --method <package-manager|direct>
                                 Install method (default: direct)
  --install-dir <dir>            Binary install directory for direct mode (default: ~/.local/bin)
USAGE
}

repo="delinoio/oss"
tag_prefix="cargo-mono@v"
workflow_identity="https://github.com/delinoio/oss/.github/workflows/release-cargo-mono.yml@"

version="latest"
method="direct"
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
      echo "[install.cargo-mono] unknown argument: $1" >&2
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
      echo "[install.cargo-mono] failed to resolve latest cargo-mono tag" >&2
      exit 1
    fi
    printf '%s\n' "$latest_tag"
    return
  fi

  printf '%s%s\n' "$tag_prefix" "$version"
}

download_bundle() {
  local base_url="$1"
  local artifact="$2"
  local bundle_name="${artifact}.sigstore.json"

  if ! curl -fsSLO "${base_url}/${bundle_name}"; then
    echo "[install.cargo-mono] missing bundle sidecar: ${bundle_name}" >&2
    echo "[install.cargo-mono] direct installs require releases published with Sigstore bundle sidecars" >&2
    exit 1
  fi
}

verify_bundle() {
  local artifact="$1"

  if ! command -v cosign >/dev/null 2>&1; then
    echo "[install.cargo-mono] cosign is required for direct install verification" >&2
    exit 1
  fi

  cosign verify-blob \
    --bundle "${artifact}.sigstore.json" \
    --certificate-identity-regexp "$workflow_identity" \
    --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
    "$artifact"
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
      echo "[install.cargo-mono] unsupported OS for this installer: $uname_os" >&2
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
      echo "[install.cargo-mono] unsupported architecture: $uname_arch" >&2
      exit 1
      ;;
  esac

  local asset_name="cargo-mono-${os}-${arch}.tar.gz"
  local base_url="https://github.com/${repo}/releases/download/${tag}"

  local tmp_dir
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT

  pushd "$tmp_dir" >/dev/null

  echo "[install.cargo-mono] downloading artifact: $asset_name" >&2
  curl -fsSLO "${base_url}/${asset_name}"
  curl -fsSLO "${base_url}/SHA256SUMS"
  download_bundle "$base_url" "$asset_name"

  grep " ${asset_name}$" SHA256SUMS > SHA256SUMS.cargo-mono
  shasum -a 256 -c SHA256SUMS.cargo-mono
  verify_bundle "$asset_name"

  tar -xzf "$asset_name"

  mkdir -p "$install_dir"
  install -m 0755 cargo-mono "$install_dir/cargo-mono"

  popd >/dev/null

  echo "[install.cargo-mono] installed cargo-mono to $install_dir/cargo-mono" >&2
}

case "$method" in
  package-manager)
    echo "[install.cargo-mono] package-manager mode is not available yet; using direct installation instead" >&2
    install_direct
    ;;
  direct)
    install_direct
    ;;
  *)
    echo "[install.cargo-mono] unsupported method: $method" >&2
    exit 1
    ;;
esac
