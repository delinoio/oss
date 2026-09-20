# @delino/clibox

Run the native `clibox` CLI with a version pinned in your JavaScript project's package manifest and lockfile.

```sh
pnpm add -D -E @delino/clibox
pnpm exec clibox --help
pnpm exec clibox --version
```

With npm:

```sh
npm install --save-dev --save-exact @delino/clibox
npm exec -- clibox --version
```

You can also add `clibox --version` to a `package.json` script. The initial CLI provides help and version output; no domain commands are available yet.

## Requirements

- Node.js 22 or newer.
- macOS or Windows (x64 or arm64), or Linux (x64 or arm64, glibc or musl). GNU builds require glibc 2.35+ on x64 and 2.39+ on arm64; Alpine uses the musl builds.
- Optional dependencies enabled in your package manager.

The package manager installs the matching prebuilt executable. Rust, postinstall scripts, and separate binary downloads are not required. You can install with `--ignore-scripts`.

## Troubleshooting

If the native package is missing, reinstall with optional dependencies enabled (`npm install --include=optional` or `pnpm install` without `--no-optional`). Do not copy `node_modules` between operating systems, architectures, or Linux libc environments; reinstall from your lockfile on the destination machine. If a lockfile omits the destination's optional package, regenerate it with your package manager and commit the corrected lockfile.

If versions disagree, reinstall the dependency so `@delino/clibox` and its selected platform package have the same exact version. Unsupported platforms can use `cargo install clibox` where the Rust toolchain supports the host.

The package provides the `clibox` command only, with no public JavaScript import API.

Licensed under MIT.
