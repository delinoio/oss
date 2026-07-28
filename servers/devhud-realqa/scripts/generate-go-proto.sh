#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

# RealQA's canonical cross-runtime source is owned by protos/devhud-realqa.
pnpm --dir protos/devhud-realqa generate:proto
