# apps-public-docs-foundation

## Scope
- Project/component: public documentation web app contract
- Canonical path: `apps/public-docs`

## Runtime and Language
- Runtime: Rspress static documentation app
- Primary language: Markdown/TypeScript content and configuration with web build tooling
- Production deployment target: Cloudflare Pages static output

## Users and Operators
- External users reading public product documentation
- Internal maintainers publishing and reviewing docs updates

## Interfaces and Contracts
- Rspress route, navigation, and sidebar contracts in `apps/public-docs/rspress.config.ts` must remain stable.
- Documentation sources live in `apps/public-docs/docs`; the production output directory is `apps/public-docs/doc_build` and is not source-controlled.
- Rspress clean URLs are enabled. Stable route IDs are `/`, `/getting-started`, `/projects-overview`, `/documentation-lifecycle`, `/cargo-mono`, `/derun`, `/with-watch`, and `/nodeup`; generated internal links must not use `.html` suffixes.
- Public-facing routes and content groupings must map to canonical docs contracts.
- Content must curate internal contracts from `docs/` into user-facing guidance and must not document repository-internal implementation details unless the detail is a stable public interface, user-visible behavior, or explicitly public maintainer workflow.
- Top-level in-site product page IDs currently include `cargo-mono`, `derun`, and `with-watch`.
- External top-level major project links include Nodeup at `https://nodeup.delino.io` and binpm at `https://binpm.delino.io`.
- The legacy `/nodeup` public-docs route must remain a lightweight compatibility handoff page to `https://nodeup.delino.io`; it is not an in-site guide route and must not duplicate Nodeup documentation content.
- Nodeup and binpm public documentation remain owned by `apps/nodeup-docs` and `apps/binpm-docs`; do not add or restore in-site guide routes for those projects under `apps/public-docs`.
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
- Local validation: `pnpm --filter public-docs test`, which builds Rspress and runs `scripts/validate-clean-urls.mjs` to verify every stable route artifact and reject generated internal `.html` links.
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
