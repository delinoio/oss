#!/usr/bin/env bash

if [ "${NODEUP_COMMON_SH_LOADED:-0}" = "1" ]; then
  return 0
fi
NODEUP_COMMON_SH_LOADED=1

NODEUP_PATH_BLOCK_START="# >>> nodeup path >>>"
NODEUP_PATH_BLOCK_END="# <<< nodeup path <<<"

nodeup_log() {
  local level="$1"
  shift
  printf '[nodeup:%s] %s\n' "$level" "$*"
}

nodeup_debug() {
  if [ "${NODEUP_DEBUG:-0}" = "1" ]; then
    nodeup_log "debug" "$*"
  fi
}

nodeup_info() {
  nodeup_log "info" "$*"
}

nodeup_warn() {
  nodeup_log "warn" "$*" >&2
}

nodeup_error() {
  nodeup_log "error" "$*" >&2
}

nodeup_die() {
  nodeup_error "$*"
  exit 1
}

nodeup_command_exists() {
  command -v "$1" >/dev/null 2>&1
}

nodeup_expand_home() {
  local value="$1"
  case "$value" in
    "~")
      printf '%s\n' "$HOME"
      ;;
    "~/"*)
      printf '%s\n' "$HOME/${value#~/}"
      ;;
    *)
      printf '%s\n' "$value"
      ;;
  esac
}

nodeup_has_root_privilege() {
  [ "$(id -u)" -eq 0 ]
}

nodeup_can_use_sudo() {
  if ! nodeup_command_exists sudo; then
    return 1
  fi

  if sudo -n true >/dev/null 2>&1; then
    return 0
  fi

  if [ -t 0 ]; then
    return 0
  fi

  return 1
}

nodeup_can_manage_system_packages() {
  if nodeup_has_root_privilege; then
    return 0
  fi

  nodeup_can_use_sudo
}

nodeup_set_sudo_mode() {
  if nodeup_has_root_privilege; then
    NODEUP_USE_SUDO=0
    export NODEUP_USE_SUDO
    return 0
  fi

  if nodeup_can_use_sudo; then
    NODEUP_USE_SUDO=1
    export NODEUP_USE_SUDO
    return 0
  fi

  return 1
}

nodeup_run_cmd() {
  if [ "${NODEUP_DRY_RUN:-0}" = "1" ]; then
    nodeup_log "dry-run" "$*"
    return 0
  fi

  nodeup_debug "running: $*"
  "$@"
}

nodeup_run_with_optional_sudo() {
  if [ "${NODEUP_USE_SUDO:-0}" = "1" ]; then
    nodeup_run_cmd sudo "$@"
    return
  fi

  nodeup_run_cmd "$@"
}

nodeup_default_profile_file() {
  local shell_name
  shell_name="$(basename "${SHELL:-}")"

  case "$shell_name" in
    zsh)
      printf '%s\n' "$HOME/.zshrc"
      ;;
    bash)
      printf '%s\n' "$HOME/.bashrc"
      ;;
    *)
      if [ -f "$HOME/.profile" ]; then
        printf '%s\n' "$HOME/.profile"
      else
        printf '%s\n' "$HOME/.bashrc"
      fi
      ;;
  esac
}

nodeup_escape_double_quotes() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '%s\n' "$value"
}

nodeup_append_path_block() {
  local profile_file="$1"
  local path_entry="$2"
  local escaped_path

  escaped_path="$(nodeup_escape_double_quotes "$path_entry")"

  if [ "${NODEUP_DRY_RUN:-0}" = "1" ]; then
    nodeup_log "dry-run" "append PATH block to ${profile_file}"
    return 0
  fi

  mkdir -p "$(dirname "$profile_file")"
  touch "$profile_file"

  if grep -Fq "$NODEUP_PATH_BLOCK_START" "$profile_file"; then
    nodeup_debug "PATH block already exists in ${profile_file}"
    return 0
  fi

  {
    printf '\n%s\n' "$NODEUP_PATH_BLOCK_START"
    printf 'export PATH="%s:$PATH"\n' "$escaped_path"
    printf '%s\n' "$NODEUP_PATH_BLOCK_END"
  } >>"$profile_file"
}

nodeup_remove_path_block() {
  local profile_file="$1"

  if [ ! -f "$profile_file" ]; then
    return 0
  fi

  if [ "${NODEUP_DRY_RUN:-0}" = "1" ]; then
    nodeup_log "dry-run" "remove PATH block from ${profile_file}"
    return 0
  fi

  local tmp_file
  tmp_file="$(mktemp)"

  awk -v start="$NODEUP_PATH_BLOCK_START" -v end="$NODEUP_PATH_BLOCK_END" '
    $0 == start { skip = 1; next }
    $0 == end { skip = 0; next }
    skip == 0 { print }
  ' "$profile_file" >"$tmp_file"

  mv "$tmp_file" "$profile_file"
}

nodeup_state_file() {
  local default_path
  default_path="$HOME/.config/nodeup/installer-state.env"
  printf '%s\n' "${NODEUP_INSTALL_STATE_FILE:-$default_path}"
}

nodeup_write_state() {
  local method="$1"
  local manager="$2"
  local prefix="$3"
  local profile="$4"
  local path_update="$5"
  local version="$6"
  local tag="$7"

  local state_file
  state_file="$(nodeup_state_file)"

  if [ "${NODEUP_DRY_RUN:-0}" = "1" ]; then
    nodeup_log "dry-run" "write installer state to ${state_file}"
    return 0
  fi

  mkdir -p "$(dirname "$state_file")"

  cat >"$state_file" <<STATE
NODEUP_INSTALL_METHOD="${method}"
NODEUP_INSTALL_MANAGER="${manager}"
NODEUP_INSTALL_PREFIX="${prefix}"
NODEUP_INSTALL_PROFILE="${profile}"
NODEUP_INSTALL_PATH_UPDATE="${path_update}"
NODEUP_INSTALL_VERSION="${version}"
NODEUP_INSTALL_TAG="${tag}"
STATE
}

nodeup_load_state() {
  local state_file
  state_file="$(nodeup_state_file)"

  if [ ! -f "$state_file" ]; then
    return 1
  fi

  # shellcheck disable=SC1090
  source "$state_file"
  return 0
}

nodeup_remove_state() {
  local state_file
  state_file="$(nodeup_state_file)"

  if [ ! -f "$state_file" ]; then
    return 0
  fi

  nodeup_run_cmd rm -f "$state_file"
}

nodeup_confirm() {
  local prompt="$1"
  local answer

  if [ "${NODEUP_YES:-0}" = "1" ]; then
    return 0
  fi

  if [ ! -t 0 ]; then
    return 1
  fi

  printf '%s [y/N]: ' "$prompt"
  read -r answer

  case "$answer" in
    y|Y|yes|YES)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}
