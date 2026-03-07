#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Render and optionally submit winget manifests.

Usage:
  ./scripts/release/update-winget.sh \
    --package-id <DelinoIO.Nodeup|DelinoIO.Derun|DelinoIO.DexDex|DelinoIO.DexDexMainServer|DelinoIO.DexDexWorkerServer> \
    --version <semver> \
    --installer-url <url> \
    --installer-sha256 <sha> \
    [--winget-repo <owner/repo>] [--dry-run]

Options:
  --package-id <id>      winget package identifier.
  --version <semver>     Release version without v-prefix.
  --installer-url <url>  Installer URL.
  --installer-sha256 <sha>
                         Installer SHA256.
  --winget-repo <repo>   winget manifests repo (default: microsoft/winget-pkgs).
  --dry-run              Render only; do not open a PR.
USAGE
}

package_id=""
version=""
installer_url=""
installer_sha256=""
winget_repo="microsoft/winget-pkgs"
dry_run="0"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --package-id)
      package_id="${2:-}"
      shift 2
      ;;
    --version)
      version="${2:-}"
      shift 2
      ;;
    --installer-url)
      installer_url="${2:-}"
      shift 2
      ;;
    --installer-sha256)
      installer_sha256="${2:-}"
      shift 2
      ;;
    --winget-repo)
      winget_repo="${2:-}"
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
      echo "[release.winget] unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [ -z "$package_id" ] || [ -z "$version" ] || [ -z "$installer_url" ] || [ -z "$installer_sha256" ]; then
  echo "[release.winget] package-id, version, installer-url, and installer-sha256 are required" >&2
  exit 1
fi

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repo_root="$(git -C "$script_dir/../.." rev-parse --show-toplevel)"
template_root="$repo_root/packaging/winget/templates"

publisher="${package_id%%.*}"
package_name="${package_id##*.}"
publisher_first_lower="$(printf '%s' "$publisher" | tr '[:upper:]' '[:lower:]' | cut -c1)"
manifest_dir="manifests/${publisher_first_lower}/${publisher}/${package_name}/${version}"

installer_type="portable"
command_name=""
package_title=""
short_description=""

case "$package_id" in
  DelinoIO.Nodeup)
    command_name="nodeup"
    package_title="nodeup"
    short_description="Rust-based Node.js version manager"
    ;;
  DelinoIO.Derun)
    command_name="derun"
    package_title="derun"
    short_description="Terminal-faithful command runner with MCP output bridge"
    ;;
  DelinoIO.DexDexMainServer)
    command_name="dexdex-main-server"
    package_title="DexDex Main Server"
    short_description="DexDex control-plane Connect RPC server"
    ;;
  DelinoIO.DexDexWorkerServer)
    command_name="dexdex-worker-server"
    package_title="DexDex Worker Server"
    short_description="DexDex execution-plane session adapter server"
    ;;
  DelinoIO.DexDex)
    installer_type="wix"
    package_title="DexDex"
    short_description="DexDex desktop orchestration client"
    ;;
  *)
    echo "[release.winget] unsupported package-id: $package_id" >&2
    exit 1
    ;;
esac

render_file() {
  local template_file="$1"
  local output_file="$2"

  sed \
    -e "s|__PACKAGE_ID__|$package_id|g" \
    -e "s|__VERSION__|$version|g" \
    -e "s|__INSTALLER_URL__|$installer_url|g" \
    -e "s|__INSTALLER_SHA256__|$installer_sha256|g" \
    -e "s|__COMMAND__|$command_name|g" \
    -e "s|__PACKAGE_TITLE__|$package_title|g" \
    -e "s|__SHORT_DESCRIPTION__|$short_description|g" \
    "$template_file" >"$output_file"
}

version_template="$template_root/version.yaml.tmpl"
locale_template="$template_root/locale.en-US.yaml.tmpl"
installer_template="$template_root/installer-portable.yaml.tmpl"
if [ "$installer_type" = "wix" ]; then
  installer_template="$template_root/installer-wix.yaml.tmpl"
fi

if [ ! -f "$version_template" ] || [ ! -f "$locale_template" ] || [ ! -f "$installer_template" ]; then
  echo "[release.winget] required templates not found in $template_root" >&2
  exit 1
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

mkdir -p "$tmp_dir/$manifest_dir"

version_file="$tmp_dir/$manifest_dir/${package_id}.yaml"
installer_file="$tmp_dir/$manifest_dir/${package_id}.installer.yaml"
locale_file="$tmp_dir/$manifest_dir/${package_id}.locale.en-US.yaml"

render_file "$version_template" "$version_file"
render_file "$installer_template" "$installer_file"
render_file "$locale_template" "$locale_file"

if [ "$dry_run" = "1" ]; then
  echo "[release.winget] dry-run manifest directory: $manifest_dir" >&2
  cat "$version_file"
  cat "$installer_file"
  cat "$locale_file"
  exit 0
fi

if ! command -v gh >/dev/null 2>&1; then
  echo "[release.winget] gh CLI is required for non-dry-run mode" >&2
  exit 1
fi

if [ -z "${GH_TOKEN:-}" ]; then
  echo "[release.winget] GH_TOKEN is required for non-dry-run mode" >&2
  exit 1
fi

echo "[release.winget] cloning $winget_repo" >&2
gh repo clone "$winget_repo" "$tmp_dir/winget" -- --depth=1

pushd "$tmp_dir/winget" >/dev/null
mkdir -p "$manifest_dir"
cp "$version_file" "$manifest_dir/"
cp "$installer_file" "$manifest_dir/"
cp "$locale_file" "$manifest_dir/"

branch_name="release/${package_name}-${version}"
git checkout -b "$branch_name"
git add "$manifest_dir"

if git diff --cached --quiet; then
  echo "[release.winget] no manifest changes for $package_id $version" >&2
  exit 0
fi

git commit -m "chore(winget): update ${package_id} to ${version}"

gh pr create \
  --repo "$winget_repo" \
  --title "${package_id} version ${version}" \
  --body "Automated winget manifest update for ${package_id} ${version}." \
  --base master \
  --head "$branch_name"

popd >/dev/null
