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
- Rspress route, navigation, and sidebar contracts in `apps/public-docs/rspress.config.ts` must remain aligned with this contract. The root site's top-level navbar is intentionally empty; route access remains available through the sidebar and documented links.
- Documentation sources live in `apps/public-docs/docs`; the production output directory is `apps/public-docs/doc_build` and is not source-controlled.
- The publication root owns Cloudflare Pages control files. Its `_headers` contains the path-scoped security headers for `/async-commit-hook/*`.
- Rspress clean URLs are enabled. Stable route IDs owned directly by this app are `/`, `/getting-started`, `/projects-overview`, `/documentation-lifecycle`, `/linux-packages`, `/devhud`, `/devhud/install`, `/devhud/guide`, `/devhud/privacy`, `/devhud/security`, `/devhud/support`, `/devhud/admin`, `/devhud/releases`, `/cargo-mono`, `/derun`, and `/with-watch`; generated internal links must not use `.html` suffixes.
- Public-facing routes and content groupings must map to canonical docs contracts.
- Content must curate internal contracts from `docs/` into user-facing guidance and must not document repository-internal implementation details unless the detail is a stable public interface, user-visible behavior, or explicitly public maintainer workflow.
- Top-level in-site product page IDs currently include `devhud`, `cargo-mono`, `derun`, and `with-watch`.
- The canonical public origin is `https://oss.delino.io`. The public documentation build owns the root site and the seven project content roots at `apps/public-docs/docs/runmoor`, `apps/public-docs/docs/nodeup`, `apps/public-docs/docs/binpm`, `apps/public-docs/docs/async-commit-hook`, `apps/public-docs/docs/clibox`, `apps/public-docs/docs/pnport`, and `apps/public-docs/docs/react-forge`; the resulting output is the only production publication surface.
- The site selector is present on every documentation page. It offers Delino OSS (`/`), Runmoor (`/runmoor`), Nodeup (`/nodeup`), binpm (`/binpm`), async-commit-hook (`/async-commit-hook`), clibox (`/clibox`), pnport (`/pnport`), and React Forge (`/react-forge`) as same-origin destinations. The selector exposes the current site, `aria-expanded`, keyboard navigation, Escape close, outside-click close, focus return, and `aria-current` for the selected destination.
- Project route IDs are authored in the consolidated site with their canonical subpath. Generated links and assets must remain clean and same-origin, using `/runmoor`, `/nodeup`, `/binpm`, `/async-commit-hook`, `/clibox`, `/pnport`, and `/react-forge` as appropriate or targeting another explicitly documented public route.
- Runmoor, Nodeup, binpm, async-commit-hook, clibox, pnport, and React Forge guides are owned directly by the seven content roots above and are published below the canonical subpaths. They must not be duplicated in the root app or represented by compatibility handoff pages. pnport guides explicitly mark the product unreleased until complete distribution verification. React Forge guides follow `docs/apps-react-forge-docs-foundation.md`.
- React Forge's seven format guides use `/react-forge/formats/<format>/`. The former same-origin `/react-forge/<format>` paths have explicit permanent redirects to the new guides; this migration does not create a duplicate guide or a retired-host alias.
- Root in-site product page IDs currently include `devhud`, `cargo-mono`, `derun`, and `with-watch`; they are not rendered as top-level navbar items.
- `/devhud` is the public DevHud overview and coordinated-release page. Its child routes cover installation/verification, implemented usage, privacy, security, support, administrator operations, and releases. These pages describe only public behavior and supported limits; internal credentials, arbitrary paths, architecture, endpoints, workflow structure, and deployment implementation remain in repository contracts. The coordinated release injects a non-secret version-and-revision marker into the built page and verifies the exact marker through the production `/devhud` route before GA. It also injects validated non-secret App Store, Google Play, and Chrome Web Store identifiers into every generated text asset containing the compiled public docs, then verifies the exact official listing destinations in `/devhud/install` before deployment.
- A coordinated DevHud release may publish its release-bound `/devhud` page, route assets, and shared runtime assets, but its retained candidate must not contain the seven project subpaths or the root `search_index.*` data. The `public_docs` release job rebuilds the complete tree from current `main` and overlays only those DevHud-owned candidate files before deploying, so a delayed or recovered DevHud release cannot roll back root-project search data or Runmoor, Nodeup, binpm, async-commit-hook, clibox, pnport, or React Forge documentation.
- Major project navigation uses the canonical same-origin subpaths `/runmoor`, `/nodeup`, `/binpm`, `/async-commit-hook`, `/clibox`, `/pnport`, and `/react-forge`; it must not point to retired standalone documentation hosts.
- Nodeup, binpm, Runmoor, async-commit-hook, clibox, pnport, and React Forge public documentation is owned directly by `apps/public-docs/docs/nodeup`, `apps/public-docs/docs/binpm`, `apps/public-docs/docs/runmoor`, `apps/public-docs/docs/async-commit-hook`, `apps/public-docs/docs/clibox`, `apps/public-docs/docs/pnport`, and `apps/public-docs/docs/react-forge`.
- The `With Watch` tab must route to the stable page ID `with-watch` and keep the `Command Rerun Watcher` grouping unless contracts are updated together.
- Rust CLI/crate product pages may omit repo-local installer script flows from public guidance even when those installers remain supported by release/runtime contracts elsewhere in the repository.
- Breaking navigation changes require explicit migration notes.

## Storage
- Source docs are versioned in-repo.
- Build artifacts are generated in `apps/public-docs/doc_build`, including the seven project subpaths, and the complete tree is published through the single `public-docs` Cloudflare Pages project.

## Security
- Public content must avoid leaking internal-only secrets or environment details. Shared HTML/CSS validators derive project-route exemptions from one explicit stable-route catalog; root directory routes may omit their trailing slash, but project prefixes never exempt arbitrary descendants.
- Stylesheet validation applies the same raw credential patterns used for HTML to the complete CSS source, including comments and custom properties, in addition to parsed resource URL checks. Diagnostics identify only the output file and error classification.
- Public content must avoid exposing internal architecture, operational, CI, or repository-layout details that are not part of a stable public contract.
- Documentation publishing pipelines must use approved credentials only.

## Logging
- Build and publish logs should include page IDs, changed files, and publish status.
- Log output must remain safe for public CI surfaces.

## Build and Test
- Development: package-local `pnpm dev` or repository-root `pnpm dev:public-docs`.
- Local validation: `pnpm --filter public-docs test`, which runs the shared `@delinoio/docs-site-switcher` interaction suite, builds the complete root and project-subpath tree, and validates every route artifact, required heading/link/accessibility landmark, site-selector state, public-content limit, clean link, and forbidden path or URL credential.
- CI alignment: `node-public-docs-test`, selected and forced for this app and the shared site-switcher package.
- Production build: `pnpm --filter public-docs build`; Cloudflare Pages must publish `apps/public-docs/doc_build`.

## Dependencies and Integrations
- Integrates with repository contract docs under `docs/`.
- Integrates with the seven in-tree project content roots, the existing canonical installer assets, the shared site-selector contract, and Cloudflare Pages deployment tooling.

## Change Triggers
- Update `docs/project-public-docs.md` and this file when navigation or public doc platform contracts change.
- If user-facing content behavior changes, update corresponding `apps/public-docs` pages in the same change set.

## References
- `docs/project-public-docs.md`
- `docs/repository-defaults.md`
- `docs/domain-template.md`

## Native package guidance

The public installation surface documents the exact repository key fingerprint, supported Linux distribution/architecture matrix, stable or explicit preview registration, and package-manager install/update/remove commands. Operational details remain in `docs/repository-linux-packages-contract.md`. The shared clean route is `/linux-packages`; exact system package-manager registration paths on that page are public interfaces permitted by its scoped content validator.

The canonical public-docs production origin is `https://oss.delino.io`. The canonical public project URLs are `https://oss.delino.io/runmoor`, `https://oss.delino.io/nodeup`, `https://oss.delino.io/binpm`, `https://oss.delino.io/async-commit-hook`, `https://oss.delino.io/clibox`, `https://oss.delino.io/pnport`, and `https://oss.delino.io/react-forge`. Existing installer entrypoints use their corresponding project subpaths. clibox installation uses npm or signed Linux archives and adds no hosted installer. pnport has no published installer while 0.1.0 remains unreleased. React Forge installs through npm.

Cloudflare Pages project `public-docs` owns production hosting and automatically deploys `main` from `delinoio/oss`. Its build root is the repository root, command is `pnpm --filter public-docs build`, and output is `apps/public-docs/doc_build`. The build environment pins `NODE_VERSION=24` and `PNPM_VERSION=10.26.2`, matching the repository toolchain. The former standalone documentation Pages projects and DNS records are operator-decommissioned only after the consolidated deployment and route checks pass. Do not add redirects or aliases for the retired standalone hosts.
