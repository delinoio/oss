# Install clibox

## Pin with pnpm

```sh
pnpm add -D -E @delino/clibox
pnpm exec clibox --help
pnpm exec clibox --version
```

Commit your package manifest and lockfile to keep the selected version reproducible.

## Pin with npm

With npm:

```sh
npm install --save-dev --save-exact @delino/clibox
npm exec -- clibox --version
```

Use `clibox` commands in `package.json` scripts, for example `clibox port list 3000`. The [getting started guide](/clibox/getting-started) pins version 0.2.0 and shows its environment execution syntax.

## Requirements

- Node.js 22 or newer.
- macOS or Windows (x64 or arm64), or Linux (x64 or arm64, glibc or musl). GNU builds require glibc 2.34+ on both architectures; Alpine uses the musl builds.
- Optional dependencies enabled in your package manager.

The package manager installs the matching prebuilt executable. Rust, postinstall scripts, and separate binary downloads are not required. You can install with `--ignore-scripts`.

## Linux release archives

Signed GNU Linux archives for x64 and arm64 are available from [clibox releases](https://github.com/delinoio/oss/releases?q=clibox). Select `clibox-linux-amd64.tar.gz` or `clibox-linux-arm64.tar.gz` for your machine. Verify the archive before extracting and running its `clibox` executable; follow [Releases and verification](/clibox/releases). These binaries require glibc 2.34+ and do not require Node.js. Alpine users should use the npm musl package instead of a GNU archive.

## Linux APT and DNF

Native packages are not published yet. After the first native package release, register the stable repository using the [Linux package setup guide](https://oss.delino.io/linux-packages), including its key fingerprint check. Then install with `sudo apt-get install clibox` or `sudo dnf install clibox` and check `clibox --version`. Native installation does not require Node.js. Update with `sudo apt-get install --only-upgrade clibox` or `sudo dnf upgrade clibox`; remove with `sudo apt-get remove clibox` or `sudo dnf remove clibox`. Desktop helpers remain separately installed runtime capabilities.


## Desktop prerequisites

Linux clipboard access needs wl-clipboard on Wayland or xclip on X11; opening a resource needs xdg-utils. Install these separately with your operating system's package manager. HTTPS readiness checks need OS CA certificates, including on Alpine. See [System commands](/clibox/system) for session and memory requirements.

Continue with [Getting started](/clibox/getting-started).
