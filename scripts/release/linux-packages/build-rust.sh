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
dnf install -y gcc gcc-c++ make pkgconf-pkg-config xz-devel perl git ca-certificates curl-minimal
export CARGO_HOME=/tmp/delino-cargo
export RUSTUP_HOME=/tmp/delino-rustup
export CARGO_TARGET_DIR=/workspace/target
export PATH="$CARGO_HOME/bin:$PATH"
toolchain=$(tr -d '\r\n' < rust-toolchain)
curl --proto '=https' --tlsv1.2 --fail --silent --show-error https://sh.rustup.rs -o /tmp/delino-rustup-init.sh
sh /tmp/delino-rustup-init.sh -y --profile minimal --default-toolchain "$toolchain" --target "$target"
cargo build --locked -p "$project" --release --target "$target"
readelf --version-info "target/$target/release/$project"
