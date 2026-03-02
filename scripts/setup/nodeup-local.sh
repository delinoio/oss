#!/usr/bin/env sh

set -eu

if [ "${1:-}" = "--help" ]; then
  cat >&2 <<'EOF'
Install nodeup from the local workspace and print shell exports for the current session.

Usage:
  eval "$(./scripts/setup/nodeup-local.sh)"

Optional environment variables:
  NODEUP_LOCAL_INSTALL_ROOT  Install root (default: <repo>/.local/nodeup)
EOF
  exit 0
fi

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repo_root="$(git -C "$script_dir/../.." rev-parse --show-toplevel)"
crate_dir="$repo_root/crates/nodeup"
install_root="${NODEUP_LOCAL_INSTALL_ROOT:-$repo_root/.local/nodeup}"
install_bin_dir="$install_root/bin"

echo "[nodeup-local] installing with cargo install --path . --root \"$install_root\"" >&2
(
  cd "$crate_dir"
  cargo install --path . --root "$install_root"
) >&2

echo "[nodeup-local] printing shell exports for current session patch" >&2
printf '_nodeup_local_bin="%s"\n' "$install_bin_dir"
printf 'case ":$PATH:" in *":${_nodeup_local_bin}:"*) ;; *) export PATH="${_nodeup_local_bin}:$PATH" ;; esac\n'
printf 'export NODEUP_SELF_BIN_PATH="%s/nodeup"\n' "$install_bin_dir"
printf 'hash -r 2>/dev/null || true\n'
printf 'unset _nodeup_local_bin\n'
