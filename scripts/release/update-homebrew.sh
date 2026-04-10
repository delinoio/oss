#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Render and optionally push Homebrew formula/cask updates.

Usage:
  ./scripts/release/update-homebrew.sh \
    --project <nodeup|with-watch|derun|dexdex-main-server|dexdex-worker-server|dexdex> \
    --version <semver> \
    [--darwin-amd64-url <url>] [--darwin-amd64-sha256 <sha>] \
    [--darwin-arm64-url <url>] [--darwin-arm64-sha256 <sha>] \
    [--linux-amd64-url <url>] [--linux-amd64-sha256 <sha>] \
    [--desktop-url <url>] [--desktop-sha256 <sha>] \
    [--tap-repo <owner/repo>] [--dry-run]

Options:
  --project <id>         Package identifier.
  --version <semver>     Release version without v-prefix.
  --darwin-amd64-url <url>
                         Darwin amd64 prebuilt artifact URL (nodeup, with-watch, derun, and DexDex server formulas).
  --darwin-amd64-sha256 <sha>
                         Darwin amd64 prebuilt artifact SHA256 (nodeup, with-watch, derun, and DexDex server formulas).
  --darwin-arm64-url <url>
                         Darwin arm64 prebuilt artifact URL (nodeup, with-watch, derun, and DexDex server formulas).
  --darwin-arm64-sha256 <sha>
                         Darwin arm64 prebuilt artifact SHA256 (nodeup, with-watch, derun, and DexDex server formulas).
  --linux-amd64-url <url>
                         Linux amd64 prebuilt artifact URL (nodeup, with-watch, derun, and DexDex server formulas).
  --linux-amd64-sha256 <sha>
                         Linux amd64 prebuilt artifact SHA256 (nodeup, with-watch, derun, and DexDex server formulas).
  --desktop-url <url>    Desktop installer URL (dexdex cask).
  --desktop-sha256 <sha> Desktop installer SHA256 (dexdex cask).
  --tap-repo <repo>      Homebrew tap repository (default: delinoio/homebrew-tap).
  --dry-run              Render only; do not push to the tap repository.
USAGE
}

project=""
version=""
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

log() {
  echo "[release.homebrew] $*" >&2
}

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
  nodeup|with-watch|derun|dexdex-main-server|dexdex-worker-server)
    if [ -z "$darwin_amd64_url" ] || [ -z "$darwin_amd64_sha256" ] || [ -z "$darwin_arm64_url" ] || [ -z "$darwin_arm64_sha256" ] || [ -z "$linux_amd64_url" ] || [ -z "$linux_amd64_sha256" ]; then
      log "$project requires --darwin-amd64-url, --darwin-amd64-sha256, --darwin-arm64-url, --darwin-arm64-sha256, --linux-amd64-url, and --linux-amd64-sha256"
      exit 1
    fi

    template_path="$repo_root/packaging/homebrew/templates/${project}.rb.tmpl"
    destination_path="Formula/${project}.rb"

    if [ ! -f "$template_path" ]; then
      log "template not found: $template_path"
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
      log "dexdex cask requires --desktop-url and --desktop-sha256"
      exit 1
    fi

    template_path="$repo_root/packaging/homebrew/templates/dexdex.rb.tmpl"
    destination_path="Casks/dexdex.rb"

    if [ ! -f "$template_path" ]; then
      log "template not found: $template_path"
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
    log "unsupported project: $project"
    exit 1
    ;;
esac

if [ "$dry_run" = "1" ]; then
  log "dry-run render for $project -> $destination_path"
  cat "$rendered_file"
  rm -f "$rendered_file"
  exit 0
fi

if ! command -v gh >/dev/null 2>&1; then
  log "gh CLI is required for non-dry-run mode"
  exit 1
fi

tap_push_token="${HOMEBREW_TAP_GH_TOKEN:-${GH_TOKEN:-}}"
if [ -z "$tap_push_token" ]; then
  log "HOMEBREW_TAP_GH_TOKEN (or GH_TOKEN) is required for non-dry-run mode"
  exit 1
fi
export GH_TOKEN="$tap_push_token"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir" "$rendered_file"' EXIT

log "cloning tap repo: $tap_repo"
gh repo clone "$tap_repo" "$tmp_dir/tap" -- --depth=1

pushd "$tmp_dir/tap" >/dev/null

# Workaround: GitHub runner environments may not provide a git committer identity,
# which causes `git commit` to fail with "empty ident name" in this script.
# Scope: non-dry-run path only, inside the temporary tap clone created above.
# Remove when release workflows or the execution environment guarantee commit identity.
git config user.name "github-actions[bot]"
git config user.email "github-actions@users.noreply.github.com"
log "using commit identity: $(git config user.name) <$(git config user.email)>"

remote_url="https://x-access-token:${tap_push_token}@github.com/${tap_repo}.git"
git remote set-url origin "$remote_url"

if git ls-remote --exit-code --heads origin main >/dev/null 2>&1; then
  log "checking out tap branch: main"
  git fetch origin main --depth=1
  git checkout -B main origin/main
else
  log "bootstrapping empty tap repository with main branch"
  git checkout --orphan main
fi

mkdir -p "$(dirname -- "$destination_path")"
cp "$rendered_file" "$destination_path"
log "rendered ${project} formula/cask at ${destination_path}"

git add "$destination_path"

if git diff --cached --quiet; then
  log "no Homebrew changes for $project $version"
  exit 0
fi

log "staged changes:"
git status --short >&2

log "creating commit for ${destination_path}"
git commit -m "chore(${project}): bump Homebrew package to ${version}"
log "pushing tap update to ${tap_repo} main"
git push --set-upstream origin HEAD:main
log "tap push complete for ${project} ${version}"

popd >/dev/null
