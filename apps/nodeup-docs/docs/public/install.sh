#!/usr/bin/env bash

set -euo pipefail

installer_url="https://raw.githubusercontent.com/delinoio/oss/refs/heads/main/scripts/install/nodeup.sh"
tmp_dir="$(mktemp -d)"

cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

if ! curl -fsSL "$installer_url" -o "$tmp_dir/nodeup.sh"; then
  echo "[install.nodeup] failed to download canonical installer: $installer_url" >&2
  exit 1
fi

bash "$tmp_dir/nodeup.sh" "$@"
