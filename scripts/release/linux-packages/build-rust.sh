#!/usr/bin/env bash
set -euo pipefail
project=${1:?project required}
target=${2:?target required}
case "$project" in binpm|cargo-mono|nodeup|with-watch) ;; *) exit 2 ;; esac
case "$target" in
  x86_64-unknown-linux-gnu) export RUSTFLAGS='-C target-cpu=x86-64'; export CFLAGS='-march=x86-64 -mtune=generic'; export CXXFLAGS="$CFLAGS" ;;
  aarch64-unknown-linux-gnu) export RUSTFLAGS='-C target-cpu=generic'; export CFLAGS='-march=armv8-a'; export CXXFLAGS="$CFLAGS" ;;
  *) exit 2 ;;
esac
# Build on the oldest supported libc, independently of the hosted runner OS.
dnf install -y gcc gcc-c++ make pkgconf-pkg-config xz-devel perl git ca-certificates curl-minimal python3
export CARGO_HOME=/tmp/delino-cargo
export RUSTUP_HOME=/tmp/delino-rustup
export CARGO_TARGET_DIR=/workspace/target
export PATH="$CARGO_HOME/bin:$PATH"
toolchain=$(tr -d '\r\n' < rust-toolchain)
mapfile -t bootstrap < <(python3 - "$target" <<'PY'
import json, sys
pin = json.load(open('packaging/linux/pins.json'))['rustup']['assets'][sys.argv[1]]
print(pin['url'])
print(pin['sha256'])
PY
)
curl --proto '=https' --tlsv1.2 --fail --silent --show-error "${bootstrap[0]}" -o /tmp/delino-rustup-init
printf '%s  %s\n' "${bootstrap[1]}" /tmp/delino-rustup-init | sha256sum --check --strict
chmod 0755 /tmp/delino-rustup-init
/tmp/delino-rustup-init -y --profile minimal --default-toolchain "$toolchain" --target "$target" --no-modify-path
rustup set auto-self-update disable
cargo build --locked -p "$project" --release --target "$target"
readelf --version-info "target/$target/release/$project"
