# React Forge npm Release Contract

## Packages and runtime

`packages/react-forge` is a private source workspace. Release assembly produces public `@delino/react-forge` and six native packages named `@delino/react-forge-<host-id>`, with IDs from `src/native-platforms.json`. The main package pins all six as exact-version optional dependencies. Native package manifests declare their OS, CPU and Linux glibc constraints; the workspace manifest has no OS/CPU filters. Every package declares Apache-2.0 and includes its complete terms and the root copyright NOTICE. The main package carries only compiled JavaScript, declarations, CLI, README, license and NOTICE; each native package carries one matching binding, README, license and NOTICE. No package runs an install hook, compiles or downloads a binary at runtime. A missing or version-mismatched optional binding fails with a typed I/O error after supported-host validation.

## Source and candidate identity

Release Project prepares a version-only `packages/react-forge/package.json` commit and `react-forge@v<version>` tag. From `0.0.0`, only a minor bump to `0.1.0` is valid. CLI and reconciler versions come from that manifest. The tag workflow builds and smoke-tests on macOS, Windows and glibc Linux, each on x64 and arm64, with Node 24 and the pinned Rust toolchain. Its assembler requires exactly six native tarballs and one main tarball at the same version and source commit. It verifies exact manifests, allowlisted files, native binary headers, Apache license and NOTICE bytes and SHA-512 npm integrity without rewriting downloaded candidates. PR CI runs installed consumers on each host and assembles the complete set without publishing. Generated `dist` remains ignored and is removed from worktrees after validation.

## First registration and subsequent publishing

The first `0.1.0` tag produces a retained, validated seven-tarball candidate. The npm owner publishes those exact tarballs interactively with 2FA, native packages first and the main package last, using public access. If interrupted, inspect registry integrity before publishing any remaining tarball; a conflicting immutable version is an error. Register `delinoio/oss` and `release-react-forge.yml` as Trusted Publisher for all seven npm names, permitting direct `npm publish`. Rerun the tag workflow to verify all seven remote integrities. Its initial publish job deliberately fails with `bootstrap_required` until all seven match. No token is stored in CI for bootstrap.

For later exact tags, only the guarded publish job receives GitHub Actions OIDC. It inspects the complete remote set before the first upload, publishes native packages before the main package, confirms each registry integrity before advancing, and reuses identical published versions on retry. A conflicting version or incomplete confirmation fails without repacking or retagging. Manual workflow dispatch defaults to a credential-free dry run and cannot publish from a branch. The package names and Trusted Publisher mapping must be provisioned before the first automated version.

## Validation and limits

Run the package build, tests and installed consumer on all six hosts; the aggregate job validates all seven tarballs and publication dry run. Run release coordinator tests and workflow checks for version-only commits, exact tag authority, bootstrap behavior, integrity conflict and retry. Rust crates remain private and unpublished; no public documentation hosting or GitHub Release is added. Existing immutable npm versions retain their historical license and bytes.
