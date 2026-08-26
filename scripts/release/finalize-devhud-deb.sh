#!/usr/bin/env bash

set -euo pipefail

if [ "$#" -ne 2 ] || [ ! -f "$1" ]; then
  echo "usage: finalize-devhud-deb.sh <input.deb> <output.deb>" >&2
  exit 1
fi

input="$(cd "$(dirname "$1")" && pwd -P)/$(basename "$1")"
script_directory="$(cd "$(dirname "$0")" && pwd -P)"
output_directory="$(cd "$(dirname "$2")" && pwd -P)"
output="$output_directory/$(basename "$2")"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

dpkg-deb -R "$input" "$work/package"
host_relative="$(find "$work/package" -type f -name devhud-native-messaging-host -printf '%P\n' | LC_ALL=C sort)"
if [ -z "$host_relative" ] || [ "$(printf '%s\n' "$host_relative" | wc -l)" -ne 1 ]; then
  echo "DevHud Debian package must contain exactly one Native Messaging host" >&2
  exit 1
fi
host="/$host_relative"
manifest="/etc/opt/chrome/native-messaging-hosts/io.delino.devhud.native_messaging.json"

sed "s|@HOST@|$host|g; s|@MANIFEST@|$manifest|g" "$script_directory/linux/postinst.in" > "$work/package/DEBIAN/postinst"
sed "s|@HOST@|$host|g; s|@MANIFEST@|$manifest|g; s|@RUNTIME_ROOT@|/run/user|g" "$script_directory/linux/prerm.in" > "$work/package/DEBIAN/prerm"
chmod 0755 "$work/package/DEBIAN/postinst" "$work/package/DEBIAN/prerm"
if [ -n "${SOURCE_DATE_EPOCH:-}" ]; then find "$work/package" -print0 | xargs -0 touch -h -d "@$SOURCE_DATE_EPOCH"; fi
dpkg-deb --root-owner-group --build "$work/package" "$output"
