# apps/delidev-mobile working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `apps/delidev-mobile/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Owning contracts

- [DeliDev mobile client contract](../../docs/apps-delidev-mobile-contract.md)

The desktop-owned pure identity presentation module is shared with mobile. Read the [desktop product identity contract](../../docs/apps-delidev-desktop-contract.md#product-identity-presentation) when changing that cross-renderer module; keep desktop React/query-cache ownership out of mobile.

When mobile source imports a file from another package by relative path, declare that file as an explicit `$TURBO_ROOT$`-relative input of the `build`, `build:frontend` and `typecheck` tasks in `apps/delidev-mobile/turbo.json`, repeating the root task inputs because Turbo replaces inherited arrays. Keep `scripts/ci/ci-contract.test.mjs` aligned with those declarations.
