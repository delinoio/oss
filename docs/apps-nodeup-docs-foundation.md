# apps-nodeup-docs-foundation

## Scope
- Project/component: Nodeup documentation web app contract
- Canonical path: `apps/nodeup-docs`

## Runtime and Language
- Runtime: Rspress static documentation app
- Primary language: Markdown and TypeScript configuration with web build tooling
- Build toolchain: Rspress, aligned with the repository default preference for Rsbuild/Rspress-style static documentation surfaces.
- Deployment target: Cloudflare Pages by default.

## Users and Operators
- External users reading Nodeup installation, runtime, and CLI behavior documentation
- Internal maintainers publishing and reviewing Nodeup documentation updates

## Interfaces and Contracts
- The package name is `nodeup-docs`.
- The app is registered through the existing `apps/*` pnpm workspace glob.
- The canonical production URL is `https://nodeup.delino.io`.
- Stable documentation route IDs are `/`, `/installation`, `/getting-started`, `/commands`, `/runtime-resolution`, `/shims-and-package-managers`, `/output`, `/completions`, `/releases`, `/troubleshooting`, and `/reference`.
- Stable public direct-installer file entrypoints are `/install.sh` and `/install.ps1`.
- The development server uses fixed port `46250`.
- Local production preview uses fixed port `46251`.
- Fixed-port dev and preview commands must preflight port availability and print actionable recovery steps when a listener already owns the requested port. Temporary local overrides are supported through `NODEUP_DOCS_DEV_PORT` and `NODEUP_DOCS_PREVIEW_PORT`; those overrides do not change the canonical defaults or CI validation behavior.
- The production output directory is `doc_build`.
- The default theme must expose a visible GitHub repository link to `https://github.com/delinoio/oss`, including the top-level GitHub social link and the document-page footer repository link.
- The docs theme must preserve keyboard accessibility for generated navigation controls: mobile documentation navigation closes on `Escape`, returns focus to its opener, keeps closed sidebar links out of the tab order, uses a labeled button for mobile search, avoids redundant ambiguous hamburger labels, keeps search overlays clear of the sticky header, removes decorative heading anchors from sequential keyboard navigation, and keeps Markdown tables horizontally readable on mobile viewports.
- Content must stay aligned with the Nodeup project and crate contracts, especially installation method selection, direct-installer current and pinned command patterns, release verification, supported host targets, x64/amd64 release asset terminology, command behavior, linked-runtime lifecycle and executable validation, linked-runtime per-shim command availability diagnostics, runtime resolution precedence, shim behavior, Windows shim alias extension behavior versus delegated runtime `.cmd` package-manager executables, shell completions and shell-specific completion installation guidance, invalid subcommand-scope guidance, package-manager resolution, `nodeup run` versus managed-shim install-on-demand behavior, human/JSON output contracts, parser-error envelope behavior, PATH/PATHEXT troubleshooting guidance, and color-control precedence.
- Content must curate those internal contracts into public guidance and must not document repository-internal implementation details unless the detail is a stable public interface, user-visible behavior, or explicitly public maintainer workflow.

## Storage
- Source documentation is versioned in-repo under `apps/nodeup-docs/docs`.
- Build artifacts are generated into `apps/nodeup-docs/doc_build` and are not source-controlled.
- The app does not introduce user-uploaded files or persistent application data.

## Security
- Published content must not expose internal-only secrets, unpublished release credentials, or private CI environment details.
- Published content must not expose internal architecture, operational, CI, or repository-layout details that are not part of a stable public contract.
- Installation guidance must preserve the Nodeup direct-installer verification contract for `SHA256SUMS` and Sigstore bundle sidecars and must explain that legacy `.sig` or `.pem` sidecars do not satisfy the direct-installer bundle requirement.
- Installation guidance must include a chooser that states when to use Homebrew, direct installers, `cargo-binstall`, and binpm.
- Direct-installer guidance must provide remote copy-paste POSIX and PowerShell commands using `https://nodeup.delino.io/install.sh` and `https://nodeup.delino.io/install.ps1`, preserve current raw GitHub examples using stable first-party `delinoio/oss` raw GitHub URLs, include tag/commit-pinned raw GitHub command patterns for reproducible automation, keep canonical in-repo script paths visible for maintainer workflows, present `cosign` as a required prerequisite before direct installer commands, and distinguish missing prerequisite failures from missing release material and verification failures.
- Installation, release, and troubleshooting guidance must explain that Nodeup `cargo-binstall` support uses first-party release assets only and does not enable `quick-install` or `compile` fallback strategies.
- Cloudflare Pages deployment credentials must remain managed by CI or hosting configuration, not checked into the repository.

## Logging
- Build and deployment logs should include the workspace name, changed documentation paths, build status, and deployment status.
- Log output must be safe for public CI surfaces.

## Build and Test
- Local validation: `pnpm --filter nodeup-docs test`, which builds the Rspress output, verifies documented route IDs are emitted as extensionless links rather than `.html` hrefs, and verifies the public installer files are emitted.
- Production build: `pnpm --filter nodeup-docs build`
- CI alignment: `node-nodeup-docs-test`
- App preparation: `pnpm run prepare` invokes `prepare:app`; `nodeup-docs` currently has no app-specific preparation step.

## Dependencies and Integrations
- Integrates with the repository pnpm workspace through `apps/*`.
- Integrates with Rspress and its Rsbuild-based static-site pipeline.
- Integrates with Cloudflare Pages for static deployment by default.
- Depends on `docs/project-nodeup.md` and `docs/crates-nodeup-foundation.md` for canonical Nodeup product and runtime contracts.

## Change Triggers
- Update `docs/project-nodeup.md`, this file, and `apps/AGENTS.md` when the app path, route IDs, theme repository-link surface, validation commands, toolchain, output directory, or deployment target changes.
- Update `docs/crates-nodeup-foundation.md` and the relevant app pages when Nodeup runtime, release, installer, shim, completion, package-manager, or color-control behavior changes.
- Update `docs/README.md` when adding, renaming, or removing this domain contract.

## References
- `docs/project-nodeup.md`
- `docs/crates-nodeup-foundation.md`
- `docs/repository-defaults.md`
- `docs/domain-template.md`
