#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
# shellcheck source=./lib/go-proto-tools.sh
source "${REPO_ROOT}/scripts/lib/go-proto-tools.sh"

log() {
	printf '[generate-go-proto] %s\n' "$1"
}

main() {
	log "starting protobuf Go generation via go generate"
	go_proto_install_tools "${REPO_ROOT}" "generate-go-proto"

	log "running go generate for server protobuf contracts"
	(
		cd "${REPO_ROOT}"
		go generate ./servers/thenv
		go generate ./servers/commit-tracker
		go generate ./servers/remote-file-picker
	)

	log "running buf generate for shared dexdex protobuf contracts"
	(
		cd "${REPO_ROOT}/protos/dexdex"
		buf generate
	)

	log "protobuf Go generation completed"
}

main "$@"
