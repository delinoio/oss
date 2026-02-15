#!/usr/bin/env bash

set -eu

# early-exit on CI or VERCEL
if [ -n "${CI+x}" ] || [ -n "${VERCEL+x}" ]; then
	exit 0
fi

pnpm run --parallel prepare:app