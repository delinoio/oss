# CacheCow distribution tooling

Version 0.1.0 is published on GitHub Releases. Package fixtures, cross-builds and installation checks do not establish native CoW or process-lifecycle support.

CacheCow ships versioned native archives, `SHA256SUMS`, Sigstore bundles and `release.json` on GitHub Releases (tag `ccow@v<version>`). It is not published to npm. The archives contain an independent `ccow` executable and require no Node.js. This private workspace package holds the repository tooling that builds, packages and verifies those archives.

The intended native targets are macOS 15+ arm64/APFS, Linux x64 with an Ubuntu 22.04-equivalent environment on Btrfs or reflink-enabled XFS, and Windows 11 24H2+ x64/ReFS. These targets have not passed native acceptance tests.

Installation does not activate shims or change shell configuration. Rollback means installing an earlier released archive. Homebrew on macOS ARM64 and signed APT/DNF packages named `ccow` on Linux amd64/arm64 consume these releases. These channels remain unavailable until their publication and public installation checks pass. There is no automatic updater or standalone installer.

Repository-owned code and distributions are licensed under Apache-2.0. Report problems through the delinoio/oss GitHub issue tracker.
