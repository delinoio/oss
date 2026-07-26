#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: resolve-release-tag.sh <delibase@vX.Y.Z>" >&2
  exit 2
fi

tag="$1"
core_number='(0|[1-9][0-9]*)'
if [[ ! "$tag" =~ ^delibase@v${core_number}\.${core_number}\.${core_number}$ ]]; then
  echo "invalid delibase release tag: expected delibase@vX.Y.Z with no leading zeroes" >&2
  exit 1
fi

version="${tag#delibase@}"
if [ "${#version}" -gt 128 ]; then
  echo "invalid delibase release tag: OCI version tag exceeds 128 characters" >&2
  exit 1
fi

printf '%s\n' "$version"
