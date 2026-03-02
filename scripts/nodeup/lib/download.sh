#!/usr/bin/env bash

if [ "${NODEUP_DOWNLOAD_SH_LOADED:-0}" = "1" ]; then
  return 0
fi
NODEUP_DOWNLOAD_SH_LOADED=1

nodeup_release_repository() {
  printf '%s\n' "${NODEUP_RELEASE_REPOSITORY:-delinoio/oss}"
}

nodeup_require_https_url() {
  local url="$1"
  case "$url" in
    https://*)
      return 0
      ;;
    *)
      nodeup_error "Refusing non-HTTPS URL: ${url}"
      return 1
      ;;
  esac
}

nodeup_fetch_file() {
  local url="$1"
  local destination="$2"

  nodeup_require_https_url "$url" || return 1

  if [ "${NODEUP_DRY_RUN:-0}" = "1" ]; then
    nodeup_log "dry-run" "download ${url} -> ${destination}"
    return 0
  fi

  curl -fsSL "$url" -o "$destination"
}

nodeup_fetch_latest_tag() {
  local repository
  local api_url
  local payload
  local tag

  repository="$(nodeup_release_repository)"
  api_url="https://api.github.com/repos/${repository}/releases/latest"

  payload="$(curl -fsSL "$api_url")" || return 1
  tag="$(printf '%s\n' "$payload" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"

  if [ -z "$tag" ]; then
    return 1
  fi

  printf '%s\n' "$tag"
}

nodeup_is_valid_version() {
  local version="$1"
  case "$version" in
    v[0-9]*.[0-9]*.[0-9]*)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

nodeup_release_tag_from_version() {
  local version="$1"
  printf 'nodeup-%s\n' "$version"
}

nodeup_version_from_tag() {
  local tag="$1"
  case "$tag" in
    nodeup-v*)
      printf '%s\n' "${tag#nodeup-}"
      ;;
    *)
      return 1
      ;;
  esac
}

nodeup_resolve_release_tag() {
  local requested="$1"
  local resolved_tag
  local resolved_version

  if [ "$requested" = "latest" ]; then
    resolved_tag="$(nodeup_fetch_latest_tag)" || {
      nodeup_error "Failed to resolve latest release tag"
      return 1
    }
  else
    if ! nodeup_is_valid_version "$requested"; then
      nodeup_error "Invalid version: ${requested}. Expected latest or vX.Y.Z"
      return 1
    fi
    resolved_tag="$(nodeup_release_tag_from_version "$requested")"
  fi

  resolved_version="$(nodeup_version_from_tag "$resolved_tag")" || {
    nodeup_error "Invalid release tag format: ${resolved_tag}"
    return 1
  }

  printf '%s %s\n' "$resolved_tag" "$resolved_version"
}

nodeup_release_asset_url() {
  local tag="$1"
  local asset="$2"
  local repository

  repository="$(nodeup_release_repository)"
  printf 'https://github.com/%s/releases/download/%s/%s\n' "$repository" "$tag" "$asset"
}

nodeup_download_release_asset() {
  local tag="$1"
  local asset="$2"
  local destination="$3"
  local url

  url="$(nodeup_release_asset_url "$tag" "$asset")"
  nodeup_fetch_file "$url" "$destination"
}

nodeup_sha256_file() {
  local file_path="$1"

  if nodeup_command_exists sha256sum; then
    sha256sum "$file_path" | awk '{print $1}'
    return
  fi

  if nodeup_command_exists shasum; then
    shasum -a 256 "$file_path" | awk '{print $1}'
    return
  fi

  nodeup_error "No SHA256 tool found (need sha256sum or shasum)"
  return 1
}

nodeup_verify_checksum() {
  local checksum_file="$1"
  local artifact_path="$2"
  local artifact_name
  local expected
  local actual

  artifact_name="$(basename "$artifact_path")"

  expected="$(awk -v file="$artifact_name" '$2 == file || $2 == ("*" file) { print $1; exit }' "$checksum_file")"

  if [ -z "$expected" ]; then
    nodeup_error "Checksum entry missing for ${artifact_name}"
    return 1
  fi

  actual="$(nodeup_sha256_file "$artifact_path")" || return 1

  if [ "$expected" != "$actual" ]; then
    nodeup_error "Checksum mismatch for ${artifact_name}"
    return 1
  fi

  nodeup_debug "checksum verified for ${artifact_name}"
  return 0
}
