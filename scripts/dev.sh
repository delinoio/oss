#!/usr/bin/env sh
set -eu

repo_root="$(git rev-parse --show-toplevel)"

export DERUN_STATE_ROOT="$repo_root/.derun-state"
export GOMODCACHE="$repo_root/.gomodcache"
export GOCACHE="$repo_root/.gocache"
export GOPATH="$repo_root/.gopath"

if [ "${1-}" = "--" ]; then
  shift
fi

exec go -C "$repo_root" run ./cmds/derun run -- turbo dev "$@"
