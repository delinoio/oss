# Releases and verification

## Choose a version

Use the [npm package](https://www.npmjs.com/package/@delino/clibox) and [clibox GitHub releases](https://github.com/delinoio/oss/releases?q=clibox) to select a published version. GitHub release tags use `clibox@v<version>`. The main npm package and its selected native package must have the same exact version.

For example, pin the published 0.2.0 version with either package manager:

```sh
pnpm add -D -E @delino/clibox@0.2.0
npm install --save-dev --save-exact @delino/clibox@0.2.0
```

Run `clibox --version` through that project's package manager and commit the manifest and lockfile. Review [Migration](/clibox/migration) when upgrading scripts using older names. Version 0.2.0 includes `run env`, the five `run with-*` execution wrappers, and `system cpus`. The seven `fspy` workflows are implemented in source for a future release and are absent from 0.2.0. Use the version-specific examples in [Getting started](/clibox/getting-started).

## Distribution and verification

npm distributes prebuilt executables for macOS/Windows x64 and arm64 and Linux x64/arm64 with glibc or musl. Keep optional dependencies enabled. Installation needs no Rust compiler, installation script, or runtime binary download. Registry integrity and the lockfile bind the selected package bytes; npm publication provides provenance.

GNU Linux GitHub releases contain `clibox-linux-amd64.tar.gz`, `clibox-linux-arm64.tar.gz`, `SHA256SUMS`, and Sigstore bundles. Download assets from the same release. Before running a binary, verify the archive and checksum-file bundles using a trusted installation of Cosign, the exact signing identity below, and the GitHub Actions OIDC issuer. This example uses version 0.2.0 and the x64 archive:

```sh
cosign verify-blob --bundle clibox-linux-amd64.tar.gz.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/delinoio/oss/\.github/workflows/release-clibox\.yml@(?:refs/tags/clibox@v0\.2\.0|refs/heads/main)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-github-workflow-repository delinoio/oss \
  --certificate-github-workflow-sha 825d85de25f813f78214fabfd9c0dda871f7e13f \
  clibox-linux-amd64.tar.gz
cosign verify-blob --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/delinoio/oss/\.github/workflows/release-clibox\.yml@(?:refs/tags/clibox@v0\.2\.0|refs/heads/main)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-github-workflow-repository delinoio/oss \
  --certificate-github-workflow-sha 825d85de25f813f78214fabfd9c0dda871f7e13f \
  SHA256SUMS
```

The identity accepts the release tag or the official main-branch signer, always bound to the exact release source commit. For another version, use its tag and independently resolve its full commit from the official repository before replacing the SHA above; do not take the expected commit from the downloaded bundle. Use the `arm64` asset name when appropriate. Compare the archive's SHA-256 digest with its entry in the authenticated `SHA256SUMS`; on Linux, `sha256sum --ignore-missing --check SHA256SUMS` checks the archives downloaded into that directory. Require a successful verification of the selected archive. Do not treat a checksum alone as publisher authentication, and do not use an unverified downloaded executable to verify itself.

## Native package availability

APT/DNF integration uses the stable channel, but native packages are not published yet. Follow [Linux package setup](/linux-packages) only after the package's first public installation verification. GNU archives require glibc 2.34 or newer; Alpine uses npm musl packages. Homebrew and crates.io installation are not supported.

## Use an earlier version

Replace the version in the exact npm/pnpm installation command with the earlier published version you intend to use, then commit the updated lockfile. Except for `run with-lock` and `run with-rate-limit`, clibox stores no application state to migrate. Those wrappers retain private local coordination state, which can affect later invocations or reject an incompatible rate configuration after reinstall. Stop every affected invocation before deciding to remove that state; clibox provides no cleanup command. Downgrading changes command behavior; it cannot restore overwritten files, clipboard contents, terminated processes, or other completed effects. Keep your own backups when replacing files.
