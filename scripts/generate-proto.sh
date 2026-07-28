#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
PROTO_COMPONENTS=(
	"delibase:released"
	"devhud-deck:planned"
	"devhud-realqa:planned"
)

"${SCRIPT_DIR}/generate-go-proto.sh"

for component_contract in "${PROTO_COMPONENTS[@]}"; do
	IFS=: read -r component lifecycle <<<"${component_contract}"
	component_root="${REPO_ROOT}/protos/${component}"
	if [ ! -f "${component_root}/package.json" ]; then
		if [ "${lifecycle}" = "released" ]; then
			printf '[generate-proto] released component has no workspace package: %s\n' \
				"${component}" >&2
			exit 1
		fi
		if [ -d "${component_root}/v1" ]; then
			printf '[generate-proto] implemented component has no workspace package: %s\n' \
				"${component}" >&2
			exit 1
		fi
		continue
	fi

	printf '[generate-proto] generating %s\n' "${component}"
	pnpm --dir "${component_root}" run generate:proto
done
