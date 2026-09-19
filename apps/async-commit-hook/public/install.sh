#!/bin/sh
# Download, verify, then install. Requires Git, curl, tar, and cosign v3+.
set -eu
version=${ACH_VERSION:-0.1.0}
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      [ "$#" -ge 2 ] || { echo '--version requires MAJOR.MINOR.PATCH' >&2; exit 2; }
      version=$2
      shift 2
      ;;
    *) echo "Unknown argument: $1. Usage: sh install.sh [--version MAJOR.MINOR.PATCH]" >&2; exit 2;;
  esac
done
case "$version" in *[!0-9.]*|'') echo 'Version must be MAJOR.MINOR.PATCH' >&2; exit 2;; esac
printf '%s\n' "$version" | awk '/^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/ {ok=1} END {exit !ok}' || { echo 'Version must be MAJOR.MINOR.PATCH' >&2; exit 2; }
for tool in curl tar cosign git; do command -v "$tool" >/dev/null || { echo "Required tool missing: $tool" >&2; exit 2; }; done
case "$(uname -s)" in Darwin) platform=darwin;; Linux) platform=linux;; *) echo 'Use install.ps1 on Windows.' >&2; exit 2;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) echo 'Unsupported architecture' >&2; exit 2;; esac
asset="ach-$platform-$arch.tar.gz"
base="https://github.com/delinoio/oss/releases/download/async-commit-hook@v$version"
identity='https://github.com/delinoio/oss/.github/workflows/release-async-commit-hook.yml@refs/heads/main'
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT HUP INT TERM
for name in "$asset" "$asset.sigstore.json" SHA256SUMS SHA256SUMS.sigstore.json; do
  curl --fail --location --proto '=https' --proto-redir '=https' --silent --show-error "$base/$name" -o "$temporary/$name"
done
for name in "$asset" SHA256SUMS; do
  cosign verify-blob --bundle "$temporary/$name.sigstore.json" --certificate-identity "$identity" --certificate-oidc-issuer https://token.actions.githubusercontent.com "$temporary/$name"
done
expected=$(awk -v asset="$asset" '$2 == asset {print $1; count++} END {if (count != 1) exit 1}' "$temporary/SHA256SUMS")
if command -v sha256sum >/dev/null; then actual=$(sha256sum "$temporary/$asset" | cut -d ' ' -f 1); else actual=$(shasum -a 256 "$temporary/$asset" | cut -d ' ' -f 1); fi
[ "$actual" = "$expected" ] || { echo 'Checksum mismatch; installation refused' >&2; exit 3; }
[ "$(tar -tzf "$temporary/$asset")" = ach ] || { echo 'Invalid archive layout' >&2; exit 3; }
tar -xzf "$temporary/$asset" -C "$temporary" ach
[ -f "$temporary/ach" ] && [ ! -L "$temporary/ach" ] || exit 3
destination=${ACH_INSTALL_DIR:-"$HOME/.local/bin"}
mkdir -p "$destination"
if [ -e "$destination/ach" ] || [ -L "$destination/ach" ]; then
  echo 'An installation already exists. Use ach self-update, or brew upgrade async-commit-hook for Homebrew.' >&2
  exit 2
fi
# Same-directory staging prevents a partial executable from becoming visible.
staged=$(mktemp "$destination/.ach-install-XXXXXX")
cp "$temporary/ach" "$staged"
chmod 755 "$staged"
if ! ln "$staged" "$destination/ach"; then rm -f "$staged"; exit 3; fi
rm -f "$staged"
printf 'Installed ach %s at %s/ach. Add this directory to PATH and run ach init.\n' "$version" "$destination"
