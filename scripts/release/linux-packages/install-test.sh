#!/usr/bin/env bash
set -euo pipefail
project=${1:?project}
version=${2:?version}
channel=${3:?channel}
arch=${4:?architecture}
origin=${5:?origin}
mode=${6:-fixture}
case "$project" in binpm|cargo-mono|nodeup|with-watch|derun|runmoor) ;; *) exit 2 ;; esac
name=delino
if [ "$channel" = preview ]; then name=delino-preview; fi
mkdir -p /root/.config/delino-package-test
printf 'preserve\n' > /root/.config/delino-package-test/sentinel
if command -v apt-get >/dev/null; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y ca-certificates curl gnupg >/dev/null
  mkdir -p /etc/apt/keyrings
  curl -fsS "$origin/keys/delino-packages.asc" | gpg --batch --yes --dearmor -o /etc/apt/keyrings/delino-packages.gpg
  curl -fsS "$origin/setup/$name.sources" -o "/etc/apt/sources.list.d/$name.sources"
  apt-get update
  apt-cache policy "$project"
  apt-get install -y "$project=$version-1"
  test "$(dpkg-query -W -f='${Architecture}' "$project")" = "$arch"
  "$project" --help >/dev/null
  if [ "$project" = runmoor ]; then "$project" version; else "$project" --version; fi | grep -F "$version"
  apt-get remove -y "$project"
  if [ "$mode" = fixture ]; then
    curl -fsS "$origin/previous/${project}_0.0.0-1_${arch}.deb" -o /tmp/previous.deb
    apt-get install -y /tmp/previous.deb
    test "$(dpkg-query -W -f='${Version}' "$project")" = 0.0.0-1
    apt-get install --only-upgrade -y "$project"
    test "$(dpkg-query -W -f='${Version}' "$project")" = "$version-1"
    apt-get remove -y "$project"
  fi
else
  dnf install -y ca-certificates curl-minimal >/dev/null
  curl -fsS "$origin/setup/$name.repo" -o "/etc/yum.repos.d/$name.repo"
  rpm --import "$origin/keys/delino-packages.asc"
  dnf -y --refresh install "$project-$version-1"
  rpm_arch=x86_64; if [ "$arch" = arm64 ]; then rpm_arch=aarch64; fi
  test "$(rpm -q --qf '%{ARCH}' "$project")" = "$rpm_arch"
  "$project" --help >/dev/null
  if [ "$project" = runmoor ]; then "$project" version; else "$project" --version; fi | grep -F "$version"
  dnf remove -y "$project"
  if [ "$mode" = fixture ]; then
    curl -fsS "$origin/previous/${project}-0.0.0-1.${rpm_arch}.rpm" -o /tmp/previous.rpm
    dnf -y --setopt=localpkg_gpgcheck=1 install /tmp/previous.rpm
    test "$(rpm -q --qf '%{VERSION}-%{RELEASE}' "$project")" = 0.0.0-1
    dnf upgrade -y "$project"
    test "$(rpm -q --qf '%{VERSION}-%{RELEASE}' "$project")" = "$version-1"
    dnf remove -y "$project"
  fi
fi
test ! -e "/usr/bin/$project"
test "$(cat /root/.config/delino-package-test/sentinel)" = preserve
printf 'Package lifecycle verified: %s %s %s\n' "$project" "$version" "$arch"
