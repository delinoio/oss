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
- Public project pages currently exposed as in-site top-level navigation sections include `devhud`, `cargo-mono`, `derun`, and `with-watch`. DevHud's stable child routes are `/devhud/install`, `/devhud/guide`, `/devhud/privacy`, `/devhud/security`, `/devhud/support`, `/devhud/admin`, and `/devhud/releases`.
- Runmoor public guides are owned by `apps/public-docs/docs/runmoor` at `https://oss.delino.io/runmoor`. The retired `https://runmoor.delino.io` host redirects to the integrated routes without serving duplicate content.
- `/devhud` is built privately with the release candidate and published only after the updater and other coordinated DevHud channels are public; the final release verification checks the public page before GA. Publication injects and validates the configured official App Store, Google Play, and Chrome Web Store listing links into every generated text asset, with the exact destinations verified in `/devhud/install`, without treating those public identifiers as credentials.
- Nodeup and binpm remain external top-level navigation links to `https://nodeup.delino.io` and `https://binpm.delino.io`; Runmoor is an in-site `/runmoor` section.
- The legacy `/nodeup` route remains supported as a lightweight compatibility handoff page to `https://nodeup.delino.io`.
- Nodeup and binpm public guides remain external documentation surfaces maintained outside this repository; Runmoor is owned by `apps/public-docs/docs/runmoor`.
- `public-docs` uses Rspress clean URLs and publishes its `doc_build` static output to Cloudflare Pages.
- Package-local `pnpm dev` and root `pnpm dev:public-docs` bind to loopback on fixed port `46302`, reject host overrides, preflight that exact port, and fail on conflicts without automatic remapping.

## Change Policy
- Update this index and `docs/apps-public-docs-foundation.md` in the same change for navigation, runtime, or publishing workflow updates.
- Keep `apps/public-docs` route/content behavior aligned with contract documents.

## References
- `docs/repository-defaults.md`
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`

## Shared Linux package documentation

The `/linux-packages` clean route owns shared key verification, APT/DNF registration, channel selection, installation, update and removal guidance for the seven CLI projects. It does not duplicate their product guides.

The canonical public-docs production origin is `https://oss.delino.io`.

The Cloudflare Pages `public-docs` project deploys `main` automatically with the build and custom-domain settings recorded in `apps-public-docs-foundation.md`.
