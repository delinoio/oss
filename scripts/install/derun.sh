#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Install derun with package manager or direct release artifact.

Usage:
  ./scripts/install/derun.sh [--version <semver|latest>] [--method <package-manager|direct>] [--install-dir <dir>]

Options:
  --version <semver|latest>      Version without v-prefix (default: latest)
  --method <package-manager|direct>
                                 Install method (default: package-manager)
  --install-dir <dir>            Binary install directory for direct mode (default: ~/.local/bin)
USAGE
}

repo="delinoio/oss"
tag_prefix="derun@v"
workflow_identity="https://github.com/delinoio/oss/.github/workflows/release-derun.yml@"

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
      echo "[install.derun] unknown argument: $1" >&2
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
      echo "[install.derun] failed to resolve latest derun tag" >&2
      exit 1
    fi
    printf '%s\n' "$latest_tag"
    return
  fi

  printf '%s%s\n' "$tag_prefix" "$version"
}

install_via_package_manager() {
  if command -v brew >/dev/null 2>&1; then
    echo "[install.derun] installing via Homebrew" >&2
    brew install delinoio/tap/derun
    return 0
  fi

  echo "[install.derun] package-manager mode requested but Homebrew is not available; falling back to direct" >&2
  return 1
}

verify_signature() {
  local artifact="$1"

  if ! command -v cosign >/dev/null 2>&1; then
    echo "[install.derun] cosign is required for direct install verification" >&2
    exit 1
  fi

  cosign verify-blob \
    --certificate "${artifact}.pem" \
    --signature "${artifact}.sig" \
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
      echo "[install.derun] unsupported OS for this installer: $uname_os" >&2
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
      echo "[install.derun] unsupported architecture: $uname_arch" >&2
      exit 1
      ;;
  esac

  if [ "$os" = "linux" ] && [ "$arch" = "arm64" ]; then
    echo "[install.derun] linux arm64 direct artifacts are not published yet" >&2
    exit 1
  fi

  local ext="tar.gz"
  local asset_name="derun-${os}-${arch}.${ext}"
  local base_url="https://github.com/${repo}/releases/download/${tag}"

  local tmp_dir
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT

  pushd "$tmp_dir" >/dev/null

  echo "[install.derun] downloading artifact: $asset_name" >&2
  curl -fsSLO "${base_url}/${asset_name}"
  curl -fsSLO "${base_url}/SHA256SUMS"
  curl -fsSLO "${base_url}/${asset_name}.sig"
  curl -fsSLO "${base_url}/${asset_name}.pem"

  grep " ${asset_name}$" SHA256SUMS > SHA256SUMS.derun
  shasum -a 256 -c SHA256SUMS.derun
  verify_signature "$asset_name"

  tar -xzf "$asset_name"

  mkdir -p "$install_dir"
  install -m 0755 derun "$install_dir/derun"

  popd >/dev/null

  echo "[install.derun] installed derun to $install_dir/derun" >&2
}

case "$method" in
  package-manager)
    install_via_package_manager || install_direct
    ;;
  direct)
    install_direct
    ;;
  *)
    echo "[install.derun] unsupported method: $method" >&2
    exit 1
    ;;
esac
