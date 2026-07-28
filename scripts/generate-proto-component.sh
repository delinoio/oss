#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
# shellcheck source=./lib/go-proto-tools.sh
source "${REPO_ROOT}/scripts/lib/go-proto-tools.sh"

usage() {
	printf 'usage: %s <component> <descriptor-file>\n' "${0##*/}" >&2
	exit 2
}

log() {
	printf '[generate-proto:%s] %s\n' "${COMPONENT}" "$1"
}

validate_arguments() {
	if [ "$#" -ne 2 ]; then
		usage
	fi

	COMPONENT="$1"
	DESCRIPTOR_FILE="$2"
	if [[ ! "${COMPONENT}" =~ ^[a-z0-9]+([a-z0-9-]*[a-z0-9])?$ ]]; then
		printf 'invalid Protobuf component name: %s\n' "${COMPONENT}" >&2
		exit 2
	fi
	if [[ ! "${DESCRIPTOR_FILE}" =~ ^[a-z0-9][a-z0-9.-]*\.binpb$ ]]; then
		printf 'invalid Protobuf descriptor filename: %s\n' "${DESCRIPTOR_FILE}" >&2
		exit 2
	fi

	COMPONENT_ROOT="${REPO_ROOT}/protos/${COMPONENT}"
	CONTRACT_PATH="protos/${COMPONENT}/v1"
	GO_OUT="${COMPONENT_ROOT}/gen/go"
	TS_OUT="${COMPONENT_ROOT}/gen/ts"
	DESCRIPTOR_OUT="${COMPONENT_ROOT}/${DESCRIPTOR_FILE}"
	GEN_TEMPLATE="${COMPONENT_ROOT}/buf.gen.yaml"
	TS_TOOL_BIN="${COMPONENT_ROOT}/node_modules/.bin"

	if [ ! -d "${COMPONENT_ROOT}" ] || [ ! -d "${REPO_ROOT}/${CONTRACT_PATH}" ]; then
		printf 'Protobuf component source does not exist: %s\n' "${CONTRACT_PATH}" >&2
		exit 1
	fi
	if [ ! -f "${GEN_TEMPLATE}" ]; then
		printf 'component generation template does not exist: %s\n' "${GEN_TEMPLATE}" >&2
		exit 1
	fi
}

main() {
	validate_arguments "$@"
	go_proto_install_tools "${REPO_ROOT}" "generate-proto:${COMPONENT}"

	if [ ! -x "${TS_TOOL_BIN}/protoc-gen-es" ]; then
		printf 'TypeScript protobuf plugin is missing for %s; run pnpm install at %s\n' \
			"${COMPONENT}" "${REPO_ROOT}" >&2
		exit 1
	fi

	export PATH="${TS_TOOL_BIN}:${PATH}"
	log "generating scoped Go, Connect, and TypeScript artifacts"
	(
		cd "${REPO_ROOT}"
		buf generate protos \
			--template "${GEN_TEMPLATE}" \
			--path "${CONTRACT_PATH}"
		buf build protos \
			--path "${CONTRACT_PATH}" \
			--exclude-source-info \
			--output "${DESCRIPTOR_OUT}"
	)

	find "${GO_OUT}" -type f -name '*.go' -exec gofmt -w {} +
	log "generation completed"
}

main "$@"
