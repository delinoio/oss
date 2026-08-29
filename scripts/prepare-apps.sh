#!/usr/bin/env bash

set -eu

# early-exit on CI
if [ -n "${CI+x}" ]; then
	exit 0
fi

pnpm run --parallel prepare:app
