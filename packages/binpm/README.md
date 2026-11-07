# binpm

Run the native binpm CLI through npm:

```sh
npx @delino/binpm --help
npx @delino/binpm x --package github:BurntSushi/ripgrep rg --version
```

For a global install:

```sh
npm install --global @delino/binpm
binpm --version
```

Requires Node.js 24 or later. The launcher selects an exact-version optional
native dependency for macOS, Windows, or glibc Linux on x64 or arm64. Keep npm
optional dependencies enabled. musl Linux is not supported. Installation uses no
lifecycle scripts and downloads no executable outside the npm packages.

The unscoped `binpm` npm name is unavailable; use `@delino/binpm`.
Version `0.0.0` is a historical name reservation with no executable.

See the [binpm documentation](https://oss.delino.io/binpm) for commands and
Node-free installation through Homebrew, direct installers, or cargo-binstall.
