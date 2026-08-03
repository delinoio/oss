#!/usr/bin/env sh
set -eu

repo_root="$(git rev-parse --show-toplevel)"
git_common_dir="$(git rev-parse --path-format=absolute --git-common-dir)"
configured_hooks_path="$(git config --get core.hooksPath || true)"
shared_hooks_path="$git_common_dir/hooks"

if [ -z "$configured_hooks_path" ]; then
	exec lefthook install
fi

case "$configured_hooks_path" in
	/*|[A-Za-z]:/*) absolute_hooks_path="$configured_hooks_path" ;;
	*) absolute_hooks_path="$repo_root/$configured_hooks_path" ;;
esac

if [ "$absolute_hooks_path" = "$shared_hooks_path" ]; then
	# Lefthook rejects an existing core.hooksPath in linked worktrees even when it
	# already targets Git's shared hooks directory. Force is safe only for this
	# exact repository-owned path; remove this branch when Lefthook handles it.
	exec lefthook install --force
fi

# Preserve Lefthook's protective failure for unrelated custom hook paths.
exec lefthook install
