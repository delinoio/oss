# CacheCow

Version 0.1.0 is unreleased. Package fixtures and cross-builds do not establish native support or release availability.

`@delino/ccow` exposes `ccow` and requires Node.js 24+. Its exact-version optional native package must be installed. Native archives contain an independent executable and require no Node.js.

The intended native targets are macOS 15+ arm64/APFS, Linux x64 with an Ubuntu 22.04-equivalent environment on Btrfs or reflink-enabled XFS, and Windows 11 24H2+ x64/ReFS. Actual minimum-platform acceptance remains required.

Installation does not activate shims or change shell configuration. Rollback means reinstalling an earlier released version. There is no automatic updater, Homebrew package, or standalone installer.

Repository-owned code and distributions are licensed under Apache-2.0. Report problems through the delinoio/oss GitHub issue tracker.
