#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd -- "${SERVICE_ROOT}/../.." && pwd)"

log() {
  printf '[generate-go-proto:dexdex-main-server] %s\n' "$1"
}

main() {
  log "running shared dexdex proto generation"
  (
    cd "${REPO_ROOT}/protos/dexdex"
    buf generate
  )
  log "protobuf Go generation completed"
}

main "$@"
