#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
INSTALLER_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
UNINSTALL_SCRIPT="${INSTALLER_DIR}/uninstall.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  local haystack="$1"
  local needle="$2"
  local message="$3"

  if [[ "$haystack" != *"$needle"* ]]; then
    fail "${message} (missing '${needle}')"
  fi
}

assert_not_contains() {
  local haystack="$1"
  local needle="$2"
  local message="$3"

  if [[ "$haystack" == *"$needle"* ]]; then
    fail "${message} (found '${needle}')"
  fi
}

make_fake_nodeup() {
  local bin_dir="$1"

  cat >"${bin_dir}/nodeup" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf 'fake nodeup %s\n' "$*"
FAKE

  chmod +x "${bin_dir}/nodeup"
}

test_default_uninstall_purges_data() {
  local temp_dir
  local fake_bin
  local output

  temp_dir="$(mktemp -d)"
  fake_bin="${temp_dir}/bin"
  mkdir -p "$fake_bin"

  make_fake_nodeup "$fake_bin"

  output="$(HOME="$temp_dir" PATH="${fake_bin}:$PATH" bash "$UNINSTALL_SCRIPT" --method binary --dry-run --yes 2>&1)"
  assert_contains "$output" "nodeup self uninstall" "default uninstall should call self uninstall"

  rm -rf "$temp_dir"
}

test_keep_data_skips_self_uninstall() {
  local temp_dir
  local fake_bin
  local output

  temp_dir="$(mktemp -d)"
  fake_bin="${temp_dir}/bin"
  mkdir -p "$fake_bin"

  make_fake_nodeup "$fake_bin"

  output="$(HOME="$temp_dir" PATH="${fake_bin}:$PATH" bash "$UNINSTALL_SCRIPT" --method binary --dry-run --yes --keep-data 2>&1)"
  assert_not_contains "$output" "nodeup self uninstall" "--keep-data should skip self uninstall"

  rm -rf "$temp_dir"
}

run_tests() {
  test_default_uninstall_purges_data
  test_keep_data_skips_self_uninstall
  echo "PASS: uninstall_test.sh"
}

run_tests
