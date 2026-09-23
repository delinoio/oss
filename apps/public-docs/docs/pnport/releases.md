# Releases and rollback

**pnport 0.1.0 is not published.** There is no preview release, public native package, npm package, installer, or Homebrew formula to select or roll back to today.

## Release readiness

A first release requires validated native execution and installation on macOS, Windows, and glibc Linux for x64 and arm64. Linux static child execution, filesystem and process conformance, complete native/package artifacts, and documentation are release gates. A passing development fixture on one host is not complete support evidence.

When available, a release will use the `pnport@v<MAJOR.MINOR.PATCH>` identity. Its native and npm versions must match. Published GitHub archives will include the executable and adjacent interception library, `SHA256SUMS`, and Sigstore bundles. Verify the signed checksum manifest against the release identity and exact source commit before comparing an archive's SHA-256 digest; a checksum downloaded next to an archive alone does not establish publisher identity. The npm launcher will select an exact-version native package rather than download, compile, or fall back to another executable at runtime. Check [installation and availability](/pnport/installation) before using any distribution method.

## Explicit updates and rollback

pnport will not check for or install updates automatically. After verified versions exist, update by choosing a specific published version and its matching native/npm artifacts. To roll back, explicitly install the earlier verified version, then confirm it with `pnport --version` and `pnport doctor` before restarting work. Preserve the project's Yarn installation and lockfile unless you deliberately change them.

Cache formats from different versions are designed to coexist without destructive migration. Cache cleanup is a separate explicit action; see [cache management](/pnport/cache). Compatibility of command names and diagnostic meanings is intended throughout 0.1.x; a later breaking change requires migration guidance.

Follow [Delino OSS GitHub Releases](https://github.com/delinoio/oss/releases) for actual published artifacts, and use [GitHub Issues](https://github.com/delinoio/oss/issues) for support. Do not treat this page as evidence that a release already exists.
