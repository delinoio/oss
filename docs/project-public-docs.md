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

## Cross-Domain Invariants

- CacheCow reserves `apps/public-docs/docs/cachecow` for a planned `/cachecow` section under the [CacheCow documentation contract](apps-cachecow-docs-foundation.md). The skeleton adds no published routes or navigation entries; content and route validation must be integrated together.

- The root site’s top-level navbar contains no route items. Product pages `cargo-mono` and `with-watch` remain available through the sidebar and documented links.

- Rspress clean routes, navigation, sidebar, and docs structure must stay aligned with documented contracts.
- pnport's eleven public guide routes include `/pnport/preview-testing` for external Turbopack and TypeScript 7 testing; its sidebar, entry-page links and required headings/links stay aligned with the shared route catalog and `docs/apps-pnport-docs-foundation.md`.
- User-facing content changes should be versioned alongside relevant contract updates.
- Runmoor public guides are owned directly by `apps/public-docs/docs/runmoor` and are published at `https://oss.delino.io/runmoor`. The canonical public routes use the same-origin `/runmoor` subpath.
- Nodeup, binpm, Runmoor, async-commit-hook, clibox, pnport are major projects exposed through same-origin navigation to `/nodeup`, `/binpm`, `/runmoor`, `/async-commit-hook`, `/clibox`, `/pnport`. Their Markdown is owned directly by `apps/public-docs/docs/{nodeup,binpm,runmoor,async-commit-hook,clibox,pnport}`, while this project builds and publishes the complete tree. pnport's public guides describe verified stable 0.1.2 availability with known macOS and full-acceptance limits.
- The shared site selector appears on every assembled page and marks the active project with `aria-current`; it supports keyboard navigation, Escape close, outside-click close, and focus return.
- No legacy handoff page or duplicate guide route is canonical. Retired standalone documentation hosting is decommissioned by operators after route and installer verification; no redirects or aliases are added.
- `public-docs` uses Rspress clean URLs, builds the six project content roots below their canonical subpaths, and publishes the complete `doc_build` tree to the single `public-docs` Cloudflare Pages project.
- Public-docs build and route-validation caches include all eight canonical installer sources through package-local external inputs. Generated installer bytes must equal the current source; inherited build dependencies, outputs, and tool inputs remain intact. These cache inputs do not invalidate unrelated workspace tasks. The app-domain contract owns the disposable cache regression boundary.
- Package-local `pnpm dev` and root `pnpm dev:public-docs` bind to loopback on fixed port `46302`, reject host overrides, preflight that exact port, and fail on conflicts without automatic remapping.

## Project requirements

- `public-docs` -> `apps/public-docs`, `packages/docs-site-switcher`

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
