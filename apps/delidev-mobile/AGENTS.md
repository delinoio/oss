# apps/delidev-mobile working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `apps/delidev-mobile/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

- Keep the `delidev-mobile-beta` workflow concurrency at group `delidev-mobile-beta`, `cancel-in-progress: false` and `queue: max`. At the 100-pending capacity, stop dispatching, rerun the capacity-cancelled original run once, and never replace the dispatch or regenerate the candidate.

## Owning contracts

- [DeliDev mobile client contract](../../docs/apps-delidev-mobile-contract.md)

The desktop-owned pure identity presentation module is shared with mobile. Read the [desktop product identity contract](../../docs/apps-delidev-desktop-contract.md#product-identity-presentation) when changing that cross-renderer module; keep desktop React/query-cache ownership out of mobile.
