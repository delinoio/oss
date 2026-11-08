# Project: CacheCow

## Goal

Prepare independent writable build caches for Git worktrees with filesystem copy-on-write. Reduce redundant disk allocation and preparation work without bypassing build-tool correctness decisions or promising memory savings.

## Project ID

`cachecow`; product name CacheCow; executable `ccow`.

## Domain Ownership Map

| Domain   | Canonical path                   | Ownership                                                                                           |
| -------- | -------------------------------- | --------------------------------------------------------------------------------------------------- |
| crates   | `crates/cachecow`                | Rust CLI (`ccow`), cache management, presets, shims, shell integration and native process ownership |
| packages | `packages/ccow`                  | Native archives, Homebrew and APT/DNF packaging and verification tooling (private; no npm package)  |
| apps     | `apps/public-docs/docs/cachecow` | Public guides, built and published by public-docs                                                   |

Implementation and packaging do not establish published availability or native acceptance. CI and the release workflow run no native CoW or process-lifecycle acceptance, and publication is not gated on it. The release validates Homebrew installation and the shared APT/DNF installation matrix.

## Domain Contract Documents

- [Complete requirements](crates-cachecow-requirements.md)
- [Command foundation](crates-cachecow-foundation.md)
- [Native distribution](packages-ccow-distribution-contract.md)
- [Public documentation](apps-cachecow-docs-foundation.md)

## Cross-Domain Invariants

- The complete requirements contract retains all product decisions, acceptance criteria, test scenarios, and exclusions. Domain summaries must not weaken them.
- Preset IDs are `cargo`, `rspack`, and `rsbuild`. Explicit execution and activated shims share managed cache and process behavior.
- Native Cargo use and the native release archives have no Node.js dependency. Only the Rspack/Rsbuild frontend integration requires Node.js 24+.
- CacheCow is distributed as versioned native archives, `SHA256SUMS`, Sigstore bundles and `release.json` through GitHub Releases, a macOS ARM64 Homebrew formula, and signed stable APT/DNF packages named `ccow` on Linux amd64 and arm64. Package installation coverage does not expand native CoW acceptance. It has no npm package, launcher or npm credential.
- Repository-owned code and distributions use Apache-2.0. Preserve third-party licenses and notices.
- Storage is local-only, same-user and same-Git-repository. This is an explicit exception to the default R2 storage approach; it adds no remote service or telemetry.
- Use UUID v7 when new persisted entities need identifiers; do not introduce entities solely to use UUIDs.
- Public documentation must distinguish planned behavior, available artifacts, and verified native acceptance. Compilation, fixtures, and packaging alone do not prove native support or performance benefits.
- Support floors on APFS, Btrfs, reflink-enabled XFS, and ReFS remain defined by the complete requirements contract. Native acceptance on them does not run in CI. No macOS CI allocation is added to PR, main-push, or affected manual validation.

## Change Policy

Update this index, affected domain contracts, and the catalog together for path or interface changes. Keep public guides aligned when user-facing behavior changes. Update AGENTS only for ownership, development procedures, or rules, following the [instruction-update policy](README.md#instruction-update-policy). Record revisions, validation results, unperformed checks, and unresolved limits in PRs and CI artifacts.

## References

- [Repository defaults](repository-defaults.md)
- [Workflow](repository-workflow-contract.md)
- [Licensing](repository-license-contract.md)
- [Public-docs project](project-public-docs.md)
- [Project template](project-template.md)
- [Domain template](domain-template.md)
