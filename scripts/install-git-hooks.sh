#!/usr/bin/env sh
set -eu

repo_root="$(git rev-parse --show-toplevel)"
git_common_dir="$(git -C "$repo_root" rev-parse --path-format=absolute --git-common-dir)"
configured_hooks_path="$(git -C "$repo_root" config --path --get core.hooksPath || true)"
default_hooks_path="$git_common_dir/hooks"

if [ "$configured_hooks_path" = "$default_hooks_path" ]; then
  # Lefthook rejects any configured core.hooksPath by default, including an
  # absolute path to Git's own shared hooks directory. This case is common in
  # linked worktrees. --force is intentionally limited to that exact default
  # directory; remove this branch when Lefthook accepts it without an override.
  echo "[prepare] event=install_git_hooks mode=configured_default path=$default_hooks_path"
  exec lefthook install --force
fi

echo "[prepare] event=install_git_hooks mode=standard"
exec lefthook install
