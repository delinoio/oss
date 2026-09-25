# Project: public-docs

## Goal
Provide the Rspress-based public documentation site for user-facing product and platform content.

## Project ID
`public-docs`

## Domain Ownership Map
- `apps/public-docs`
- `packages/docs-site-switcher`

## Domain Contract Documents
- `docs/apps-public-docs-foundation.md`
- `docs/packages-docs-site-switcher-contract.md`
- `docs/apps-react-forge-docs-foundation.md`

## Cross-Domain Invariants
- Rspress clean routes, navigation, sidebar, and docs structure must stay aligned with documented contracts.
- User-facing content changes should be versioned alongside relevant contract updates.
- The root site's top-level navbar intentionally contains no route items; the public project pages remain available through the sidebar and documented links. The current product page IDs are `devhud`, `cargo-mono`, `derun`, and `with-watch`. DevHud's stable child routes are `/devhud/install`, `/devhud/guide`, `/devhud/privacy`, `/devhud/security`, `/devhud/support`, `/devhud/admin`, and `/devhud/releases`.
- Runmoor public guides are owned directly by `apps/public-docs/docs/runmoor` and are published at `https://oss.delino.io/runmoor`. The canonical public routes use the same-origin `/runmoor` subpath.
- `/devhud` is built privately with the release candidate and published only after the updater and other coordinated DevHud channels are public; the final release verification checks the public page before GA. Publication injects and validates the configured official App Store, Google Play, and Chrome Web Store listing links into every generated text asset, with the exact destinations verified in `/devhud/install`, without treating those public identifiers as credentials.
- Nodeup, binpm, Runmoor, async-commit-hook, clibox, pnport, and React Forge are major projects exposed through same-origin navigation to `/nodeup`, `/binpm`, `/runmoor`, `/async-commit-hook`, `/clibox`, `/pnport`, and `/react-forge`. Their Markdown is owned directly by `apps/public-docs/docs/{nodeup,binpm,runmoor,async-commit-hook,clibox,pnport,react-forge}`, while this project builds and publishes the complete tree. pnport's public guides identify its unreleased status; React Forge's format guides live under `react-forge/formats/<format>/`, distinguish the six local-document hosts from its narrower live Figma authentication boundary, and mark SFX and Sprite as unreleased previews.
- The shared site selector appears on every assembled page and marks the active project with `aria-current`; it supports keyboard navigation, Escape close, outside-click close, and focus return.
- No legacy handoff page or duplicate guide route is canonical. Retired standalone documentation hosting is decommissioned by operators after route and installer verification; no redirects or aliases are added.
- `public-docs` uses Rspress clean URLs, builds the seven project content roots below their canonical subpaths, and publishes the complete `doc_build` tree to the single `public-docs` Cloudflare Pages project.
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

The Cloudflare Pages `public-docs` project deploys `main` automatically with the build and custom-domain settings recorded in `apps-public-docs-foundation.md`. The project is the only production documentation publisher for the consolidated root and subpaths.

React Forge now has eighteen clean guide routes, including unreleased `/react-forge/formats/glb/` and `/react-forge/formats/fbx/`. The shared selector destination remains `/react-forge/`; its active state and existing documentation hosts are unchanged. See `apps-react-forge-docs-foundation.md` for availability and validation boundaries.
