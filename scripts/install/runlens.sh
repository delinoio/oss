#!/bin/sh
# Only authenticated release archives are installed. Local archives use the same trust checks.
set -eu
umask 077
version= install_dir=${HOME:?HOME is required}/.local/bin archive_dir=
fail() { printf '%s\n' "runlens installer: $*" >&2; exit 1; }
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version|--install-dir|--archive-dir)
      [ "$#" -ge 2 ] || fail 'option requires a value'
      case "$1" in --version) version=$2;; --install-dir) install_dir=$2;; --archive-dir) archive_dir=$2;; esac
      shift 2;;
    --help) printf '%s\n' 'Usage: sh install-runlens.sh --version X.Y.Z [--install-dir DIR] [--archive-dir DIR]'; exit 0;;
    *) fail 'unknown option';;
  esac
done
printf '%s\n' "$version" | LC_ALL=C grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || fail 'an explicit stable version X.Y.Z is required'
case $(uname -s) in Darwin) platform=darwin;; Linux) platform=linux;; *) fail 'unsupported operating system';; esac
case $(uname -m) in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) fail 'unsupported architecture';; esac
if [ "$platform" = linux ]; then
  getconf GNU_LIBC_VERSION >/dev/null 2>&1 || fail 'a glibc Linux host is required'
fi
command -v cosign >/dev/null 2>&1 || fail 'install a trusted cosign executable first'
asset=runlens-$platform-$arch.tar.gz
tag=runlens@v$version
base=https://github.com/delinoio/oss/releases/download/$tag
identity=https://github.com/delinoio/oss/.github/workflows/release-runlens.yml@refs/tags/$tag
work=$(mktemp -d "${TMPDIR:-/tmp}/runlens-install.XXXXXXXX") || fail 'cannot create private temporary directory'
staged=
cleanup() { result=$?; trap - 0; [ -z "$staged" ] || rm -f -- "$staged" || result=1; rm -rf -- "$work" || result=1; exit "$result"; }
trap cleanup 0
trap 'exit 1' 1 2 15
fetch() {
  if [ -n "$archive_dir" ]; then cp -- "$archive_dir/$1" "$work/$1" || fail 'offline release file is missing';
  else curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --output "$work/$1" "$base/$1" || fail 'download failed'; fi
}
for file in "$asset" "$asset.sigstore.json" SHA256SUMS SHA256SUMS.sigstore.json; do fetch "$file"; done
for file in SHA256SUMS "$asset"; do
  cosign verify-blob --bundle "$work/$file.sigstore.json" --certificate-identity "$identity" --certificate-oidc-issuer https://token.actions.githubusercontent.com "$work/$file" >/dev/null || fail 'release authentication failed'
done
expected=$(awk -v name="$asset" '$2 == name { print $1; found++ } END { if (found != 1) exit 1 }' "$work/SHA256SUMS") || fail 'missing or duplicate checksum'
printf '%s\n' "$expected" | LC_ALL=C grep -Eq '^[0-9a-f]{64}$' || fail 'invalid checksum'
if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$work/$asset" | cut -d ' ' -f 1); else actual=$(shasum -a 256 "$work/$asset" | cut -d ' ' -f 1); fi
[ "$expected" = "$actual" ] || fail 'archive checksum mismatch'
# The archive contract has exactly two regular files. Never extract arbitrary member paths.
[ "$(tar -tzf "$work/$asset" | LC_ALL=C sort)" = "$(printf 'LICENSES.txt\nrunlens')" ] || fail 'unexpected archive members'
mkdir -p -- "$install_dir" || fail 'cannot create installation directory'
[ ! -L "$install_dir/runlens" ] || fail 'refusing to replace a symlink'
[ ! -e "$install_dir/runlens" ] || [ -f "$install_dir/runlens" ] || fail 'installation target is not a regular file'
staged=$(mktemp "$install_dir/.runlens.XXXXXXXX") || fail 'cannot stage installation'
tar -xOzf "$work/$asset" runlens > "$staged" || fail 'cannot read executable'
chmod 755 "$staged"
[ "$("$staged" --version)" = "runlens $version" ] || fail 'executable version mismatch'
# Rename on the destination volume preserves the previous executable until verification succeeds.
[ ! -L "$install_dir/runlens" ] || fail 'refusing to replace a symlink'
[ ! -e "$install_dir/runlens" ] || [ -f "$install_dir/runlens" ] || fail 'installation target is not a regular file'
mv -f -- "$staged" "$install_dir/runlens" || fail 'atomic installation failed'
staged=
printf '%s\n' "Installed Runlens $version. Run runlens doctor to check host tracing support." >&2
