#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Install binpm with package manager or direct release artifact.

Usage:
  ./scripts/install/binpm.sh [--version <semver|latest>] [--method <package-manager|direct>] [--install-dir <dir>]

Options:
  --version <semver|latest>      Version without v-prefix (default: latest)
  --method <package-manager|direct>
                                 Install method (default: package-manager)
  --install-dir <dir>            Binary install directory for direct mode (default: ~/.local/bin)
USAGE
}

repo="delinoio/oss"
tag_prefix="binpm@v"
supported_direct_targets="darwin/amd64 (macOS x64), darwin/arm64 (macOS arm64), linux/amd64 (Linux x64), linux/arm64 (Linux arm64), windows/amd64 (Windows x64), windows/arm64 (Windows arm64)"
unsupported_platform_hint="Use an x64/arm64 host or supported CI image for direct install, use Homebrew or cargo-binstall where they support your host, or build binpm from source."

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
      echo "[install.binpm] unknown argument: $1" >&2
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
      echo "[install.binpm] failed to resolve latest binpm tag" >&2
      exit 1
    fi
    printf '%s\n' "$latest_tag"
    return
  fi

  printf '%s%s\n' "$tag_prefix" "$version"
}

install_via_package_manager() {
  if command -v brew >/dev/null 2>&1; then
    echo "[install.binpm] installing via Homebrew" >&2
    brew install delinoio/tap/binpm
    return 0
  fi

  echo "[install.binpm] package-manager mode requested but Homebrew is not available; falling back to direct" >&2
  return 1
}

unsupported_direct_platform() {
  local os="$1"
  local arch="$2"

  echo "[install.binpm] unsupported host platform for direct installation: detected os=${os}, arch=${arch}" >&2
  echo "[install.binpm] no first-party binpm direct installer artifact is published for this detected host" >&2
  echo "[install.binpm] supported direct-install targets: ${supported_direct_targets}" >&2
  echo "[install.binpm] recommended alternatives: ${unsupported_platform_hint}" >&2
  return 1
}

detect_direct_platform() {
  local uname_os
  uname_os="${BINPM_INSTALL_TEST_UNAME_OS:-$(uname -s)}"
  uname_os="$(printf '%s' "$uname_os" | tr '[:upper:]' '[:lower:]')"

  local uname_arch
  uname_arch="${BINPM_INSTALL_TEST_UNAME_ARCH:-$(uname -m)}"

  local os=""
  case "$uname_os" in
    linux*)
      os="linux"
      ;;
    darwin*)
      os="darwin"
      ;;
    *)
      unsupported_direct_platform "$uname_os" "$uname_arch"
      return
      ;;
  esac

  local arch=""
  case "$uname_arch" in
    x86_64|amd64)
      arch="amd64"
      ;;
    arm64|aarch64)
      arch="arm64"
      ;;
    x86|i386|i486|i586|i686|ia32|386)
      unsupported_direct_platform "$os" "$uname_arch"
      return
      ;;
    *)
      unsupported_direct_platform "$os" "$uname_arch"
      return
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
  local asset_name="binpm-${os}-${arch}.${ext}"
  local base_url="https://github.com/${repo}/releases/download/${tag}"

  local tmp_dir
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT

  pushd "$tmp_dir" >/dev/null

  echo "[install.binpm] downloading artifact: $asset_name" >&2
  curl -fsSLO "${base_url}/${asset_name}"
  curl -fsSLO "${base_url}/SHA256SUMS"

  grep " ${asset_name}$" SHA256SUMS > SHA256SUMS.binpm
  shasum -a 256 -c SHA256SUMS.binpm

  tar -xzf "$asset_name"

  mkdir -p "$install_dir"
  install -m 0755 binpm "$install_dir/binpm"

  popd >/dev/null

  echo "[install.binpm] installed binpm to $install_dir/binpm" >&2
}

case "$method" in
  package-manager)
    install_via_package_manager || install_direct
    ;;
  direct)
    install_direct
    ;;
  *)
    echo "[install.binpm] unsupported method: $method" >&2
    exit 1
    ;;
esac
