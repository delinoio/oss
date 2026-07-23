#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
GO_OUT="${REPO_ROOT}/protos/delibase/gen/go"
TS_OUT="${REPO_ROOT}/protos/delibase/gen/ts"
TS_TOOL_BIN="${REPO_ROOT}/protos/delibase/node_modules/.bin"
# shellcheck source=./lib/go-proto-tools.sh
source "${REPO_ROOT}/scripts/lib/go-proto-tools.sh"

log() {
	printf '[generate-delibase-proto] %s\n' "$1"
}

main() {
	go_proto_install_tools "${REPO_ROOT}" "generate-delibase-proto"

	if [ ! -x "${TS_TOOL_BIN}/protoc-gen-es" ]; then
		printf 'TypeScript protobuf plugins are missing; run pnpm install at %s\n' "${REPO_ROOT}" >&2
		exit 1
	fi

	export PATH="${TS_TOOL_BIN}:${PATH}"
	mkdir -p "${GO_OUT}" "${TS_OUT}"
	find "${GO_OUT}" -type f -delete
	find "${TS_OUT}" -type f -delete

	log "generating Go and TypeScript Connect artifacts from protos/delibase/v1"
	(
		cd "${REPO_ROOT}"
		buf generate --template protos/buf.gen.yaml
	)

	find "${GO_OUT}" -type f -name '*.go' -exec gofmt -w {} +
	log "generation completed"
}

main "$@"
