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
- The development server uses fixed port `46250`.
- Local production preview uses fixed port `46251`.
- The production output directory is `doc_build`.
- The default theme must expose a visible GitHub repository link to `https://github.com/delinoio/oss`, including the top-level GitHub social link and the document-page footer repository link.
- Content must stay aligned with the Nodeup project and crate contracts, especially installation and verification flows, release verification, supported host targets, command behavior, linked-runtime lifecycle and executable validation, runtime resolution precedence, shim behavior, shell completions, package-manager resolution, human/JSON output contracts, parser-error envelope behavior, and color-control precedence.

## Storage
- Source documentation is versioned in-repo under `apps/nodeup-docs/docs`.
- Build artifacts are generated into `apps/nodeup-docs/doc_build` and are not source-controlled.
- The app does not introduce user-uploaded files or persistent application data.

## Security
- Published content must not expose internal-only secrets, unpublished release credentials, or private CI environment details.
- Installation guidance must preserve the Nodeup direct-installer verification contract for `SHA256SUMS` and Sigstore bundle sidecars.
- Direct-installer guidance must provide remote copy-paste POSIX and PowerShell commands using stable first-party `delinoio/oss` raw GitHub URLs, keep canonical in-repo script paths visible for maintainer workflows, and present `cosign` as a required prerequisite.
- Cloudflare Pages deployment credentials must remain managed by CI or hosting configuration, not checked into the repository.

## Logging
- Build and deployment logs should include the workspace name, changed documentation paths, build status, and deployment status.
- Log output must be safe for public CI surfaces.

## Build and Test
- Local validation: `pnpm --filter nodeup-docs test`, which builds the Rspress output and verifies documented route IDs are emitted as extensionless links rather than `.html` hrefs.
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
