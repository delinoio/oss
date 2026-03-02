# Project: public-docs

## Goal
`public-docs` provides a Mintlify-based public documentation app for user-facing guidance across Delino OSS projects.
It delivers curated onboarding and project overview content while keeping detailed engineering contracts in `docs/`.

## Path
- `apps/public-docs`

## Runtime and Language
- Mintlify documentation app (MDX + JSON configuration)

## Users
- External developers exploring Delino OSS projects
- Internal teams publishing user-facing documentation updates

## In Scope
- Public-facing documentation pages and navigation
- Curated project overview content derived from internal contracts
- Documentation onboarding and contribution workflow for public docs
- Broken-link validation for published pages

## Out of Scope
- Replacing internal project contracts in `docs/project-*.md`
- Product runtime APIs or backend service implementation
- Auto-generated synchronization from internal docs to public pages

## Architecture
- Mintlify app content is authored as MDX pages under `apps/public-docs`.
- Site navigation, top tabs, and page grouping are defined in `apps/public-docs/docs.json`.
- `docs.json` must include `colors.primary` and a `navigation` object using the `navigation.tabs` array contract.
- Public docs summarize stable user-facing information from internal project contracts.
- Internal contracts remain authoritative and must be updated before or alongside related public docs updates.

## Interfaces
Canonical project identifier:

```ts
enum ProjectId {
  PublicDocs = "public-docs",
}
```

Canonical page identifier contract:

```ts
enum PublicDocsPageId {
  Index = "index",
  GettingStarted = "getting-started",
  ProjectsOverview = "projects-overview",
  DocumentationLifecycle = "documentation-lifecycle",
  Devmon = "devmon",
  Nodeup = "nodeup",
  Derun = "derun",
}
```

Navigation contract:
- `navigation` must be an object, not an array.
- `navigation.tabs` is the canonical top-level navigation list.
- Tab `Home` must include:
: Group `Get Started` with `index` and `getting-started`.
: Group `Reference` with `projects-overview` and `documentation-lifecycle`.
- Tabs `Devmon`, `Nodeup`, and `Derun` must each be present as top-level tabs.
- Tab `Devmon` must include page `devmon`.
- Tab `Nodeup` must include page `nodeup`.
- Tab `Derun` must include page `derun`.

Dev preview port contract:
- `pnpm --filter public-docs dev` runs Mintlify with fixed port `46249`.

## Storage
- Source pages and configuration are stored in git at `apps/public-docs`.
- No runtime database or server-side persistent state is used by this project.

## Security
- Do not include secrets, tokens, or internal-only credentials in docs content.
- Keep user-facing guidance consistent with approved repository policies.
- Link only to trusted and intended public destinations.

## Logging
Required baseline logs and checks:
- Mintlify broken-link check output for pull request validation.
- CI job results for `node-public-docs-test`.
- Pull request review notes for documentation lifecycle policy changes.

## Build and Test
Current commands:
- Dev preview: `pnpm --filter public-docs dev`
- Link validation test: `pnpm --filter public-docs test`
- Dependency installation: `pnpm install`

## Roadmap
- Phase 1: Establish Mintlify app shell and starter core pages.
- Phase 2: Expand project-level public guides based on adoption needs.
- Phase 3: Add stronger documentation governance for cross-project consistency.

## Open Questions
- Whether to introduce automated drift detection between `docs/` and `apps/public-docs`.
- Whether to publish versioned public docs snapshots for releases.

## References
- `docs/project-template.md`
- `AGENTS.md`
- `apps/AGENTS.md`
