#!/usr/bin/env bash
set -euo pipefail
project=${1:?project}
version=${2:?version}
channel=${3:?channel}
arch=${4:?architecture}
origin=${5:?origin}
mode=${6:-fixture}
case "$project" in binpm|cargo-mono|nodeup|with-watch|derun|runmoor|clibox) ;; *) exit 2 ;; esac
name=delino
if [ "$channel" = preview ]; then name=delino-preview; fi
mkdir -p /root/.config/delino-package-test
printf 'preserve\n' > /root/.config/delino-package-test/sentinel
verify_license() {
  local license_file="/usr/share/doc/$project/copyright"
  grep -F 'TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION' "$license_file"
}
if command -v apt-get >/dev/null; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y ca-certificates curl gnupg >/dev/null
  mkdir -p /usr/share/keyrings
  curl -fsS "$origin/keys/delino-packages.asc" | gpg --batch --yes --dearmor -o /usr/share/keyrings/delino-packages.gpg
  curl -fsS "$origin/setup/$name.sources" -o "/etc/apt/sources.list.d/$name.sources"
  apt-get update
  apt-cache policy "$project"
  apt-get install -y "$project=$version-1"
  test "$(dpkg-query -W -f='${Architecture}' "$project")" = "$arch"
  test "$(dpkg-query -W -f='${Architecture}' delino-archive-keyring)" = all
  dpkg-query -L delino-archive-keyring | grep -Fx /usr/share/keyrings/delino-packages.gpg
  "$project" --help >/dev/null
  if [ "$project" = runmoor ]; then "$project" version; else "$project" --version; fi | grep -F "$version"
  verify_license
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
  verify_license
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
if [ "$mode" = fixture ]; then
  # Each fault is served only by the isolated fixture server. Require the package
  # manager's verification diagnostic, not merely an unrelated command failure.
  for fault in bad-signature bad-checksum; do
    if command -v apt-get >/dev/null; then
      apt-get clean
      curl -fsS "$origin/$fault/setup/$name.sources" -o "/etc/apt/sources.list.d/$name.sources"
      if [ "$fault" = bad-signature ]; then
        if apt-get update --error-on=any > /tmp/rejected.log 2>&1; then cat /tmp/rejected.log; exit 1; fi
        grep -Ei 'signature|GPG|Signed file' /tmp/rejected.log
      else
        apt-get update --error-on=any
        if apt-get install -y "$project=$version-1" > /tmp/rejected.log 2>&1; then cat /tmp/rejected.log; exit 1; fi
        grep -Ei 'hash sum mismatch|checksum|hashes of expected file' /tmp/rejected.log
      fi
    else
      dnf clean all >/dev/null
      curl -fsS "$origin/$fault/setup/$name.repo" -o "/etc/yum.repos.d/$name.repo"
      if [ "$fault" = bad-signature ]; then
        if dnf -y --disablerepo='*' --enablerepo="$name" --setopt="$name.skip_if_unavailable=0" makecache > /tmp/rejected.log 2>&1; then cat /tmp/rejected.log; exit 1; fi
        grep -Ei 'signature|GPG|verification' /tmp/rejected.log
      else
        if dnf -y --setopt="$name.skip_if_unavailable=0" install "$project-$version-1" > /tmp/rejected.log 2>&1; then cat /tmp/rejected.log; exit 1; fi
        grep -Ei 'checksum|digest' /tmp/rejected.log
      fi
    fi
    test ! -e "/usr/bin/$project"
  done
fi
test ! -e "/usr/bin/$project"
test "$(cat /root/.config/delino-package-test/sentinel)" = preserve
printf 'Package lifecycle verified: %s %s %s\n' "$project" "$version" "$arch"
