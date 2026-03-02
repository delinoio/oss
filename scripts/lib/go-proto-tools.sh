#!/usr/bin/env bash

set -euo pipefail

BUF_VERSION="v1.65.0"

go_proto_log() {
	local scope="$1"
	local message="$2"
	printf '[%s] %s\n' "${scope}" "${message}"
}

go_proto_resolve_module_version() {
	local repo_root="$1"
	local module_name="$2"
	local version

	version="$(
		cd "${repo_root}"
		go list -m -f '{{.Version}}' "${module_name}" | tr -d '\r'
	)"
	if [ -z "${version}" ]; then
		echo "failed to resolve version for ${module_name}" >&2
		exit 1
	fi

	printf '%s' "${version}"
}

go_proto_install_tools() {
	local repo_root="$1"
	local scope="$2"
	local tool_bin="${repo_root}/.cache/proto-tools/bin"
	local buf_bin="${tool_bin}/buf"
	local protoc_gen_go_bin="${tool_bin}/protoc-gen-go"
	local protoc_gen_connect_go_bin="${tool_bin}/protoc-gen-connect-go"
	local connect_version
	local protobuf_version

	if [ -x "${buf_bin}" ] || [ -x "${buf_bin}.exe" ]; then
		if [ -x "${protoc_gen_go_bin}" ] || [ -x "${protoc_gen_go_bin}.exe" ]; then
			if [ -x "${protoc_gen_connect_go_bin}" ] || [ -x "${protoc_gen_connect_go_bin}.exe" ]; then
				go_proto_log "${scope}" "using cached tools from ${tool_bin}"
				export GO_PROTO_TOOL_BIN="${tool_bin}"
				export GO_PROTO_TOOLS_READY=1
				export PATH="${tool_bin}:${PATH}"
				return
			fi
		fi
	fi

	if [ "${GO_PROTO_TOOLS_READY:-0}" = "1" ] && [ -d "${tool_bin}" ]; then
		export GO_PROTO_TOOL_BIN="${tool_bin}"
		export PATH="${tool_bin}:${PATH}"
		return
	fi

	connect_version="$(go_proto_resolve_module_version "${repo_root}" "connectrpc.com/connect")"
	protobuf_version="$(go_proto_resolve_module_version "${repo_root}" "google.golang.org/protobuf")"

	go_proto_log "${scope}" "installing tools into ${tool_bin}"
	mkdir -p "${tool_bin}"

	GOBIN="${tool_bin}" go install "github.com/bufbuild/buf/cmd/buf@${BUF_VERSION}"
	GOBIN="${tool_bin}" go install "google.golang.org/protobuf/cmd/protoc-gen-go@${protobuf_version}"
	GOBIN="${tool_bin}" go install "connectrpc.com/connect/cmd/protoc-gen-connect-go@${connect_version}"

	export GO_PROTO_TOOL_BIN="${tool_bin}"
	export GO_PROTO_TOOLS_READY=1
	export PATH="${tool_bin}:${PATH}"
}
