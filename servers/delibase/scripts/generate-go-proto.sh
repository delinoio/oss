#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

# Delibase's canonical proto source is shared at protos/delibase/v1. Use the
# root generator so Go and TypeScript derived views cannot drift.
./scripts/generate-proto.sh
