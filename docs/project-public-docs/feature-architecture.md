# Feature: architecture

## Architecture
- Mintlify app content is authored as MDX pages under `apps/public-docs`.
- Site navigation, top tabs, and page grouping are defined in `apps/public-docs/docs.json`.
- `docs.json` must include `colors.primary` and a `navigation` object using the `navigation.tabs` array contract.
- Public docs summarize stable user-facing information from internal project contracts.
- Internal contracts remain authoritative and must be updated before or alongside related public docs updates.

