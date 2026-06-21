# Project: public-docs

## Goal
Provide the Mintlify-based public documentation site for user-facing product and platform content.

## Project ID
`public-docs`

## Domain Ownership Map
- `apps/public-docs`

## Domain Contract Documents
- `docs/apps-public-docs-foundation.md`

## Cross-Domain Invariants
- Mintlify navigation IDs and docs structure must stay aligned with documented contracts.
- User-facing content changes should be versioned alongside relevant contract updates.
- Public project pages currently exposed in top-level navigation include `cargo-mono`, `derun`, and `with-watch`.
- Nodeup is intentionally not exposed in this Mintlify surface's navigation; the legacy `/nodeup` route remains only as an unlisted compatibility handoff to `https://nodeup.delino.io`.
- `public-docs` is an existing documented exception to the default Rsbuild/Rspress-style static-site toolchain and Cloudflare Pages deployment preference.

## Change Policy
- Update this index and `docs/apps-public-docs-foundation.md` in the same change for navigation, runtime, or publishing workflow updates.
- Keep `apps/public-docs` route/content behavior aligned with contract documents.

## References
- `docs/repository-defaults.md`
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
