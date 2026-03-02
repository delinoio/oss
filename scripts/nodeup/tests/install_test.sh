#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib/common.sh
source "${SCRIPT_DIR}/../lib/common.sh"
# shellcheck source=../lib/platform.sh
source "${SCRIPT_DIR}/../lib/platform.sh"
# shellcheck source=../lib/manager.sh
source "${SCRIPT_DIR}/../lib/manager.sh"
# shellcheck source=../lib/download.sh
source "${SCRIPT_DIR}/../lib/download.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_eq() {
  local expected="$1"
  local actual="$2"
  local message="$3"

  if [ "$expected" != "$actual" ]; then
    fail "${message} (expected='${expected}', actual='${actual}')"
  fi
}

assert_contains() {
  local haystack="$1"
  local needle="$2"
  local message="$3"

  if [[ "$haystack" != *"$needle"* ]]; then
    fail "${message} (missing '${needle}')"
  fi
}

test_detect_platform_mapping() {
  NODEUP_TEST_UNAME_S="Darwin"
  NODEUP_TEST_UNAME_M="x86_64"
  read -r os arch <<<"$(nodeup_detect_platform)"
  assert_eq "darwin" "$os" "darwin os mapping"
  assert_eq "x64" "$arch" "x64 arch mapping"

  NODEUP_TEST_UNAME_S="Linux"
  NODEUP_TEST_UNAME_M="aarch64"
  read -r os arch <<<"$(nodeup_detect_platform)"
  assert_eq "linux" "$os" "linux os mapping"
  assert_eq "arm64" "$arch" "arm64 arch mapping"

  unset NODEUP_TEST_UNAME_S
  unset NODEUP_TEST_UNAME_M
}

test_auto_method_selection() {
  local method

  method="$(nodeup_select_install_method darwin 1 "" 0)"
  assert_eq "homebrew" "$method" "darwin auto method with brew"

  method="$(nodeup_select_install_method darwin 0 "" 0)"
  assert_eq "binary" "$method" "darwin auto method without brew"

  method="$(nodeup_select_install_method linux 0 apt 1)"
  assert_eq "package" "$method" "linux auto method with manager and privilege"

  method="$(nodeup_select_install_method linux 0 apt 0)"
  assert_eq "binary" "$method" "linux auto method without privilege"
}

test_manager_command_generation() {
  local preview

  preview="$(nodeup_manager_install_preview apt /tmp/nodeup.deb)"
  assert_eq "apt-get install -y /tmp/nodeup.deb" "$preview" "apt install preview"

  preview="$(nodeup_manager_install_preview pacman /tmp/nodeup.pkg.tar.zst)"
  assert_eq "pacman -U --noconfirm /tmp/nodeup.pkg.tar.zst" "$preview" "pacman install preview"

  preview="$(nodeup_manager_uninstall_preview dnf)"
  assert_eq "dnf remove -y nodeup" "$preview" "dnf uninstall preview"
}

test_checksum_mismatch_detection() {
  local temp_dir
  local artifact
  local checksum_file

  temp_dir="$(mktemp -d)"
  artifact="${temp_dir}/nodeup-v0.0.0-linux-x64.tar.gz"
  checksum_file="${temp_dir}/nodeup-v0.0.0-checksums.txt"

  printf 'archive-bytes' >"$artifact"
  printf 'deadbeef  %s\n' "$(basename "$artifact")" >"$checksum_file"

  if nodeup_verify_checksum "$checksum_file" "$artifact"; then
    rm -rf "$temp_dir"
    fail "checksum mismatch should fail"
  fi

  rm -rf "$temp_dir"
}

test_path_block_idempotency() {
  local temp_dir
  local profile
  local count

  temp_dir="$(mktemp -d)"
  profile="${temp_dir}/.bashrc"

  NODEUP_DRY_RUN=0
  nodeup_append_path_block "$profile" "/tmp/nodeup/bin"
  nodeup_append_path_block "$profile" "/tmp/nodeup/bin"

  count="$(grep -Fc "$NODEUP_PATH_BLOCK_START" "$profile")"
  assert_eq "1" "$count" "PATH block should be idempotent"

  nodeup_remove_path_block "$profile"
  if grep -Fq "$NODEUP_PATH_BLOCK_START" "$profile"; then
    rm -rf "$temp_dir"
    fail "PATH block should be removed"
  fi

  rm -rf "$temp_dir"
}

run_tests() {
  test_detect_platform_mapping
  test_auto_method_selection
  test_manager_command_generation
  test_checksum_mismatch_detection
  test_path_block_idempotency
  echo "PASS: install_test.sh"
}

run_tests
