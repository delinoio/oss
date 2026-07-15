# Project: public-docs

## Goal
Provide the Rspress-based public documentation site for user-facing product and platform content.

## Project ID
`public-docs`

## Domain Ownership Map
- `apps/public-docs`

## Domain Contract Documents
- `docs/apps-public-docs-foundation.md`

## Cross-Domain Invariants
- Rspress clean routes, navigation, sidebar, and docs structure must stay aligned with documented contracts.
- User-facing content changes should be versioned alongside relevant contract updates.
- Public project pages currently exposed as in-site top-level navigation sections include `cargo-mono`, `derun`, and `with-watch`.
- Nodeup and binpm are major projects exposed from this Rspress surface through external top-level navigation links to `https://nodeup.delino.io` and `https://binpm.delino.io`.
- The legacy `/nodeup` route remains supported as a lightweight compatibility handoff page to `https://nodeup.delino.io`.
- Nodeup and binpm public guides must not be duplicated as in-site Rspress routes; their standalone documentation apps own those docs, except for the lightweight legacy `/nodeup` handoff.
- `public-docs` uses Rspress clean URLs and publishes its `doc_build` static output to Cloudflare Pages.

## Change Policy
- Update this index and `docs/apps-public-docs-foundation.md` in the same change for navigation, runtime, or publishing workflow updates.
- Keep `apps/public-docs` route/content behavior aligned with contract documents.

## References
- `docs/repository-defaults.md`
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
