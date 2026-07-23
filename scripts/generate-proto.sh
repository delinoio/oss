#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

"${SCRIPT_DIR}/generate-go-proto.sh"
"${SCRIPT_DIR}/generate-delibase-proto.sh"
