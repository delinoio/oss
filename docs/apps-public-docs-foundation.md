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
- Rspress clean URLs are enabled. Stable route IDs are `/`, `/getting-started`, `/projects-overview`, `/documentation-lifecycle`, `/linux-packages`, `/devhud`, `/devhud/install`, `/devhud/guide`, `/devhud/privacy`, `/devhud/security`, `/devhud/support`, `/devhud/admin`, `/devhud/releases`, `/cargo-mono`, `/derun`, `/with-watch`, `/taskflow`, `/taskflow/configuration`, `/taskflow/commands`, `/taskflow/cache`, `/taskflow/ci`, and `/nodeup`; generated internal links must not use `.html` suffixes.
- Public-facing routes and content groupings must map to canonical docs contracts.
- Content must curate internal contracts from `docs/` into user-facing guidance and must not document repository-internal implementation details unless the detail is a stable public interface, user-visible behavior, or explicitly public maintainer workflow.
- Top-level in-site product page IDs currently include `devhud`, `cargo-mono`, `derun`, `with-watch`, and `taskflow`.
- Runmoor guides have moved to `apps/runmoor-docs` at `https://runmoor.delino.io`, with their complete content and public constraints governed by `docs/apps-runmoor-docs-foundation.md`. The former `/runmoor` and six child routes are removed without redirects or compatibility pages. Build validation must reject old route artifacts and local links, and require the standalone URL in navigation, home, and project catalog.
- TaskFlow public pages describe the source installation workflow, native task configuration, graph commands, development sessions, caching/secrets, sharding, and GitHub Actions export. They must state unpublished status and tested adapter boundaries without claiming unexecuted platform evidence.
- `/devhud` is the public DevHud overview and coordinated-release page. Its child routes cover installation/verification, implemented usage, privacy, security, support, administrator operations, and releases. These pages describe only public behavior and supported limits; internal credentials, arbitrary paths, architecture, endpoints, workflow structure, and deployment implementation remain in repository contracts. The coordinated release injects a non-secret version-and-revision marker into the built page and verifies the exact marker through the production `/devhud` route before GA. It also injects validated non-secret App Store, Google Play, and Chrome Web Store identifiers into every generated text asset containing the compiled public docs, then verifies the exact official listing destinations in `/devhud/install` before deployment.
- External top-level major project links include Nodeup at `https://nodeup.delino.io`, binpm at `https://binpm.delino.io`, and Runmoor at `https://runmoor.delino.io`.
- The legacy `/nodeup` public-docs route must remain a lightweight compatibility handoff page to `https://nodeup.delino.io`; it is not an in-site guide route and must not duplicate Nodeup documentation content.
- Nodeup, binpm, and Runmoor public documentation remain owned by `apps/nodeup-docs`, `apps/binpm-docs`, and `apps/runmoor-docs`; do not add or restore in-site guide routes for those projects under `apps/public-docs`.
- The `With Watch` tab must route to the stable page ID `with-watch` and keep the `Command Rerun Watcher` grouping unless contracts are updated together.
- Rust CLI/crate product pages may omit repo-local installer script flows from public guidance even when those installers remain supported by release/runtime contracts elsewhere in the repository.
- Breaking navigation changes require explicit migration notes.

## Storage
- Source docs are versioned in-repo.
- Build artifacts are generated in `apps/public-docs/doc_build` and published to Cloudflare Pages.

## Security
- Public content must avoid leaking internal-only secrets or environment details.
- Public content must avoid exposing internal architecture, operational, CI, or repository-layout details that are not part of a stable public contract.
- Documentation publishing pipelines must use approved credentials only.

## Logging
- Build and publish logs should include page IDs, changed files, and publish status.
- Log output must remain safe for public CI surfaces.

## Build and Test
- Development: package-local `pnpm dev` or repository-root `pnpm dev:public-docs`.
- Local validation: `pnpm --filter public-docs test`, which builds Rspress and runs `scripts/validate-clean-urls.mjs` to verify every stable route artifact, required headings/links/accessibility landmarks, public-content limits, generated internal `.html` links across navigation-bearing HTML attributes, and forbidden paths or URL credentials in HTML attributes, HTML resources, and CSS `url()` values.
- CI alignment: `node-public-docs-test`
- Production build: `pnpm --filter public-docs build`; Cloudflare Pages must publish `apps/public-docs/doc_build`.

## Dependencies and Integrations
- Integrates with repository contract docs under `docs/`.
- Integrates with Rspress navigation and Cloudflare Pages deployment tooling.

## Change Triggers
- Update `docs/project-public-docs.md` and this file when navigation or public doc platform contracts change.
- If user-facing content behavior changes, update corresponding `apps/public-docs` pages in the same change set.

## References
- `docs/project-public-docs.md`
- `docs/repository-defaults.md`
- `docs/domain-template.md`

## Native package guidance

The public installation surface documents the exact repository key fingerprint, supported Linux distribution/architecture matrix, stable or explicit preview registration, and package-manager install/update/remove commands. Operational details remain in `docs/repository-linux-packages-contract.md`. The shared clean route is `/linux-packages`; exact system package-manager registration paths on that page are public interfaces permitted by its scoped content validator.

The canonical public-docs production origin is `https://oss.delino.io`.

Cloudflare Pages project `public-docs` owns production hosting and automatically deploys `main` from `delinoio/oss`. Its build root is the repository root, command is `pnpm --filter public-docs build`, and output is `apps/public-docs/doc_build`. The build environment pins `NODE_VERSION=24` and `PNPM_VERSION=10.26.2`, matching the repository toolchain. Its platform hostname is `public-docs-43x.pages.dev`; the custom-domain CNAME now targets that hostname instead of the previous Mintlify target.
