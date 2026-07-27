#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
SNAPSHOT_DIR=""
# shellcheck source=./lib/go-proto-tools.sh
source "${REPO_ROOT}/scripts/lib/go-proto-tools.sh"

usage() {
	printf 'usage: %s <component> <descriptor-file>\n' "${0##*/}" >&2
	exit 2
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
	DESCRIPTOR="${COMPONENT_ROOT}/${DESCRIPTOR_FILE}"
	BASELINE_VARIABLE="$(printf '%s_PROTO_BASELINE' "${COMPONENT}" | tr '[:lower:]-' '[:upper:]_')"
	BASELINE="${!BASELINE_VARIABLE:-${DESCRIPTOR}}"
}

copy_generated_snapshot() {
	mkdir -p "${SNAPSHOT_DIR}/go" "${SNAPSHOT_DIR}/ts"
	cp -R "${COMPONENT_ROOT}/gen/go/." "${SNAPSHOT_DIR}/go/"
	cp -R "${COMPONENT_ROOT}/gen/ts/." "${SNAPSHOT_DIR}/ts/"
	cp "${DESCRIPTOR}" "${SNAPSHOT_DIR}/${DESCRIPTOR_FILE}"
}

main() {
	validate_arguments "$@"
	if [ ! -f "${BASELINE}" ]; then
		printf '%s Protobuf baseline does not exist: %s\n' "${COMPONENT}" "${BASELINE}" >&2
		exit 1
	fi

	go_proto_install_tools "${REPO_ROOT}" "check-proto:${COMPONENT}"
	SNAPSHOT_DIR="$(mktemp -d)"
	trap 'rm -rf -- "${SNAPSHOT_DIR}"' EXIT

	(
		cd "${REPO_ROOT}"
		buf lint protos --path "${CONTRACT_PATH}"
		buf build protos \
			--path "${CONTRACT_PATH}" \
			--exclude-source-info \
			--output "${SNAPSHOT_DIR}/current.binpb"
		buf breaking "${SNAPSHOT_DIR}/current.binpb" --against "${BASELINE}"
	)

	"${SCRIPT_DIR}/generate-proto-component.sh" "${COMPONENT}" "${DESCRIPTOR_FILE}"
	copy_generated_snapshot
	"${SCRIPT_DIR}/generate-proto-component.sh" "${COMPONENT}" "${DESCRIPTOR_FILE}"
	diff -ru "${SNAPSHOT_DIR}/go" "${COMPONENT_ROOT}/gen/go"
	diff -ru "${SNAPSHOT_DIR}/ts" "${COMPONENT_ROOT}/gen/ts"
	cmp "${SNAPSHOT_DIR}/${DESCRIPTOR_FILE}" "${DESCRIPTOR}"

	if [ -n "$(git -C "${REPO_ROOT}" status --porcelain --untracked-files=all -- \
		"${COMPONENT_ROOT}/gen" "${DESCRIPTOR}")" ]; then
		printf 'generated %s artifacts or descriptor differ from the checked-in files\n' \
			"${COMPONENT}" >&2
		git -C "${REPO_ROOT}" status --short --untracked-files=all -- \
			"${COMPONENT_ROOT}/gen" "${DESCRIPTOR}" >&2
		exit 1
	fi
}

main "$@"
