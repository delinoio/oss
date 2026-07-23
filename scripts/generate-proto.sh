#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"

"${SCRIPT_DIR}/generate-go-proto.sh"
"${SCRIPT_DIR}/generate-delibase-proto.sh"

(
	cd "${REPO_ROOT}"
	pnpm --filter @delinoio/delibase-connect build
)
