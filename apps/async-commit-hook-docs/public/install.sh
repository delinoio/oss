#!/bin/sh
# Download, verify, then install. Requires Git, curl, tar, and cosign v3+.
set -eu
version=${ACH_VERSION-}
explicit_version=0
[ -n "$version" ] && explicit_version=1
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      [ "$#" -ge 2 ] || { echo '--version requires MAJOR.MINOR.PATCH' >&2; exit 2; }
      version=$2
      explicit_version=1
      shift 2
      ;;
    *) echo "Unknown argument: $1. Usage: sh install.sh [--version MAJOR.MINOR.PATCH]" >&2; exit 2;;
  esac
done

for tool in curl tar cosign git awk sort tail tr; do command -v "$tool" >/dev/null || { echo "Required tool missing: $tool" >&2; exit 2; }; done

latest_published_version() {
  page=1
  versions=
  while :; do
    releases=$(curl --fail --location --proto '=https' --proto-redir '=https' --silent --show-error \
      --header 'Accept: application/vnd.github+json' \
      --header 'X-GitHub-Api-Version: 2022-11-28' \
      "https://api.github.com/repos/delinoio/oss/releases?per_page=100&page=$page") || return 1
    compact=$(printf '%s' "$releases" | tr -d '[:space:]')
    case "$compact" in
      \[*\]) ;;
      *) return 1 ;;
    esac
    [ "$compact" = '[]' ] && break
    page_versions=$(printf '%s' "$releases" | tr ',' '\n' | awk '
      function field(line, name) {
        sub("^.*\\\"" name "\\\"[[:space:]]*:[[:space:]]*", "", line)
        sub("[,}].*$", "", line)
        gsub(/^"|"$/, "", line)
        return line
      }
      /\"tag_name\"[[:space:]]*:/ { tag = field($0, "tag_name"); next }
      /\"draft\"[[:space:]]*:/ { draft = field($0, "draft"); next }
      /\"prerelease\"[[:space:]]*:/ {
        prerelease = field($0, "prerelease")
        if (draft == "false" && prerelease == "false" && tag ~ /^async-commit-hook@v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/) {
          sub(/^async-commit-hook@v/, "", tag)
          print tag
        }
        tag = ""
        draft = ""
        prerelease = ""
      }
    ')
    [ -n "$page_versions" ] && versions="${versions}${page_versions}
"
    page=$((page + 1))
  done
  [ -n "$versions" ] || return 1
  printf '%s' "$versions" | LC_ALL=C sort -t. -k1,1n -k2,2n -k3,3n | tail -n 1
}

if [ "$explicit_version" -eq 1 ]; then
  case "$version" in *[!0-9.]*|'') echo 'Version must be MAJOR.MINOR.PATCH' >&2; exit 2;; esac
  printf '%s\n' "$version" | awk '/^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/ {ok=1} END {exit !ok}' || { echo 'Version must be MAJOR.MINOR.PATCH' >&2; exit 2; }
  base="https://github.com/delinoio/oss/releases/download/async-commit-hook@v$version"
  display_version=$version
else
  version=$(latest_published_version) || { echo 'Could not determine the latest published async-commit-hook release.' >&2; exit 1; }
  base="https://github.com/delinoio/oss/releases/download/async-commit-hook@v$version"
  display_version=$version
fi
case "$(uname -s)" in Darwin) platform=darwin;; Linux) platform=linux;; *) echo 'Use install.ps1 on Windows.' >&2; exit 2;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) echo 'Unsupported architecture' >&2; exit 2;; esac
asset="ach-$platform-$arch.tar.gz"
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
printf 'Installed ach %s at %s/ach. Add this directory to PATH and run ach init.\n' "$display_version" "$destination"
