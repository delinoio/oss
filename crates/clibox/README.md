# clibox

A native Rust CLI, also available as `@delino/clibox` on npm for project-local version pinning.

The initial release provides help and version output. Domain commands will be added in future releases.

```sh
cargo install clibox
clibox --help
clibox --version
```

For JavaScript projects:

```sh
pnpm add -D -E @delino/clibox
pnpm exec clibox --version
```

The npm launcher requires Node.js 22 or newer. Prebuilt binaries cover macOS and Windows x64/arm64, and Linux x64/arm64 with glibc or musl. npm installation does not require Rust or installation scripts.

Licensed under MIT.
