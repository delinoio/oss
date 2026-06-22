# apps-binpm-docs-foundation

## Scope
- Project/component: binpm documentation web app contract
- Canonical path: `apps/binpm-docs`
- Canonical production URL: `https://binpm.delino.io`

## Runtime and Language
- Runtime: Rspress static documentation app
- Primary language: Markdown and TypeScript configuration with web build tooling
- Build toolchain: Rspress, aligned with the repository default preference for Rsbuild/Rspress-style static documentation surfaces.
- Deployment target: Cloudflare Pages by default.
- Production host: `https://binpm.delino.io`.

## Users and Operators
- External users reading binpm installation, local tooling, cache, verification, and CLI behavior documentation.
- Internal maintainers publishing and reviewing binpm documentation updates.

## Interfaces and Contracts
- The package name is `binpm-docs`.
- The app is registered through the existing `apps/*` pnpm workspace glob.
- Stable documentation route IDs are `/`, `/installation`, `/getting-started`, `/commands`, `/local-tooling`, `/cache-and-verification`, `/releases`, `/troubleshooting`, and `/reference`.
- Rspress clean URLs are enabled. Stable public route IDs must remain extensionless, each route ID must have a generated build output artifact, and generated internal links must not use `.html` suffixes for those route IDs.
- The development server uses fixed port `46260`.
- Local production preview uses fixed port `46261`.
- The production output directory is `doc_build`.
- The default theme must expose a visible GitHub repository link to `https://github.com/delinoio/oss`, including the top-level GitHub social link and the document-page footer repository link.
- The canonical production URL is `https://binpm.delino.io`; documentation must treat this value as deployment metadata only and must not infer product behavior or published page content from the live site.
- Content must stay aligned with the binpm project and crate contracts, especially source identifiers, local manifest and lockfile behavior, target selection, asset scoring, cache reuse, verification, read-only diagnostics, install finalization, release distribution, direct installers, cargo-binstall metadata, Homebrew installation, and Node-free runtime requirements.
- Content must curate those internal contracts into public guidance and must not document repository-internal implementation details unless the detail is a stable public interface, user-visible behavior, or explicitly public maintainer workflow.
- This app is a documentation surface only. It must not expand binpm runtime behavior, release automation, package-manager backend scope, checksum discovery, signature verification, or global update behavior without corresponding updates to `docs/project-binpm.md` and `docs/crates-binpm-foundation.md`.

## Storage
- Source documentation is versioned in-repo under `apps/binpm-docs/docs`.
- Build artifacts are generated into `apps/binpm-docs/doc_build` and are not source-controlled.
- The app does not introduce user-uploaded files or persistent application data.

## Security
- Published content must not expose internal-only secrets, unpublished release credentials, private CI environment details, or source-provider tokens.
- Published content must not expose internal architecture, operational, CI, or repository-layout details that are not part of a stable public contract.
- Installation guidance must preserve the binpm HTTPS, sanitized URL persistence, cache validation, and `--require-verified` contracts.
- Direct-installer guidance must provide latest and reproducible pinned remote copy-paste POSIX and PowerShell commands using stable first-party `delinoio/oss` raw GitHub URLs, keep canonical in-repo script paths visible for maintainer workflows, present `cosign` as a required prerequisite with official installation guidance before installer commands, and clearly distinguish binpm release artifact verification from verification of packages installed by binpm.
- Installation and release guidance must describe Homebrew as a prebuilt-only binpm channel for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`, and must describe `cargo-binstall` as first-party release-asset-only with quick-install and compile fallbacks disabled.
- Installation, release, troubleshooting, and reference guidance must distinguish first-party binpm binary distribution platforms from the broader third-party package target parsing model.
- Cloudflare Pages deployment credentials must remain managed by CI or hosting configuration, not checked into the repository.
- Published content must be sourced from repository contracts and app documentation, not from assumptions about the current live contents of `https://binpm.delino.io`.

## Logging
- Build and deployment logs should include the workspace name, changed documentation paths, build status, and deployment status.
- Log output must be safe for public CI surfaces.

## Build and Test
- Local validation: `pnpm --filter binpm-docs test`, which builds the Rspress output and runs `scripts/validate-clean-urls.mjs` to verify stable route IDs have build output artifacts and generated internal HTML links use clean public URLs rather than `.html` hrefs.
- Production build: `pnpm --filter binpm-docs build`
- CI alignment: `node-binpm-docs-test`
- App preparation: `pnpm run prepare` invokes `prepare:app`; `binpm-docs` currently has no app-specific preparation step.

## Dependencies and Integrations
- Integrates with the repository pnpm workspace through `apps/*`.
- Integrates with Rspress and its Rsbuild-based static-site pipeline.
- Integrates with Cloudflare Pages for static deployment by default.
- Depends on `docs/project-binpm.md` and `docs/crates-binpm-foundation.md` for canonical binpm product and runtime contracts.

## Change Triggers
- Update `docs/project-binpm.md`, this file, and `apps/AGENTS.md` when the app path, route IDs, theme repository-link surface, validation commands, toolchain, output directory, or deployment target changes.
- Update `docs/crates-binpm-foundation.md` and the relevant app pages when binpm runtime, source, target, local tooling, cache, verification, install, execution, release distribution, installer, diagnostic, or output behavior changes.
- Update `docs/README.md` when adding, renaming, or removing this domain contract.

## References
- `docs/project-binpm.md`
- `docs/crates-binpm-foundation.md`
- `docs/repository-defaults.md`
- `docs/domain-template.md`
