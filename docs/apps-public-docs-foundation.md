# apps-public-docs-foundation

## Scope
- Project/component: public documentation web app contract
- Canonical path: `apps/public-docs`

## Runtime and Language
- Runtime: Rspress static documentation app
- Primary language: Markdown/TypeScript content and configuration with web build tooling
- Production deployment target: Cloudflare Pages static output
- The development server binds to loopback on fixed port `46302`, rejects host overrides, preflights availability, and exits on conflicts without automatically selecting another port.

## Users and Operators
- External users reading public product documentation
- Internal maintainers publishing and reviewing docs updates

## Interfaces and Contracts
- Rspress route, navigation, and sidebar contracts in `apps/public-docs/rspress.config.ts` must remain stable.
- Documentation sources live in `apps/public-docs/docs`; the production output directory is `apps/public-docs/doc_build` and is not source-controlled.
- The publication root owns Cloudflare Pages control files. Its `_headers` contains the path-scoped security headers for `/async-commit-hook/*`; package-local `_headers` files remain package inputs but do not replace the root rule.
- Rspress clean URLs are enabled. Stable route IDs owned directly by this app are `/`, `/getting-started`, `/projects-overview`, `/documentation-lifecycle`, `/linux-packages`, `/devhud`, `/devhud/install`, `/devhud/guide`, `/devhud/privacy`, `/devhud/security`, `/devhud/support`, `/devhud/admin`, `/devhud/releases`, `/cargo-mono`, `/derun`, and `/with-watch`; generated internal links must not use `.html` suffixes.
- Public-facing routes and content groupings must map to canonical docs contracts.
- Content must curate internal contracts from `docs/` into user-facing guidance and must not document repository-internal implementation details unless the detail is a stable public interface, user-visible behavior, or explicitly public maintainer workflow.
- Top-level in-site product page IDs currently include `devhud`, `cargo-mono`, `derun`, and `with-watch`.
- The canonical public origin is `https://oss.delino.io`. The public documentation build owns the root site and assembles the four project documentation apps under `/runmoor`, `/nodeup`, `/binpm`, and `/async-commit-hook`. Each project keeps its Markdown ownership and package-local validation; the assembled output is the only production publication surface.
- The site selector is present on every assembled documentation page. It offers Delino OSS (`/`), Runmoor (`/runmoor`), Nodeup (`/nodeup`), binpm (`/binpm`), and async-commit-hook (`/async-commit-hook`) as same-origin destinations. The selector exposes the current site, `aria-expanded`, keyboard navigation, Escape close, outside-click close, focus return, and `aria-current` for the selected destination. On the documented fixed loopback development ports, activation targets the selected package's own local root so each independently running docs app remains reachable.
- Package-local route IDs remain relative to each documentation app. When assembled, every route and asset is prefixed by its project subpath, and generated links must stay within that subpath or target an explicitly documented same-origin public route.
- Runmoor, Nodeup, binpm, and async-commit-hook guides are owned by their respective documentation apps and are published below the canonical subpaths. They must not be duplicated in the root app or represented by compatibility handoff pages.
- `/devhud` is the public DevHud overview and coordinated-release page. Its child routes cover installation/verification, implemented usage, privacy, security, support, administrator operations, and releases. These pages describe only public behavior and supported limits; internal credentials, arbitrary paths, architecture, endpoints, workflow structure, and deployment implementation remain in repository contracts. The coordinated release injects a non-secret version-and-revision marker into the built page and verifies the exact marker through the production `/devhud` route before GA. It also injects validated non-secret App Store, Google Play, and Chrome Web Store identifiers into every generated text asset containing the compiled public docs, then verifies the exact official listing destinations in `/devhud/install` before deployment.
- Major project navigation uses the canonical same-origin subpaths `/runmoor`, `/nodeup`, `/binpm`, and `/async-commit-hook`; it must not point to retired standalone documentation hosts.
- Nodeup, binpm, Runmoor, and async-commit-hook public documentation remains owned by `apps/nodeup-docs`, `apps/binpm-docs`, `apps/runmoor-docs`, and `apps/async-commit-hook-docs`; public-docs only assembles their validated output.
- The `With Watch` tab must route to the stable page ID `with-watch` and keep the `Command Rerun Watcher` grouping unless contracts are updated together.
- Rust CLI/crate product pages may omit repo-local installer script flows from public guidance even when those installers remain supported by release/runtime contracts elsewhere in the repository.
- Breaking navigation changes require explicit migration notes.

## Storage
- Source docs are versioned in-repo.
- Build artifacts are generated in `apps/public-docs/doc_build`. The build assembles validated package output at `doc_build/runmoor`, `doc_build/nodeup`, `doc_build/binpm`, and `doc_build/async-commit-hook`, then publishes the complete tree through the single `public-docs` Cloudflare Pages project.

## Security
- Public content must avoid leaking internal-only secrets or environment details.
- Public content must avoid exposing internal architecture, operational, CI, or repository-layout details that are not part of a stable public contract.
- Documentation publishing pipelines must use approved credentials only.

## Logging
- Build and publish logs should include page IDs, changed files, and publish status.
- Log output must remain safe for public CI surfaces.

## Build and Test
- Development: package-local `pnpm dev` or repository-root `pnpm dev:public-docs`.
- Local validation: `pnpm --filter public-docs test`, which runs the shared `@delinoio/docs-site-switcher` interaction suite, builds the root site and aggregated subpaths, and runs `scripts/validate-clean-urls.mjs` to verify every root and subpath route artifact, required headings/links/accessibility landmarks, site-selector state, public-content limits, generated internal `.html` links across navigation-bearing HTML attributes, and forbidden paths or URL credentials in HTML attributes, HTML resources, and CSS `url()` values. Package-local validators remain required for each assembled project.
- CI alignment: `node-public-docs-test`, selected and forced for this app, the shared site-switcher package, and every package-local docs app consumed by the aggregate build.
- Production build: `pnpm --filter public-docs build`; Cloudflare Pages must publish `apps/public-docs/doc_build`.

## Dependencies and Integrations
- Integrates with repository contract docs under `docs/`.
- Integrates with the four package-local Rspress builds, their unchanged installer assets, the shared site-selector contract, and Cloudflare Pages deployment tooling.

## Change Triggers
- Update `docs/project-public-docs.md` and this file when navigation or public doc platform contracts change.
- If user-facing content behavior changes, update corresponding `apps/public-docs` pages in the same change set.

## References
- `docs/project-public-docs.md`
- `docs/repository-defaults.md`
- `docs/domain-template.md`

## Native package guidance

The public installation surface documents the exact repository key fingerprint, supported Linux distribution/architecture matrix, stable or explicit preview registration, and package-manager install/update/remove commands. Operational details remain in `docs/repository-linux-packages-contract.md`. The shared clean route is `/linux-packages`; exact system package-manager registration paths on that page are public interfaces permitted by its scoped content validator.

The canonical public-docs production origin is `https://oss.delino.io`. The canonical public project URLs are `https://oss.delino.io/runmoor`, `https://oss.delino.io/nodeup`, `https://oss.delino.io/binpm`, and `https://oss.delino.io/async-commit-hook`; their installer entrypoints use the corresponding subpath.

Cloudflare Pages project `public-docs` owns production hosting and automatically deploys `main` from `delinoio/oss`. Its build root is the repository root, command is `pnpm --filter public-docs build`, and output is `apps/public-docs/doc_build`. The build environment pins `NODE_VERSION=24` and `PNPM_VERSION=10.26.2`, matching the repository toolchain. The former standalone documentation Pages projects and DNS records are operator-decommissioned only after the consolidated deployment and route checks pass. Do not add redirects or aliases for the retired standalone hosts.
