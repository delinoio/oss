#!/usr/bin/env bash

set -eu

# Use first argument as app name if provided.
app_name="${1:-$(basename $(pwd))}"

echo "Setting env vars for $app_name"

TOKEN_ARG=""
if [ -n "${VERCEL_TOKEN:-}" ]; then
    TOKEN_ARG="--token $VERCEL_TOKEN"
fi

vercel link --scope delino --yes $TOKEN_ARG -p $app_name > /dev/null || true
vercel env pull .env --yes $TOKEN_ARG > /dev/null