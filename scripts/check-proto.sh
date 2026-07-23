#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
DESCRIPTOR="${REPO_ROOT}/protos/delibase/delibase.v1.binpb"
BASELINE="${DELIBASE_PROTO_BASELINE:-${DESCRIPTOR}}"
SNAPSHOT_DIR=""
# shellcheck source=./lib/go-proto-tools.sh
source "${REPO_ROOT}/scripts/lib/go-proto-tools.sh"

main() {
	if [ ! -f "${BASELINE}" ]; then
		printf 'delibase Protobuf baseline does not exist: %s\n' "${BASELINE}" >&2
		exit 1
	fi

	go_proto_install_tools "${REPO_ROOT}" "check-proto"
	SNAPSHOT_DIR="$(mktemp -d)"
	trap 'rm -rf -- "${SNAPSHOT_DIR}"' EXIT

	(
		cd "${REPO_ROOT}"
		buf lint protos
		buf breaking protos --against "${BASELINE}"
	)

	"${SCRIPT_DIR}/generate-delibase-proto.sh"
	cp -R "${REPO_ROOT}/protos/delibase/gen/go" "${SNAPSHOT_DIR}/go"
	cp -R "${REPO_ROOT}/protos/delibase/gen/ts" "${SNAPSHOT_DIR}/ts"
	cp "${DESCRIPTOR}" "${SNAPSHOT_DIR}/delibase.v1.binpb"
	"${SCRIPT_DIR}/generate-delibase-proto.sh"
	diff -ru "${SNAPSHOT_DIR}/go" "${REPO_ROOT}/protos/delibase/gen/go"
	diff -ru "${SNAPSHOT_DIR}/ts" "${REPO_ROOT}/protos/delibase/gen/ts"
	cmp "${SNAPSHOT_DIR}/delibase.v1.binpb" "${DESCRIPTOR}"

	if [ -n "$(git -C "${REPO_ROOT}" status --porcelain --untracked-files=all -- protos/delibase/gen protos/delibase/delibase.v1.binpb)" ]; then
		printf 'generated delibase artifacts or descriptor differ from the checked-in files\n' >&2
		git -C "${REPO_ROOT}" status --short --untracked-files=all -- protos/delibase/gen protos/delibase/delibase.v1.binpb >&2
		exit 1
	fi
}

main "$@"
