# Labs documentation foundation

## Scope

`apps/labs-docs` owns the independent static Delino Labs documentation site.

## Runtime and Language

Use Node.js 24, the repository-pinned pnpm, Rspress 2.0.23, TypeScript configuration and Markdown pages. Use the stock Rspress theme without custom React components or visual assets.

## Users and Operators

Readers browse Labs documentation. Maintainers edit public pages and operate a dedicated Cloudflare Pages project.

## Interfaces and Contracts

- Canonical origin: `https://docs.labs.delino.io`; base: `/`; clean URLs enabled.
- `/` is the Overview page with the title `Delino Labs` and a link to Getting Started.
- `/getting-started` is the Getting Started page with navigation guidance. Do not invent product availability or workflows before product requirements exist.
- The Guide sidebar lists Overview and Getting Started. A TaiRouter group lists the eleven pages in `scripts/tairouter-pages.json` in onboarding order: overview, installation, configuration, organizations, providers/routing, clients, native compatibility, compatibility, operations, recovery and migration. The overview in every language links to the product and English guides.
- TaiRouter means Team AI Router and is an open-source Delino Labs product. Its public guides live in `docs/en/tairouter`, use the English `/tairouter/` and `/tairouter/guide/*` routes and are explicitly source-only in the localization registry for initial publication. Each language sidebar labels this group as English and links to the same English routes. Do not create translated copies or missing-language route aliases.
- Use the default search, theme toggle, page table of contents and previous/next links. TaiRouter pages are searchable in the English index; localized readers can follow the labelled English sidebar group. The stock Rspress 2.0.23 menu guesses nonexistent translated paths. A narrow build adapter in `scripts/english-guides-loader.cjs` keeps the default theme and sends language selection on an English-only page to the selected language overview, in both server rendering and client navigation. Its exact upstream hook signature is checked during compilation. Remove the adapter when the stock theme handles missing translations or the guides receive translations.
- The default responsive theme collapses navigation on mobile. Preserve keyboard navigation, visible focus and Escape dismissal for dialogs. Search handles empty results; unknown routes use the default 404 page. Static pages require no account, permission or loading flow.
- Root `pnpm dev:labs-docs` and package-local `pnpm dev` bind to `127.0.0.1:46303`. The shared Rspress wrapper prevents address overrides, fails on conflicts and forwards termination signals. Preview uses fixed loopback port `46304`.
- Cloudflare Pages target settings: project `labs-docs`, repository `delinoio/oss`, production branch `main`, repository-root build command `pnpm --filter labs-docs build`, output `apps/labs-docs/doc_build`, `NODE_VERSION=24`, `PNPM_VERSION=10.26.2`, custom domain `docs.labs.delino.io`.
- These are deployment settings for operators, not evidence of provisioned hosting or DNS. CI validates only and must not deploy.

## Localization

Follow the [shared Labs localization contract](apps-labs-localization-contract.md) for catalogs, localized routes, ChatGPT plan generation, credential ownership and offline validation.

## Storage

English source pages are versioned under `apps/labs-docs/docs/en`; translated pages use sibling language directories. Generated `doc_build` output and Rspress cache directories are ignored. No runtime storage is required.

## Security

Publish user-facing content only. Exclude credentials, private configuration and repository-internal implementation details. No secrets or external services are required to build or browse the site.

## Logging

Retain Rspress build diagnostics. Route validation reports structured route and result events without credentials or reader data.

## Build and Test

- Development: `pnpm dev:labs-docs` from the root, or `pnpm dev` from the app.
- Build: `pnpm --filter labs-docs build`.
- Preview: `pnpm --filter labs-docs preview`.
- Validation: `pnpm test` from `apps/labs-docs` builds the site and checks all registered language route artifacts and the eleven English TaiRouter routes, page titles, cross-page navigation, canonical links and the 404 artifact. Content tests check local TaiRouter links, navigation coverage, exact English-only declarations, public-content limits and removal of the former publisher.
- CI: `node-labs-docs-test` uses the central affected planner and exact Turbo comparison. Its uncached `ci:check` depends on the cacheable build and route checks. Source/config, shared development wrapper, toolchain and owning contracts select the job; inputs outside the workspace force it.
- Manually inspect desktop/mobile navigation, search, theme toggle, focus and 404 behavior when changing the theme. Local validation does not establish Cloudflare account, DNS or production acceptance.

## Dependencies and Integrations

The app depends on `@rspress/core` and uses the shared development wrapper. It does not add entries to the OSS project selector or duplicate Labs pages in `apps/public-docs`.

## Change Triggers

Update [the project index](project-labs-docs.md) for ownership or publishing changes, this contract for route/runtime changes and [the catalog](README.md) for document inventory changes. Update `apps/AGENTS.md` when development rules or ownership change.

## References

- [Project index](project-labs-docs.md)
- [Repository defaults](repository-defaults.md)
- [Environment ownership](repository-environment-contract.md)
- [Repository workflow](repository-workflow-contract.md)
