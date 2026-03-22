#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Render and optionally submit Homebrew formula/cask updates.

Usage:
  ./scripts/release/update-homebrew.sh \
    --project <nodeup|derun|dexdex-main-server|dexdex-worker-server|dexdex> \
    --version <semver> \
    [--source-url <url>] [--source-sha256 <sha>] \
    [--darwin-amd64-url <url>] [--darwin-amd64-sha256 <sha>] \
    [--darwin-arm64-url <url>] [--darwin-arm64-sha256 <sha>] \
    [--linux-amd64-url <url>] [--linux-amd64-sha256 <sha>] \
    [--desktop-url <url>] [--desktop-sha256 <sha>] \
    [--tap-repo <owner/repo>] [--dry-run]

Options:
  --project <id>         Package identifier.
  --version <semver>     Release version without v-prefix.
  --source-url <url>     Source tarball URL (nodeup/derun formula only).
  --source-sha256 <sha>  Source tarball SHA256 (nodeup/derun formula only).
  --darwin-amd64-url <url>
                         Darwin amd64 prebuilt artifact URL (DexDex server formulas).
  --darwin-amd64-sha256 <sha>
                         Darwin amd64 prebuilt artifact SHA256 (DexDex server formulas).
  --darwin-arm64-url <url>
                         Darwin arm64 prebuilt artifact URL (DexDex server formulas).
  --darwin-arm64-sha256 <sha>
                         Darwin arm64 prebuilt artifact SHA256 (DexDex server formulas).
  --linux-amd64-url <url>
                         Linux amd64 prebuilt artifact URL (DexDex server formulas).
  --linux-amd64-sha256 <sha>
                         Linux amd64 prebuilt artifact SHA256 (DexDex server formulas).
  --desktop-url <url>    Desktop installer URL (dexdex cask).
  --desktop-sha256 <sha> Desktop installer SHA256 (dexdex cask).
  --tap-repo <repo>      Homebrew tap repository (default: delinoio/homebrew-tap).
  --dry-run              Render only; do not open a PR.
USAGE
}

project=""
version=""
source_url=""
source_sha256=""
darwin_amd64_url=""
darwin_amd64_sha256=""
darwin_arm64_url=""
darwin_arm64_sha256=""
linux_amd64_url=""
linux_amd64_sha256=""
desktop_url=""
desktop_sha256=""
tap_repo="delinoio/homebrew-tap"
dry_run="0"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --project)
      project="${2:-}"
      shift 2
      ;;
    --version)
      version="${2:-}"
      shift 2
      ;;
    --source-url)
      source_url="${2:-}"
      shift 2
      ;;
    --source-sha256)
      source_sha256="${2:-}"
      shift 2
      ;;
    --darwin-amd64-url)
      darwin_amd64_url="${2:-}"
      shift 2
      ;;
    --darwin-amd64-sha256)
      darwin_amd64_sha256="${2:-}"
      shift 2
      ;;
    --darwin-arm64-url)
      darwin_arm64_url="${2:-}"
      shift 2
      ;;
    --darwin-arm64-sha256)
      darwin_arm64_sha256="${2:-}"
      shift 2
      ;;
    --linux-amd64-url)
      linux_amd64_url="${2:-}"
      shift 2
      ;;
    --linux-amd64-sha256)
      linux_amd64_sha256="${2:-}"
      shift 2
      ;;
    --desktop-url)
      desktop_url="${2:-}"
      shift 2
      ;;
    --desktop-sha256)
      desktop_sha256="${2:-}"
      shift 2
      ;;
    --tap-repo)
      tap_repo="${2:-}"
      shift 2
      ;;
    --dry-run)
      dry_run="1"
      shift 1
      ;;
    --help)
      usage
      exit 0
      ;;
    *)
      echo "[release.homebrew] unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [ -z "$project" ] || [ -z "$version" ]; then
  echo "[release.homebrew] --project and --version are required" >&2
  exit 1
fi

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repo_root="$(git -C "$script_dir/../.." rev-parse --show-toplevel)"

rendered_file=""
destination_path=""

case "$project" in
  nodeup|derun)
    if [ -z "$source_url" ] || [ -z "$source_sha256" ]; then
      echo "[release.homebrew] $project requires --source-url and --source-sha256" >&2
      exit 1
    fi

    template_path="$repo_root/packaging/homebrew/templates/${project}.rb.tmpl"
    destination_path="Formula/${project}.rb"

    if [ ! -f "$template_path" ]; then
      echo "[release.homebrew] template not found: $template_path" >&2
      exit 1
    fi

    rendered_file="$(mktemp)"
    sed \
      -e "s|__SOURCE_URL__|$source_url|g" \
      -e "s|__SOURCE_SHA256__|$source_sha256|g" \
      -e "s|__VERSION__|$version|g" \
      "$template_path" >"$rendered_file"
    ;;
  dexdex-main-server|dexdex-worker-server)
    if [ -z "$darwin_amd64_url" ] || [ -z "$darwin_amd64_sha256" ] || [ -z "$darwin_arm64_url" ] || [ -z "$darwin_arm64_sha256" ] || [ -z "$linux_amd64_url" ] || [ -z "$linux_amd64_sha256" ]; then
      echo "[release.homebrew] $project requires --darwin-amd64-url, --darwin-amd64-sha256, --darwin-arm64-url, --darwin-arm64-sha256, --linux-amd64-url, and --linux-amd64-sha256" >&2
      exit 1
    fi

    template_path="$repo_root/packaging/homebrew/templates/${project}.rb.tmpl"
    destination_path="Formula/${project}.rb"

    if [ ! -f "$template_path" ]; then
      echo "[release.homebrew] template not found: $template_path" >&2
      exit 1
    fi

    rendered_file="$(mktemp)"
    sed \
      -e "s|__DARWIN_AMD64_URL__|$darwin_amd64_url|g" \
      -e "s|__DARWIN_AMD64_SHA256__|$darwin_amd64_sha256|g" \
      -e "s|__DARWIN_ARM64_URL__|$darwin_arm64_url|g" \
      -e "s|__DARWIN_ARM64_SHA256__|$darwin_arm64_sha256|g" \
      -e "s|__LINUX_AMD64_URL__|$linux_amd64_url|g" \
      -e "s|__LINUX_AMD64_SHA256__|$linux_amd64_sha256|g" \
      -e "s|__VERSION__|$version|g" \
      "$template_path" >"$rendered_file"
    ;;
  dexdex)
    if [ -z "$desktop_url" ] || [ -z "$desktop_sha256" ]; then
      echo "[release.homebrew] dexdex cask requires --desktop-url and --desktop-sha256" >&2
      exit 1
    fi

    template_path="$repo_root/packaging/homebrew/templates/dexdex.rb.tmpl"
    destination_path="Casks/dexdex.rb"

    if [ ! -f "$template_path" ]; then
      echo "[release.homebrew] template not found: $template_path" >&2
      exit 1
    fi

    rendered_file="$(mktemp)"
    sed \
      -e "s|__DESKTOP_URL__|$desktop_url|g" \
      -e "s|__DESKTOP_SHA256__|$desktop_sha256|g" \
      -e "s|__VERSION__|$version|g" \
      "$template_path" >"$rendered_file"
    ;;
  *)
    echo "[release.homebrew] unsupported project: $project" >&2
    exit 1
    ;;
esac

if [ "$dry_run" = "1" ]; then
  echo "[release.homebrew] dry-run render for $project -> $destination_path" >&2
  cat "$rendered_file"
  rm -f "$rendered_file"
  exit 0
fi

if ! command -v gh >/dev/null 2>&1; then
  echo "[release.homebrew] gh CLI is required for non-dry-run mode" >&2
  exit 1
fi

if [ -z "${GH_TOKEN:-}" ]; then
  echo "[release.homebrew] GH_TOKEN is required for non-dry-run mode" >&2
  exit 1
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir" "$rendered_file"' EXIT

echo "[release.homebrew] cloning tap repo: $tap_repo" >&2
gh repo clone "$tap_repo" "$tmp_dir/tap" -- --depth=1

pushd "$tmp_dir/tap" >/dev/null
mkdir -p "$(dirname -- "$destination_path")"
cp "$rendered_file" "$destination_path"

branch_name="release/${project}-${version}"
git checkout -b "$branch_name"
git add "$destination_path"

if git diff --cached --quiet; then
  echo "[release.homebrew] no Homebrew changes for $project $version" >&2
  exit 0
fi

git commit -m "chore(${project}): bump Homebrew package to ${version}"

gh pr create \
  --repo "$tap_repo" \
  --title "chore(${project}): bump Homebrew package to ${version}" \
  --body "Automated Homebrew package update for ${project} ${version}." \
  --base main \
  --head "$branch_name"

popd >/dev/null
