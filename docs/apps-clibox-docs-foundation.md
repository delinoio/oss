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
- The guides cover all 25 commands in published 0.2.0 with syntax, examples, defaults, output formats, exit statuses, cancellation, atomic file replacement, platform prerequisites, privacy boundaries, and operational limits from the Rust and distribution contracts. Source builds add seven issue #971 `fspy` commands that remain absent from published 0.2.0 packages until a later manual release. The fspy guide describes the declared synchronous-operation boundary, artifact sensitivity, limits, cancellation, workflow constraints, and unsupported tracing without exposing repository internals. Execution-wrapper guidance makes the Unix process-group ownership boundary clear: foreground workloads and managed services are supported, while programs that daemonize or create another session/process group must manage their own lifecycle. Preserve source-backed detail from both consumer READMEs; those READMEs remain and link to the canonical guide.
- Installation covers exact npm/pnpm pinning on Node.js 22+, all eight platform/libc targets, signed GNU Linux archives, glibc 2.34+, and the shared Linux package guide. APT/DNF remains explicitly unavailable until public installation verification completes. No Homebrew, crates.io, or new hosted installer is promised.
- Published version 0.2.0 includes `run env`, the five `run with-*` wrappers, and `system cpus`, while 0.1.6 used `env run`. Reference guides follow the published 0.2.0 interface; getting-started pins 0.2.0 and uses its runnable syntax. Keep the unreleased fspy distinction in both READMEs and migration/release guidance without changing product versioning or publishing as part of documentation work.
- Dotenv guidance distinguishes the removable `export` prefix (immediate ASCII space followed by another assignment key) from the ordinary `export` key with optional assignment whitespace, including tabs. Keep these examples aligned with both READMEs and the parser contract.
- Real GUI, clipboard persistence, and application-wait observations remain unverified follow-up work. Mocked and automated coverage must not imply those observations were completed.

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

## References

- `docs/project-clibox.md`
- `docs/crates-clibox-foundation.md`
- `docs/packages-clibox-distribution-contract.md`
- `docs/project-public-docs.md`
- `docs/apps-public-docs-foundation.md`
- `docs/packages-docs-site-switcher-contract.md`
- `docs/repository-defaults.md`
