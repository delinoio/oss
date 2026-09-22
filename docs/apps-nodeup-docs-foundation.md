# Nodeup public documentation foundation

## Scope
- Project/component: Nodeup documentation content contract
- Canonical path: `apps/public-docs/docs/nodeup`

## Runtime and Language
- Runtime: Rspress static documentation app
- Primary language: Markdown and TypeScript configuration with web build tooling
- Build toolchain: Rspress, aligned with the repository default preference for Rsbuild/Rspress-style static documentation surfaces.
- Deployment target: the `apps/public-docs` static output published by the consolidated Cloudflare Pages site.

## Users and Operators
- External users reading Nodeup installation, runtime, and CLI behavior documentation
- Internal maintainers publishing and reviewing Nodeup documentation updates

## Interfaces and Contracts
- The content is built by the `public-docs` Rspress application; there is no standalone Nodeup documentation package or workspace.
- The canonical production URL is `https://oss.delino.io/nodeup`.
- Stable documentation route IDs are `/nodeup/`, `/nodeup/installation`, `/nodeup/getting-started`, `/nodeup/commands`, `/nodeup/runtime-resolution`, `/nodeup/shims-and-package-managers`, `/nodeup/output`, `/nodeup/completions`, `/nodeup/releases`, `/nodeup/troubleshooting`, and `/nodeup/reference`.
- Stable public direct-installer file entrypoints are `/install.sh` and `/install.ps1`.
- The assembled public direct-installer entrypoints are `/nodeup/install.sh` and `/nodeup/install.ps1`; their bytes remain sourced from the canonical maintained installer scripts.
- The consolidated documentation development server binds to loopback on fixed port `46302` and owns the Nodeup section alongside the other project sections.
- The production output directory is `apps/public-docs/doc_build`.
- The default theme must expose a visible GitHub repository link to `https://github.com/delinoio/oss`, including the top-level GitHub social link and the document-page footer repository link.
- The docs theme must preserve keyboard accessibility for generated navigation controls: mobile documentation navigation closes on `Escape`, returns focus to its opener, keeps closed sidebar links out of the tab order, uses a labeled button for mobile search, avoids redundant ambiguous hamburger labels, keeps search overlays clear of the sticky header, removes decorative heading anchors from sequential keyboard navigation, and keeps Markdown tables horizontally readable on mobile viewports.
- Content must stay aligned with the Nodeup project and crate contracts, especially installation method selection, direct-installer current and pinned command patterns, release verification, supported host targets, x64/amd64 release asset terminology, command behavior, linked-runtime lifecycle and executable validation, linked-runtime per-shim command availability diagnostics, runtime resolution precedence, shim behavior, Windows shim alias extension behavior versus delegated runtime `.cmd` package-manager executables, shell completions and shell-specific completion installation guidance, invalid subcommand-scope guidance, package-manager resolution, `nodeup run` versus managed-shim install-on-demand behavior, human/JSON output contracts, parser-error envelope behavior, PATH/PATHEXT troubleshooting guidance, and color-control precedence.
- Content must curate those internal contracts into public guidance and must not document repository-internal implementation details unless the detail is a stable public interface, user-visible behavior, or explicitly public maintainer workflow.

## Storage
- Source documentation is versioned in-repo under `apps/public-docs/docs/nodeup`.
- Build artifacts are generated into `apps/public-docs/doc_build` and are not source-controlled.
- The app does not introduce user-uploaded files or persistent application data.

## Security
- Published content must not expose internal-only secrets, unpublished release credentials, or private CI environment details.
- Published content must not expose internal architecture, operational, CI, or repository-layout details that are not part of a stable public contract.
- Installation guidance must preserve the Nodeup direct-installer verification contract for `SHA256SUMS`.
- Installation guidance must include a chooser that states when to use Homebrew, direct installers, `cargo-binstall`, and binpm.
- Direct-installer guidance must provide remote copy-paste POSIX and PowerShell commands using `https://oss.delino.io/nodeup/install.sh` and `https://oss.delino.io/nodeup/install.ps1`, preserve current raw GitHub examples using stable first-party `delinoio/oss` raw GitHub URLs, include tag/commit-pinned raw GitHub command patterns for reproducible automation, keep canonical in-repo script paths visible for maintainer workflows, describe checksum verification through `SHA256SUMS`, and distinguish unsupported-host, missing-release-material, and checksum-verification failures.
- Installation, release, and troubleshooting guidance must explain that Nodeup `cargo-binstall` support uses first-party release assets only and does not enable `quick-install` or `compile` fallback strategies.
- The consolidated `public-docs` publisher owns Cloudflare Pages credentials and the production tree; Nodeup content must not require a standalone hosting credential or deployment.

## Logging
- Build and deployment logs should include the workspace name, changed documentation paths, build status, and deployment status.
- Log output must be safe for public CI surfaces.

## Build and Test
- Development: `pnpm --filter public-docs dev`.
- Local validation: `pnpm --filter public-docs test`, which builds the consolidated Rspress output, validates the Nodeup route artifacts and clean links, and verifies the public installer files.
- Production build: `pnpm --filter public-docs build`.
- CI alignment: `node-public-docs-test`.

## Dependencies and Integrations
- Integrates with the `apps/public-docs` pnpm workspace.
- Integrates with Rspress and its Rsbuild-based static-site pipeline.
- Integrates directly with the `public-docs` build and its Cloudflare Pages publication.
- Depends on `docs/project-nodeup.md` and `docs/crates-nodeup-foundation.md` for canonical Nodeup product and runtime contracts.

## Change Triggers
- Update `docs/project-nodeup.md`, this file, and `apps/AGENTS.md` when the content path, route IDs, theme repository-link surface, validation commands, toolchain, output directory, or publication target changes.
- Update `docs/crates-nodeup-foundation.md` and the relevant app pages when Nodeup runtime, release, installer, shim, completion, package-manager, or color-control behavior changes.
- Update `docs/README.md` when adding, renaming, or removing this domain contract.

## References
- `docs/project-nodeup.md`
- `docs/crates-nodeup-foundation.md`
- `docs/repository-defaults.md`
- `docs/domain-template.md`

## Native package guidance

The public installation surface documents the exact repository key fingerprint, supported Linux distribution/architecture matrix, stable or explicit preview registration, and package-manager install/update/remove commands. Operational details remain in `docs/repository-linux-packages-contract.md`. Installation guidance must preserve the existing release-archive and other supported installation methods.
