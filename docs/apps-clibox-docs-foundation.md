# clibox public documentation foundation

## Scope

- Project/component: clibox public user documentation.
- Canonical path: `apps/public-docs/docs/clibox`.

## Runtime and Language

English Markdown built by the existing Rspress `public-docs` workspace. The consolidated Cloudflare Pages deployment publishes the complete `doc_build` tree. No separate package, development port, installer, or hosting project is introduced.

## Users and Operators

Developers using clibox directly or through a project-pinned npm dependency, and maintainers reviewing user-facing documentation.

## Interfaces and Contracts

- Canonical URL: `https://oss.delino.io/clibox`.
- Stable clean routes: `/clibox/`, `/clibox/install`, `/clibox/getting-started`, `/clibox/commands`, `/clibox/fspy`, `/clibox/system`, `/clibox/transformations`, `/clibox/wait`, `/clibox/configuration`, `/clibox/output`, `/clibox/migration`, `/clibox/releases`, and `/clibox/troubleshooting`.
- clibox is a major project alongside Runmoor. Every assembled page offers `DocumentationSiteId.Clibox` as the final shared selector entry. All thirteen clibox routes select it with exactly one `aria-current="page"`, show their complete project sidebar, and retain the repository social and document-footer links. Root top navigation remains empty.
- The selector uses the same relative destinations in production and development, with no hydration-dependent rewriting or retired per-project port mapping. Development uses the consolidated fixed loopback port `46302`.
- The guides cover all 25 commands in published 0.2.0 with syntax, examples, defaults, output formats, exit statuses, cancellation, atomic file replacement, platform prerequisites, privacy boundaries, and operational limits from the Rust and distribution contracts. The readiness guide and both consumer READMEs explicitly document Windows Ctrl+Break cancellation with exit code 130 alongside Ctrl+C. Source builds add seven issue #971 `fspy` commands that remain absent from published 0.2.0 packages until a later manual release. The fspy guide describes the declared synchronous-operation boundary, artifact sensitivity, limits, cancellation, workflow constraints, and unsupported tracing without exposing repository internals. Execution-wrapper guidance makes the Unix process-group ownership boundary clear: foreground workloads and managed services are supported, while programs that daemonize or create another session/process group must manage their own lifecycle. Preserve source-backed detail from both consumer READMEs; those READMEs remain and link to the canonical guide.
- Installation covers exact npm/pnpm pinning on Node.js 22+, all eight platform/libc targets, signed GNU Linux archives, glibc 2.34+, and the shared Linux package guide. APT/DNF remains explicitly unavailable until public installation verification completes. Homebrew guidance covers macOS Intel/Apple Silicon via `delinoio/tap/clibox` from the next release, with install, version, upgrade and uninstall commands. Keep its pending first-publication status explicit until public installation succeeds; do not imply Linux Homebrew support. No crates.io or new hosted installer is promised.
- Published version 0.2.0 includes `run env`, the five `run with-*` wrappers, and `system cpus`, while 0.1.6 used `env run`. Reference guides follow the published 0.2.0 interface; getting-started pins 0.2.0 and uses its runnable syntax. Keep the unreleased fspy distinction in both READMEs and migration/release guidance without changing product versioning or publishing as part of documentation work.
- Dotenv guidance distinguishes the removable `export` prefix (immediate ASCII space followed by another assignment key) from the ordinary `export` key with optional assignment whitespace, including tabs. Keep these examples aligned with both READMEs and the parser contract.
- Real GUI, clipboard persistence, and application-wait observations remain unverified follow-up work. Mocked and automated coverage must not imply those observations were completed.

### Project requirements

- Public clibox documentation is owned by `apps/public-docs/docs/clibox` at `https://oss.delino.io/clibox`, a major project alongside Runmoor in the shared selector. Follow `apps-clibox-docs-foundation.md`; synchronize public behavior with native/npm guides and keep release internals in `docs/`.

### Application integration

- clibox Homebrew guidance is macOS x64/arm64 only through `delinoio/tap/clibox`. Keep the next-release availability notice until first public installation is verified; document install, version, upgrade and uninstall without exposing release internals.

- `apps/public-docs/docs/clibox` owns the thirteen English public guide routes in `apps-clibox-docs-foundation.md`, including `/clibox/fspy`, with `https://oss.delino.io/clibox` as the canonical destination. Use the existing public-docs build, deployment, theme, and fixed development port.

- Expose clibox as a peer of Runmoor in the shared project selector, with every clibox route in its desktop/mobile sidebar and visible repository social/footer links. Keep the root top navbar empty.

- Explain the dotenv `export` prefix separately from the ordinary `export` assignment key, including the immediate ASCII-space boundary.

- Preserve all 25 commands in published 0.2.0, input/output limits, file-publication behavior, cancellation, platform prerequisites, migration, and verification limits from the clibox contracts and READMEs. Document that Unix execution-wrapper ownership is limited to its process group, so daemonizing workloads and managed services must manage their own lifecycle. Keep the seven issue #971 file-access workflows identified as source-only until a later manual release, with a public guide to their behavior and limits. Do not claim unpublished APT/DNF availability or describe already released syntax as a future feature.

- Validate every clibox route, required article heading/link, exact selector state, sidebar link, and repository region. Apply clean-URL, credential, and private-path checks to clibox HTML and shared stylesheets, with regression fixtures for removed links and rejected content.

- When command or installation behavior changes, synchronize these guides, both clibox READMEs, and the clibox project/domain contracts.

- Native package documentation must distinguish implemented release integration from published availability. Runmoor `0.1.3` native packages have passed public installation verification on all package distribution/architecture pairs; executable installation/update/removal examples must use the published Runmoor package. Keep each other CLI package-manager example unavailable until its own public installation verification completes. Package compatibility does not expand runtime support: the Runmoor manager requires Ubuntu 22.04+ on Linux, and non-Ubuntu package checks cover installation and version/help only. Runmoor and clibox use stable.

- The owner-authorized 2026-10-04 amendment in `project-pnport.md` permits exactly stable 0.1.0 publication with the recorded macOS initialization/SIGHUP failures, separate root clibox watch failure and full-acceptance review deferred. This version-specific exception takes precedence over earlier full-acceptance prerequisites; it does not establish a cause fix or passing skipped checks. Require `pnportReleaseReady: true` plus an exact `pnportReleaseVersion` match; version coordination preserves both fields. Retain all final four-native candidate execution/install/TypeScript/benchmark, integrity, signing, native-before-launcher and immutable-retry gates. New failures still block publication. Keep #958 open, preserve Windows 0.2.0 and immutable 0.1.0-next.1, and disclose unresolved user-facing limits. Remove the stable unreleased notice only after verified publication.

### Rust component integration

- Keep the CLI README and `apps/public-docs/docs/clibox` aligned with user-facing behavior. Follow `apps-clibox-docs-foundation.md`; the consolidated public guide covers the 25 commands published in 0.2.0 and marks issue #971 fspy workflows as source-only until a separate release.

## Storage

Markdown is committed source. Static output is ignored generated `apps/public-docs/doc_build` content. The documentation adds no user state, telemetry, or credentials.

## Security

Public pages describe supported user behavior, not repository architecture, private paths, or release operations. Public archive names, checksum/signature verification and exact signer identity are supported consumer interfaces. Validators reject credentials and internal paths in text, comments and resources, including encoded URLs and raw shared CSS comments/custom properties as well as its parsed resource URLs. clibox path exceptions match complete public routes, not arbitrary paths under a project prefix.

## Logging

Validation reports the route or output file and a safe classification, without echoing rejected credential or path values. Build logs identify the documentation app and its generated routes.

## Build and Test

- Run `pnpm dev:public-docs` for local development and `pnpm --filter public-docs build` for production output.
- Run `pnpm test` in `apps/public-docs`. The existing test boundary covers selector interaction/path mapping, a complete site build, clean URLs/content checks, and project route/navigation validation. Negative fixtures use temporary generated output, never tracked content.
- Check all thirteen pages for expected article headings and links, correct selected site, sidebar completeness and independently present repository regions. Exercise missing navigation, incorrect selection, credentials, private paths and non-clean links.
- Inspect desktop and mobile layouts and navigation. `node-public-docs-test` already selects changes to this content root and shared selector; no new CI job or deployment is needed.

## Dependencies and Integrations

Uses existing Rspress, `packages/docs-site-switcher`, the shared Linux package guide and the single public-docs Cloudflare Pages deployment. The DevHud release continues to rebuild current aggregate docs and overlay only DevHud-owned assets, preserving clibox content.

## Change Triggers

Update this contract, `docs/project-clibox.md`, the clibox Rust/distribution contracts, public-docs project/app contracts, selector contract, docs catalog and relevant AGENTS rules when public behavior, routes or ownership change. Keep both consumer READMEs and public guides synchronized without changing CLI behavior or distribution channels in documentation-only changes.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References

- `docs/project-clibox.md`
- `docs/crates-clibox-foundation.md`
- `docs/packages-clibox-distribution-contract.md`
- `docs/project-public-docs.md`
- `docs/apps-public-docs-foundation.md`
- `docs/packages-docs-site-switcher-contract.md`
- `docs/repository-defaults.md`
