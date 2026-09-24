#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Usage: pnport.sh [--version <MAJOR.MINOR.PATCH|latest>] [--install-dir <directory>] [--source-dir <verified-release-directory>]
Install the native pnport executable and its matched injection library together.
USAGE
}

version=latest
install_dir="${HOME}/.local/bin"
source_dir=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version|--install-dir|--source-dir)
      [ "$#" -ge 2 ] || { usage; exit 2; }
      case "$1" in
        --version) version="$2" ;;
        --install-dir) install_dir="$2" ;;
        --source-dir) source_dir="$2" ;;
      esac
      shift 2 ;;
    --help) usage; exit 0 ;;
    *) usage; exit 2 ;;
  esac
done

if [ "$version" = latest ]; then
  [ -z "$source_dir" ] || { echo '[install.pnport] --source-dir requires an exact version' >&2; exit 2; }
  versions=""
  page=1
  while :; do
    releases=$(curl -fsSL "https://api.github.com/repos/delinoio/oss/releases?per_page=100&page=$page")
    tags=$(printf '%s\n' "$releases" | awk -F '"' '/"tag_name"/ {print $4}')
    [ -n "$tags" ] || break
    matches=$(printf '%s\n' "$tags" | sed -nE 's/^pnport@v([0-9]+\.[0-9]+\.[0-9]+)$/\1/p')
    if [ -n "$matches" ]; then versions="${versions}${versions:+$'\n'}${matches}"; fi
    page=$((page + 1))
  done
  [ -n "$versions" ] || { echo '[install.pnport] no published pnport version' >&2; exit 1; }
  version=$(printf '%s\n' "$versions" | awk -F . 'NF == 3 && (best == "" || $1 > major || ($1 == major && $2 > minor) || ($1 == major && $2 == minor && $3 > patch)) { major=$1; minor=$2; patch=$3; best=$0 } END { print best }')
fi
[[ "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || { echo '[install.pnport] exact stable version required' >&2; exit 2; }
[ -n "$install_dir" ] || { echo '[install.pnport] install directory required' >&2; exit 2; }

case "$(uname -s)" in
  Darwin) platform=darwin; library=libpnport_preload.dylib ;;
  Linux)
    platform=linux; library=libpnport_preload.so
    getconf GNU_LIBC_VERSION >/dev/null 2>&1 || { echo '[install.pnport] GNU libc is required' >&2; exit 1; } ;;
  *) echo '[install.pnport] unsupported operating system' >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) architecture=x64 ;;
  arm64|aarch64) architecture=arm64 ;;
  *) echo '[install.pnport] unsupported architecture' >&2; exit 1 ;;
esac
if [ "$platform" = linux ]; then suffix="${platform}-${architecture}-gnu"; else suffix="${platform}-${architecture}"; fi
asset="pnport-${suffix}.tar.gz"
temporary=$(mktemp -d)
stage=""
cleanup() { rm -rf -- "$temporary"; if [ -n "$stage" ]; then rm -rf -- "$stage"; fi; }
trap cleanup EXIT
if [ -n "$source_dir" ]; then
  cp -- "$source_dir/$asset" "$source_dir/SHA256SUMS" "$temporary/"
else
  base="https://github.com/delinoio/oss/releases/download/pnport@v${version}"
  curl -fsSL "$base/$asset" -o "$temporary/$asset"
  curl -fsSL "$base/SHA256SUMS" -o "$temporary/SHA256SUMS"
fi
expected=$(awk -v name="$asset" '$2 == name && $1 ~ /^[a-f0-9]+$/ {print $1}' "$temporary/SHA256SUMS")
[ "${#expected}" -eq 64 ] || { echo '[install.pnport] missing or duplicate archive checksum' >&2; exit 1; }
if command -v shasum >/dev/null 2>&1; then actual=$(shasum -a 256 "$temporary/$asset" | awk '{print $1}'); else actual=$(sha256sum "$temporary/$asset" | awk '{print $1}'); fi
[ "$actual" = "$expected" ] || { echo '[install.pnport] checksum mismatch' >&2; exit 1; }
notices=$(printf '%s\n%s\n' LICENSE LICENSE.fspy)
[ "$(tar -tzf "$temporary/$asset" | LC_ALL=C sort)" = "$(printf '%s\n%s\n%s\n' pnport "$library" "$notices" | LC_ALL=C sort)" ] || { echo '[install.pnport] invalid native archive inventory' >&2; exit 1; }
tar -xzf "$temporary/$asset" -C "$temporary"
[ -f "$temporary/pnport" ] && [ -f "$temporary/$library" ] || { echo '[install.pnport] incomplete native archive' >&2; exit 1; }
[ "$("$temporary/pnport" --version)" = "pnport $version" ] || { echo '[install.pnport] executable version mismatch' >&2; exit 1; }

mkdir -p -- "$install_dir/.pnport/versions"
versions="$install_dir/.pnport/versions"
destination="$versions/$version"
stage=$(mktemp -d "$versions/.stage.XXXXXX")
cp -- "$temporary/pnport" "$temporary/$library" "$temporary/LICENSE" "$stage/"
cp -- "$temporary/LICENSE.fspy" "$stage/"
chmod 755 "$stage/pnport"
if [ -e "$destination" ]; then
  cmp -s "$stage/pnport" "$destination/pnport" && cmp -s "$stage/$library" "$destination/$library" && cmp -s "$stage/LICENSE" "$destination/LICENSE" || { echo '[install.pnport] conflicting installed version' >&2; exit 1; }
  cmp -s "$stage/LICENSE.fspy" "$destination/LICENSE.fspy" || { echo '[install.pnport] conflicting installed notice' >&2; exit 1; }
else
  mv -- "$stage" "$destination"
  stage=""
fi
launcher=$(mktemp "$install_dir/.pnport-launcher.XXXXXX")
cat > "$launcher" <<EOF
#!/bin/sh
base=\$(CDPATH= cd -- "\$(dirname -- "\$0")" && pwd -P) || exit 1
exec "\$base/.pnport/versions/$version/pnport" "\$@"
EOF
chmod 755 "$launcher"
mv -f -- "$launcher" "$install_dir/pnport"
echo "[install.pnport] installed pnport $version in $install_dir" >&2
