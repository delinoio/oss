# packages-docs-site-switcher-contract

## Scope

- Package: `@delinoio/docs-site-switcher`
- Canonical path: `packages/docs-site-switcher`
- Owner: the consolidated `public-docs` documentation surface

## Interfaces and Contracts

- `DocumentationSiteId` is the stable enum for `PublicDocs`, `Runmoor`, `Nodeup`, `Binpm`, and `AsyncCommitHook`.
- `DOCUMENTATION_SITES` is the fixed registry of user-facing labels and same-origin destinations: `/`, `/runmoor/`, `/nodeup/`, `/binpm/`, and `/async-commit-hook/`.
- Production destinations remain clean same-origin subpaths. When a page is served by one of the documented loopback development ports (`46302` public-docs, `46303` nodeup, `46304` binpm, `46309` Runmoor, or `46310` async-commit-hook), selector activation resolves the target to that app's package-local root on its own port. The server-rendered hrefs stay production-relative, so this development behavior does not create hydration differences.
- `DocsSiteSwitcher` renders a button and a `menu` of `menuitem` links. The selected destination carries `aria-current="page"`; the trigger exposes `aria-expanded` and controls the menu.
- The selector supports arrow-key and Home/End navigation, Enter activation, Escape close, outside-click close, and focus return to the trigger.
- Every assembled documentation page uses the shared package with its owning `DocumentationSiteId`; applications must not fork the registry or interaction behavior.

## Security and Accessibility

- Destinations are fixed same-origin paths and must not be constructed from user input.
- The menu is server-rendered for stable current-site semantics but remains hidden until opened.
- Focus-visible states and a reduced-motion fallback are required. Mobile layouts must keep every destination reachable without horizontal overflow.

## Build and Test

- The package is private workspace code and is consumed by the five Rspress documentation apps.
- Run `pnpm --filter @delinoio/docs-site-switcher test` for the component interaction and pathname-mapping tests.
- Changes to the enum, registry, markup contract, or keyboard behavior require corresponding updates to the package tests and assembled public-docs validation.

## Change Triggers

- Update this contract and `docs/README.md` when a documentation destination, public label, accessibility contract, or package ownership changes.
- Update `docs/apps-public-docs-foundation.md` when the shared selector changes the assembled documentation surface.
