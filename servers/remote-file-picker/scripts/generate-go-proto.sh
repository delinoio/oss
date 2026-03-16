#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd -- "${SERVICE_ROOT}/../.." && pwd)"
# shellcheck source=../../../scripts/lib/go-proto-tools.sh
source "${REPO_ROOT}/scripts/lib/go-proto-tools.sh"

log() {
	printf '[generate-go-proto:remote-file-picker] %s\n' "$1"
}

main() {
	go_proto_install_tools "${REPO_ROOT}" "generate-go-proto:remote-file-picker"
	log "running buf generate in ${SERVICE_ROOT}"

	(
		cd "${SERVICE_ROOT}"
		buf generate
	)

	log "protobuf Go generation completed"
}

main "$@"
