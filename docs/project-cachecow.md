# Project: CacheCow

## Goal

Prepare independent writable build caches for Git worktrees with filesystem copy-on-write. Reduce redundant disk allocation and preparation work without bypassing build-tool correctness decisions or promising memory savings.

## Project ID

`cachecow`; product name CacheCow; executable `ccow`.

## Domain Ownership Map

| Domain   | Canonical path                   | Ownership                                                                                        |
| -------- | -------------------------------- | ------------------------------------------------------------------------------------------------ |
| cmds     | `cmds/cachecow`                  | Planned Go CLI, cache management, presets, shims, shell integration and native process ownership |
| packages | `packages/ccow`                  | Planned `@delino/ccow` launcher and native distribution tooling                                  |
| apps     | `apps/public-docs/docs/cachecow` | Planned public guides, built and published by public-docs                                        |

These paths are onboarding skeletons. Their existence does not establish runtime support, published artifacts, or native acceptance.

## Domain Contract Documents

- [Complete requirements](cmds-cachecow-requirements.md)
- [Command foundation](cmds-cachecow-foundation.md)
- [npm and native distribution](packages-ccow-distribution-contract.md)
- [Public documentation](apps-cachecow-docs-foundation.md)

## Cross-Domain Invariants

- The complete requirements contract retains all product decisions, acceptance gates, test scenarios, and exclusions. Domain summaries must not weaken them.
- Preset IDs are `cargo`, `rspack`, and `rsbuild`. Explicit execution and activated shims share managed cache and process behavior.
- Native Cargo use has no Node.js dependency. npm and frontend integration require Node.js 24+.
- Repository-owned code and distributions use Apache-2.0. Preserve third-party licenses and notices.
- Storage is local-only, same-user and same-Git-repository. This is an explicit exception to the default R2 storage approach; it adds no remote service or telemetry.
- Use UUID v7 when new persisted entities need identifiers; do not introduce entities solely to use UUIDs.
- Public documentation must distinguish planned behavior, available artifacts, and verified native acceptance. Compilation, fixtures, and packaging alone do not prove native support or performance benefits.
- Actual support floors and acceptance on APFS, Btrfs, reflink-enabled XFS, and ReFS remain defined by the complete requirements contract. No macOS CI allocation is added to PR, main-push, or affected manual validation.

## Change Policy

Update this index, affected domain contracts, and the catalog together for path or interface changes. Keep public guides aligned when user-facing behavior changes. Update AGENTS only for ownership, development procedures, or rules, following the [instruction-update policy](README.md#instruction-update-policy). Record revisions, validation results, unperformed checks, and unresolved limits in PRs and CI artifacts.

## References

- [Repository defaults](repository-defaults.md)
- [Workflow](repository-workflow-contract.md)
- [Licensing](repository-license-contract.md)
- [Public-docs project](project-public-docs.md)
- [Project template](project-template.md)
- [Domain template](domain-template.md)
