#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
resolver="${script_dir}/resolve-release-tag.sh"

valid_tags=(
  "delibase@v0.0.0:v0.0.0"
  "delibase@v1.2.3:v1.2.3"
  "delibase@v10.200.3000:v10.200.3000"
)
for test_case in "${valid_tags[@]}"; do
  tag="${test_case%%:*}"
  expected="${test_case#*:}"
  actual="$("$resolver" "$tag")"
  if [ "$actual" != "$expected" ]; then
    echo "release tag resolved to ${actual}, expected ${expected}" >&2
    exit 1
  fi
done

invalid_tags=(
  "v1.2.3"
  "delibase@1.2.3"
  "delibase@v1.2"
  "delibase@v1.2.3.4"
  "delibase@v01.2.3"
  "delibase@v1.02.3"
  "delibase@v1.2.03"
  "delibase@v1.2.3-alpha"
  "delibase@v1.2.3+build"
  "delibase@vlatest"
  "other@v1.2.3"
)
for tag in "${invalid_tags[@]}"; do
  if "$resolver" "$tag" >/dev/null 2>&1; then
    echo "invalid release tag was accepted: ${tag}" >&2
    exit 1
  fi
done

echo "delibase release tag validation passed"
