#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Install with-watch with package manager or direct release artifact.

Usage:
  ./scripts/install/with-watch.sh [--version <semver|latest>] [--method <package-manager|direct>] [--install-dir <dir>]

Options:
  --version <semver|latest>      Version without v-prefix (default: latest)
  --method <package-manager|direct>
                                 Install method (default: package-manager)
  --install-dir <dir>            Binary install directory for direct mode (default: ~/.local/bin)
USAGE
}

repo="delinoio/oss"
tag_prefix="with-watch@v"
workflow_identity="https://github.com/delinoio/oss/.github/workflows/release-with-watch.yml@"

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
      echo "[install.with-watch] unknown argument: $1" >&2
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
      echo "[install.with-watch] failed to resolve latest with-watch tag" >&2
      exit 1
    fi
    printf '%s\n' "$latest_tag"
    return
  fi

  printf '%s%s\n' "$tag_prefix" "$version"
}

install_via_package_manager() {
  if command -v brew >/dev/null 2>&1; then
    echo "[install.with-watch] installing via Homebrew" >&2
    brew install delinoio/tap/with-watch
    return 0
  fi

  echo "[install.with-watch] package-manager mode requested but Homebrew is not available; falling back to direct" >&2
  return 1
}

download_bundle() {
  local base_url="$1"
  local artifact="$2"
  local bundle_name="${artifact}.sigstore.json"

  if ! curl -fsSLO "${base_url}/${bundle_name}"; then
    echo "[install.with-watch] missing bundle sidecar: ${bundle_name}" >&2
    echo "[install.with-watch] direct installs require releases published with Sigstore bundle sidecars" >&2
    exit 1
  fi
}

verify_bundle() {
  local artifact="$1"

  if ! command -v cosign >/dev/null 2>&1; then
    echo "[install.with-watch] cosign is required for direct install verification" >&2
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
      echo "[install.with-watch] unsupported OS for this installer: $uname_os" >&2
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
      echo "[install.with-watch] unsupported architecture: $uname_arch" >&2
      exit 1
      ;;
  esac

  local asset_name="with-watch-${os}-${arch}.tar.gz"
  local base_url="https://github.com/${repo}/releases/download/${tag}"

  local tmp_dir
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT

  pushd "$tmp_dir" >/dev/null

  echo "[install.with-watch] downloading artifact: $asset_name" >&2
  curl -fsSLO "${base_url}/${asset_name}"
  curl -fsSLO "${base_url}/SHA256SUMS"
  download_bundle "$base_url" "$asset_name"

  grep " ${asset_name}$" SHA256SUMS > SHA256SUMS.with-watch
  shasum -a 256 -c SHA256SUMS.with-watch
  verify_bundle "$asset_name"

  tar -xzf "$asset_name"

  mkdir -p "$install_dir"
  install -m 0755 with-watch "$install_dir/with-watch"

  popd >/dev/null

  echo "[install.with-watch] installed with-watch to $install_dir/with-watch" >&2
}

case "$method" in
  package-manager)
    install_via_package_manager || install_direct
    ;;
  direct)
    install_direct
    ;;
  *)
    echo "[install.with-watch] unsupported method: $method" >&2
    exit 1
    ;;
esac
