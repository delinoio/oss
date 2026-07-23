#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
BASELINE="${REPO_ROOT}/protos/delibase/delibase.v1.binpb"
SNAPSHOT_DIR=""
# shellcheck source=./lib/go-proto-tools.sh
source "${REPO_ROOT}/scripts/lib/go-proto-tools.sh"

main() {
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
	"${SCRIPT_DIR}/generate-delibase-proto.sh"
	diff -ru "${SNAPSHOT_DIR}/go" "${REPO_ROOT}/protos/delibase/gen/go"
	diff -ru "${SNAPSHOT_DIR}/ts" "${REPO_ROOT}/protos/delibase/gen/ts"

	if [ -n "$(git -C "${REPO_ROOT}" status --porcelain --untracked-files=all -- protos/delibase/gen)" ]; then
		printf 'generated delibase artifacts differ from the checked-in files\n' >&2
		git -C "${REPO_ROOT}" status --short --untracked-files=all -- protos/delibase/gen >&2
		exit 1
	fi
}

main "$@"
