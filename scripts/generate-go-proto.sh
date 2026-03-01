#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"

BUF_VERSION="v1.65.0"
TOOL_BIN="${REPO_ROOT}/.cache/proto-tools/bin"

log() {
	printf '[generate-go-proto] %s\n' "$1"
}

resolve_module_version() {
	local module_name="$1"
	local version

	version="$(go list -m -f '{{.Version}}' "${module_name}" | tr -d '\r')"
	if [ -z "${version}" ]; then
		echo "failed to resolve version for ${module_name}" >&2
		exit 1
	fi

	printf '%s' "${version}"
}

install_tools() {
	local connect_version
	local protobuf_version

	connect_version="$(resolve_module_version "connectrpc.com/connect")"
	protobuf_version="$(resolve_module_version "google.golang.org/protobuf")"

	log "installing tools into ${TOOL_BIN}"
	mkdir -p "${TOOL_BIN}"

	GOBIN="${TOOL_BIN}" go install "github.com/bufbuild/buf/cmd/buf@${BUF_VERSION}"
	GOBIN="${TOOL_BIN}" go install "google.golang.org/protobuf/cmd/protoc-gen-go@${protobuf_version}"
	GOBIN="${TOOL_BIN}" go install "connectrpc.com/connect/cmd/protoc-gen-connect-go@${connect_version}"

	export PATH="${TOOL_BIN}:${PATH}"
}

generate_service() {
	local service_dir="$1"

	log "running buf generate in ${service_dir}"
	(
		cd "${service_dir}"
		buf generate
	)
}

main() {
	log "starting protobuf Go generation"
	install_tools
	generate_service "${REPO_ROOT}/servers/thenv"
	generate_service "${REPO_ROOT}/servers/commit-tracker"
	log "protobuf Go generation completed"
}

main "$@"
