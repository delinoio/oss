#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib/common.sh
source "${SCRIPT_DIR}/lib/common.sh"
# shellcheck source=./lib/platform.sh
source "${SCRIPT_DIR}/lib/platform.sh"
# shellcheck source=./lib/manager.sh
source "${SCRIPT_DIR}/lib/manager.sh"
# shellcheck source=./lib/download.sh
source "${SCRIPT_DIR}/lib/download.sh"

NODEUP_METHOD="auto"
NODEUP_VERSION="latest"
NODEUP_MANAGER=""
NODEUP_PREFIX="~/.local"
NODEUP_YES=0
NODEUP_DRY_RUN=0
NODEUP_DEBUG=0
NODEUP_NO_PATH_UPDATE=0

print_usage() {
  cat <<'USAGE'
Usage: install.sh [options]

Install nodeup via one of the supported methods.

Options:
  --method <auto|homebrew|package|binary>
  --version <latest|vX.Y.Z>
  --manager <apt|dnf|yum|pacman|zypper>
  --prefix <path>
  --yes
  --dry-run
  --debug
  --no-path-update
  --help
USAGE
}

parse_args() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --method)
        [ "$#" -ge 2 ] || nodeup_die "Missing value for --method"
        NODEUP_METHOD="$2"
        shift 2
        ;;
      --version)
        [ "$#" -ge 2 ] || nodeup_die "Missing value for --version"
        NODEUP_VERSION="$2"
        shift 2
        ;;
      --manager)
        [ "$#" -ge 2 ] || nodeup_die "Missing value for --manager"
        NODEUP_MANAGER="$2"
        shift 2
        ;;
      --prefix)
        [ "$#" -ge 2 ] || nodeup_die "Missing value for --prefix"
        NODEUP_PREFIX="$2"
        shift 2
        ;;
      --yes)
        NODEUP_YES=1
        shift
        ;;
      --dry-run)
        NODEUP_DRY_RUN=1
        shift
        ;;
      --debug)
        NODEUP_DEBUG=1
        shift
        ;;
      --no-path-update)
        NODEUP_NO_PATH_UPDATE=1
        shift
        ;;
      --help|-h)
        print_usage
        exit 0
        ;;
      *)
        nodeup_die "Unknown argument: $1"
        ;;
    esac
  done

  case "$NODEUP_METHOD" in
    auto|homebrew|package|binary) ;;
    *)
      nodeup_die "Invalid --method value: ${NODEUP_METHOD}"
      ;;
  esac

  if [ -n "$NODEUP_MANAGER" ] && ! nodeup_is_supported_manager "$NODEUP_MANAGER"; then
    nodeup_die "Invalid --manager value: ${NODEUP_MANAGER}"
  fi
}

resolve_release() {
  local resolved
  resolved="$(nodeup_resolve_release_tag "$NODEUP_VERSION")"
  RELEASE_TAG="${resolved%% *}"
  RELEASE_VERSION="${resolved#* }"

  nodeup_info "Resolved release tag: ${RELEASE_TAG}"
}

detect_target_method() {
  local os="$1"
  local detected_manager="$2"

  if [ "$NODEUP_METHOD" != "auto" ]; then
    TARGET_METHOD="$NODEUP_METHOD"
    return
  fi

  local brew_present
  local can_manage_packages

  brew_present=0
  if nodeup_command_exists brew; then
    brew_present=1
  fi

  can_manage_packages=0
  if nodeup_can_manage_system_packages; then
    can_manage_packages=1
  fi

  TARGET_METHOD="$(nodeup_select_install_method "$os" "$brew_present" "$detected_manager" "$can_manage_packages")" || {
    nodeup_die "Unable to select install method for platform ${os}"
  }
}

find_extracted_binary() {
  local extract_dir="$1"

  if [ -f "${extract_dir}/nodeup" ]; then
    printf '%s\n' "${extract_dir}/nodeup"
    return 0
  fi

  if [ -f "${extract_dir}/bin/nodeup" ]; then
    printf '%s\n' "${extract_dir}/bin/nodeup"
    return 0
  fi

  local candidate
  candidate="$(find "$extract_dir" -type f -name nodeup | head -n 1)"
  if [ -n "$candidate" ]; then
    printf '%s\n' "$candidate"
    return 0
  fi

  return 1
}

install_with_homebrew() {
  if ! nodeup_command_exists brew; then
    nodeup_die "Homebrew is not installed. Re-run with --method binary"
  fi

  if [ "$NODEUP_VERSION" != "latest" ]; then
    nodeup_die "Homebrew install supports --version latest only"
  fi

  nodeup_info "Installing nodeup with Homebrew"
  nodeup_run_cmd brew tap delinoio/tap https://github.com/delinoio/homebrew-tap
  nodeup_run_cmd brew install nodeup

  nodeup_write_state "homebrew" "homebrew" "" "" "0" "$RELEASE_VERSION" "$RELEASE_TAG"
}

install_with_package() {
  local os="$1"
  local arch="$2"
  local detected_manager="$3"

  if [ "$os" != "linux" ]; then
    nodeup_die "Package install is supported on Linux only"
  fi

  local manager
  manager="$NODEUP_MANAGER"
  if [ -z "$manager" ]; then
    manager="$detected_manager"
  fi

  if [ -z "$manager" ]; then
    nodeup_die "No Linux package manager detected; re-run with --method binary"
  fi

  if ! nodeup_is_supported_manager "$manager"; then
    nodeup_die "Unsupported Linux package manager: ${manager}"
  fi

  if ! nodeup_set_sudo_mode; then
    nodeup_die "Package installation requires root privilege or sudo access"
  fi

  local extension
  extension="$(nodeup_package_extension_for_manager "$manager")"

  local package_asset
  local checksum_asset
  package_asset="nodeup-${RELEASE_VERSION}-linux-${arch}.${extension}"
  checksum_asset="nodeup-${RELEASE_VERSION}-checksums.txt"

  local temp_dir
  temp_dir="$(mktemp -d)"
  trap 'rm -rf "${temp_dir}"' RETURN

  local package_file
  local checksum_file
  package_file="${temp_dir}/${package_asset}"
  checksum_file="${temp_dir}/${checksum_asset}"

  nodeup_info "Downloading ${package_asset}"
  nodeup_download_release_asset "$RELEASE_TAG" "$package_asset" "$package_file"
  nodeup_download_release_asset "$RELEASE_TAG" "$checksum_asset" "$checksum_file"

  if [ "$NODEUP_DRY_RUN" != "1" ]; then
    nodeup_verify_checksum "$checksum_file" "$package_file" || nodeup_die "Checksum validation failed"
  fi

  nodeup_info "Installing ${package_asset} with ${manager}"
  nodeup_install_package_with_manager "$manager" "$package_file" || {
    nodeup_die "Package installation failed with manager ${manager}"
  }

  nodeup_write_state "package" "$manager" "/usr" "" "0" "$RELEASE_VERSION" "$RELEASE_TAG"
}

install_with_binary() {
  local os="$1"
  local arch="$2"

  local prefix
  local bin_dir
  local archive_asset
  local checksum_asset
  local temp_dir
  local archive_file
  local checksum_file
  local extract_dir
  local binary_path
  local profile_file
  local path_update

  prefix="$(nodeup_expand_home "$NODEUP_PREFIX")"
  bin_dir="${prefix%/}/bin"
  archive_asset="nodeup-${RELEASE_VERSION}-${os}-${arch}.tar.gz"
  checksum_asset="nodeup-${RELEASE_VERSION}-checksums.txt"

  temp_dir="$(mktemp -d)"
  trap 'rm -rf "${temp_dir}"' RETURN

  archive_file="${temp_dir}/${archive_asset}"
  checksum_file="${temp_dir}/${checksum_asset}"
  extract_dir="${temp_dir}/extract"

  nodeup_info "Downloading ${archive_asset}"
  nodeup_download_release_asset "$RELEASE_TAG" "$archive_asset" "$archive_file"
  nodeup_download_release_asset "$RELEASE_TAG" "$checksum_asset" "$checksum_file"

  if [ "$NODEUP_DRY_RUN" != "1" ]; then
    nodeup_verify_checksum "$checksum_file" "$archive_file" || nodeup_die "Checksum validation failed"

    mkdir -p "$extract_dir"
    tar -xzf "$archive_file" -C "$extract_dir"
    binary_path="$(find_extracted_binary "$extract_dir")" || nodeup_die "Could not find nodeup binary in archive"

    nodeup_run_cmd mkdir -p "$bin_dir"
    nodeup_run_cmd install -m 0755 "$binary_path" "${bin_dir}/nodeup"
    nodeup_run_cmd ln -sfn nodeup "${bin_dir}/node"
    nodeup_run_cmd ln -sfn nodeup "${bin_dir}/npm"
    nodeup_run_cmd ln -sfn nodeup "${bin_dir}/npx"
  else
    nodeup_run_cmd mkdir -p "$bin_dir"
    nodeup_run_cmd install -m 0755 "${extract_dir}/nodeup" "${bin_dir}/nodeup"
    nodeup_run_cmd ln -sfn nodeup "${bin_dir}/node"
    nodeup_run_cmd ln -sfn nodeup "${bin_dir}/npm"
    nodeup_run_cmd ln -sfn nodeup "${bin_dir}/npx"
  fi

  profile_file=""
  path_update=0

  if [ "$NODEUP_NO_PATH_UPDATE" = "0" ]; then
    profile_file="$(nodeup_default_profile_file)"
    nodeup_append_path_block "$profile_file" "$bin_dir"
    path_update=1
  fi

  nodeup_write_state "binary" "" "$prefix" "$profile_file" "$path_update" "$RELEASE_VERSION" "$RELEASE_TAG"
}

main() {
  parse_args "$@"

  export NODEUP_DRY_RUN
  export NODEUP_DEBUG
  export NODEUP_YES

  local os
  local arch
  local detected_manager

  read -r os arch <<<"$(nodeup_detect_platform)" || nodeup_die "Unsupported platform"
  nodeup_validate_supported_platform "$os" "$arch" || nodeup_die "Unsupported platform: ${os}-${arch}"

  detected_manager=""
  if [ "$os" = "linux" ]; then
    detected_manager="$(nodeup_detect_linux_manager || true)"
  fi

  detect_target_method "$os" "$detected_manager"
  resolve_release

  case "$TARGET_METHOD" in
    homebrew)
      install_with_homebrew
      ;;
    package)
      install_with_package "$os" "$arch" "$detected_manager"
      ;;
    binary)
      install_with_binary "$os" "$arch"
      ;;
    *)
      nodeup_die "Unsupported install method: ${TARGET_METHOD}"
      ;;
  esac

  nodeup_info "Install complete (method=${TARGET_METHOD}, version=${RELEASE_VERSION})"
}

main "$@"
