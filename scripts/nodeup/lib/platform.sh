#!/usr/bin/env bash

if [ "${NODEUP_PLATFORM_SH_LOADED:-0}" = "1" ]; then
  return 0
fi
NODEUP_PLATFORM_SH_LOADED=1

nodeup_uname_s() {
  if [ -n "${NODEUP_TEST_UNAME_S:-}" ]; then
    printf '%s\n' "$NODEUP_TEST_UNAME_S"
    return
  fi

  uname -s
}

nodeup_uname_m() {
  if [ -n "${NODEUP_TEST_UNAME_M:-}" ]; then
    printf '%s\n' "$NODEUP_TEST_UNAME_M"
    return
  fi

  uname -m
}

nodeup_detect_os() {
  case "$(nodeup_uname_s)" in
    Darwin)
      printf '%s\n' "darwin"
      ;;
    Linux)
      printf '%s\n' "linux"
      ;;
    *)
      return 1
      ;;
  esac
}

nodeup_detect_arch() {
  case "$(nodeup_uname_m)" in
    x86_64|amd64)
      printf '%s\n' "x64"
      ;;
    arm64|aarch64)
      printf '%s\n' "arm64"
      ;;
    *)
      return 1
      ;;
  esac
}

nodeup_detect_platform() {
  local os
  local arch

  os="$(nodeup_detect_os)" || return 1
  arch="$(nodeup_detect_arch)" || return 1

  printf '%s %s\n' "$os" "$arch"
}

nodeup_validate_supported_platform() {
  local os="$1"
  local arch="$2"

  case "$os" in
    linux|darwin) ;;
    *)
      return 1
      ;;
  esac

  case "$arch" in
    x64|arm64) ;;
    *)
      return 1
      ;;
  esac

  return 0
}
