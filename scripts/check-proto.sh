#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
PROTO_COMPONENTS=(
	"delibase"
	"devhud-deck"
	"devhud-realqa"
)

main() {
	for component in "${PROTO_COMPONENTS[@]}"; do
		component_root="${REPO_ROOT}/protos/${component}"
		if [ ! -f "${component_root}/package.json" ]; then
			if [ -d "${component_root}/v1" ]; then
				printf '[check-proto] implemented component has no workspace package: %s\n' \
					"${component}" >&2
				exit 1
			fi
			continue
		fi

		printf '[check-proto] checking %s\n' "${component}"
		pnpm --dir "${component_root}" run check:proto
	done
}

main "$@"
