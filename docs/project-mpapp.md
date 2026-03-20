# Project: mpapp

## Goal
Provide an Expo React Native application for mobile workflows with stable platform behavior and documented device capability usage.

## Project ID
`mpapp`

## Domain Ownership Map
- `apps/mpapp`

## Domain Contract Documents
- `docs/apps-mpapp-foundation.md`

## Cross-Domain Invariants
- Bluetooth permissions and capability behavior must be explicitly documented and versioned.
- App route and screen contracts must remain backward compatible across incremental releases, including stable `screenId` values and route/deep-link mappings.
- Session snapshot restore must always start in `Idle` mode and must not auto-reconnect previously connected sessions.

## Change Policy
- Update this index and `docs/apps-mpapp-foundation.md` together whenever app capabilities, routes, or runtime assumptions change.
- Keep `apps/AGENTS.md` and root `AGENTS.md` aligned on mobile UI and operational policies.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
