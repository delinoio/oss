#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib/common.sh
source "${SCRIPT_DIR}/lib/common.sh"
# shellcheck source=./lib/platform.sh
source "${SCRIPT_DIR}/lib/platform.sh"
# shellcheck source=./lib/manager.sh
source "${SCRIPT_DIR}/lib/manager.sh"

NODEUP_METHOD="auto"
NODEUP_MANAGER=""
NODEUP_KEEP_DATA=0
NODEUP_YES=0
NODEUP_DRY_RUN=0
NODEUP_DEBUG=0

TARGET_METHOD=""
TARGET_MANAGER=""

print_usage() {
  cat <<'USAGE'
Usage: uninstall.sh [options]

Uninstall nodeup and, by default, purge nodeup runtime/config/cache data.

Options:
  --method <auto|homebrew|package|binary>
  --manager <apt|dnf|yum|pacman|zypper>
  --keep-data
  --yes
  --dry-run
  --debug
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
      --manager)
        [ "$#" -ge 2 ] || nodeup_die "Missing value for --manager"
        NODEUP_MANAGER="$2"
        shift 2
        ;;
      --keep-data)
        NODEUP_KEEP_DATA=1
        shift
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

detect_installed_package_manager() {
  local manager

  for manager in apt dnf yum pacman zypper; do
    if ! nodeup_is_supported_manager "$manager"; then
      continue
    fi

    if nodeup_is_package_installed "$manager"; then
      printf '%s\n' "$manager"
      return 0
    fi
  done

  return 1
}

resolve_method_from_state() {
  if ! nodeup_load_state; then
    return 1
  fi

  TARGET_METHOD="${NODEUP_INSTALL_METHOD:-}"
  if [ -n "$NODEUP_MANAGER" ]; then
    TARGET_MANAGER="$NODEUP_MANAGER"
  else
    TARGET_MANAGER="${NODEUP_INSTALL_MANAGER:-}"
  fi

  return 0
}

resolve_target_method() {
  local os="$1"

  if [ "$NODEUP_METHOD" != "auto" ]; then
    TARGET_METHOD="$NODEUP_METHOD"
    if [ -n "$NODEUP_MANAGER" ]; then
      TARGET_MANAGER="$NODEUP_MANAGER"
    fi
    return
  fi

  if resolve_method_from_state && [ -n "$TARGET_METHOD" ]; then
    return
  fi

  if [ "$os" = "darwin" ] && nodeup_command_exists brew; then
    if brew list --versions nodeup >/dev/null 2>&1; then
      TARGET_METHOD="homebrew"
      TARGET_MANAGER="homebrew"
      return
    fi
  fi

  if [ "$os" = "linux" ]; then
    local manager
    manager="$(detect_installed_package_manager || true)"
    if [ -n "$manager" ]; then
      TARGET_METHOD="package"
      TARGET_MANAGER="$manager"
      return
    fi
  fi

  TARGET_METHOD="binary"
}

run_data_purge() {
  if [ "$NODEUP_KEEP_DATA" = "1" ]; then
    nodeup_info "Skipping nodeup data purge (--keep-data)"
    return
  fi

  if ! nodeup_command_exists nodeup; then
    nodeup_warn "nodeup command not found; skipping 'nodeup self uninstall'"
    return
  fi

  if ! nodeup_run_cmd nodeup self uninstall; then
    nodeup_warn "'nodeup self uninstall' failed; continuing with binary/package removal"
  fi
}

uninstall_homebrew() {
  if ! nodeup_command_exists brew; then
    nodeup_die "Homebrew command not found"
  fi

  nodeup_info "Uninstalling nodeup with Homebrew"
  nodeup_run_cmd brew uninstall nodeup
}

uninstall_package() {
  local manager

  manager="$TARGET_MANAGER"
  if [ -z "$manager" ]; then
    manager="$NODEUP_MANAGER"
  fi

  if [ -z "$manager" ]; then
    manager="$(detect_installed_package_manager || true)"
  fi

  if [ -z "$manager" ]; then
    nodeup_die "Could not determine package manager for package uninstall"
  fi

  if ! nodeup_set_sudo_mode; then
    nodeup_die "Package uninstall requires root privilege or sudo access"
  fi

  nodeup_info "Uninstalling nodeup package with ${manager}"
  nodeup_uninstall_package_with_manager "$manager"
}

uninstall_binary() {
  local prefix
  local bin_dir

  prefix="$(nodeup_expand_home "${NODEUP_INSTALL_PREFIX:-~/.local}")"
  bin_dir="${prefix%/}/bin"

  nodeup_info "Removing binaries from ${bin_dir}"
  nodeup_run_cmd rm -f "${bin_dir}/nodeup"
  nodeup_run_cmd rm -f "${bin_dir}/node"
  nodeup_run_cmd rm -f "${bin_dir}/npm"
  nodeup_run_cmd rm -f "${bin_dir}/npx"
}

cleanup_path_blocks() {
  local profile
  local profile_from_state

  profile_from_state="${NODEUP_INSTALL_PROFILE:-}"

  if [ -n "$profile_from_state" ]; then
    nodeup_remove_path_block "$profile_from_state"
  fi

  for profile in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.profile"; do
    nodeup_remove_path_block "$profile"
  done
}

main() {
  parse_args "$@"

  export NODEUP_DRY_RUN
  export NODEUP_DEBUG
  export NODEUP_YES

  local os
  local arch

  read -r os arch <<<"$(nodeup_detect_platform)" || nodeup_die "Unsupported platform"
  nodeup_validate_supported_platform "$os" "$arch" || nodeup_die "Unsupported platform: ${os}-${arch}"

  resolve_target_method "$os"

  if nodeup_load_state; then
    nodeup_debug "Loaded installer state file"
  fi

  run_data_purge

  case "$TARGET_METHOD" in
    homebrew)
      uninstall_homebrew
      ;;
    package)
      uninstall_package
      ;;
    binary)
      uninstall_binary
      ;;
    *)
      nodeup_die "Unsupported uninstall method: ${TARGET_METHOD}"
      ;;
  esac

  cleanup_path_blocks
  nodeup_remove_state

  nodeup_info "Uninstall complete (method=${TARGET_METHOD}, keep_data=${NODEUP_KEEP_DATA})"
}

main "$@"
