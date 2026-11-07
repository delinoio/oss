# CacheCow distribution tooling

Version 0.1.0 is unreleased. Package fixtures and cross-builds do not establish native support or release availability.

CacheCow ships only as versioned native archives, `SHA256SUMS` and `release.json` on GitHub Releases (tag `ccow@v<version>`). It is not published to npm. The archives contain an independent `ccow` executable and require no Node.js. This private workspace package holds the repository tooling that builds, packages and verifies those archives.

The intended native targets are macOS 15+ arm64/APFS, Linux x64 with an Ubuntu 22.04-equivalent environment on Btrfs or reflink-enabled XFS, and Windows 11 24H2+ x64/ReFS. These targets have not passed native acceptance tests.

Installation does not activate shims or change shell configuration. Rollback means installing an earlier released archive. There is no automatic updater, Homebrew package, or standalone installer.

Repository-owned code and distributions are licensed under Apache-2.0. Report problems through the delinoio/oss GitHub issue tracker.
