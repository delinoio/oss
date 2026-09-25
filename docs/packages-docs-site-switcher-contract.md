# packages-docs-site-switcher-contract

## Scope

- Package: `@delinoio/docs-site-switcher`
- Canonical path: `packages/docs-site-switcher`
- Owner: the consolidated `public-docs` documentation surface

## Interfaces and Contracts

- `DocumentationSiteId` is the stable enum for `PublicDocs`, `Runmoor`, `Nodeup`, `Binpm`, `AsyncCommitHook`, `Clibox`, `Pnport`, and `ReactForge`.
- `DOCUMENTATION_SITES` is the fixed registry of user-facing labels and same-origin destinations: `/`, `/runmoor/`, `/nodeup/`, `/binpm/`, `/async-commit-hook/`, `/clibox/`, `/pnport/`, and `/react-forge/`.
- Production destinations remain clean same-origin subpaths. The consolidated `public-docs` development server uses fixed loopback port `46302`; selector activation remains on that server and uses the same production-relative destinations, so development behavior does not create hydration differences.
- `DocsSiteSwitcher` renders a button and a `menu` of `menuitem` links. The selected destination carries `aria-current="page"`; the trigger exposes `aria-expanded` and controls the menu.
- The selector supports arrow-key and Home/End navigation, Enter activation, Escape close, outside-click close, and focus return to the trigger.
- Every consolidated documentation page uses the shared package with its owning `DocumentationSiteId`; the public-docs theme derives the current ID from the page pathname, and content sections must not fork the registry or interaction behavior.

## Security and Accessibility

- Destinations are fixed same-origin paths and must not be constructed from user input.
- The menu is server-rendered for stable current-site semantics but remains hidden until opened.
- Focus-visible states and a reduced-motion fallback are required. Mobile layouts must keep every destination reachable without horizontal overflow; the fixed menu is height-bounded and scrollable on short viewports.

## Build and Test

- The package is private workspace code and is consumed by the single `apps/public-docs` Rspress application.
- Run `pnpm --filter @delinoio/docs-site-switcher test` for the component interaction and pathname-mapping tests.
- Changes to the enum, registry, markup contract, or keyboard behavior require corresponding updates to the package tests and consolidated public-docs validation.

## Change Triggers

- Update this contract and `docs/README.md` when a documentation destination, public label, accessibility contract, or package ownership changes.
- Update `docs/apps-public-docs-foundation.md` when the shared selector changes the consolidated documentation surface.

React Forge now has sixteen clean guide routes, including unreleased `/react-forge/glb` and `/react-forge/fbx`. The shared selector destination remains `/react-forge/`; its active state and existing documentation hosts are unchanged. See `apps-react-forge-docs-foundation.md` for availability and validation boundaries.
