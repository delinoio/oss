# apps-public-docs-foundation

## Scope
- Project/component: public documentation web app contract
- Canonical path: `apps/public-docs`

## Runtime and Language
- Runtime: Mintlify web documentation app
- Primary language: Markdown/JSON content with web build tooling

## Users and Operators
- External users reading public product documentation
- Internal maintainers publishing and reviewing docs updates

## Interfaces and Contracts
- Navigation and page ID contracts in `apps/public-docs/docs.json` must remain stable.
- Public-facing routes and content groupings must map to canonical docs contracts.
- Breaking navigation changes require explicit migration notes.

## Storage
- Source docs are versioned in-repo.
- Build artifacts are generated and published through release workflows.

## Security
- Public content must avoid leaking internal-only secrets or environment details.
- Documentation publishing pipelines must use approved credentials only.

## Logging
- Build and publish logs should include page IDs, changed files, and publish status.
- Log output must remain safe for public CI surfaces.

## Build and Test
- Local validation: `pnpm --filter public-docs test`
- CI alignment: `node-public-docs-test`

## Dependencies and Integrations
- Integrates with repository contract docs under `docs/`.
- Integrates with Mintlify navigation and deployment tooling.

## Change Triggers
- Update `docs/project-public-docs.md` and this file when navigation or public doc platform contracts change.
- If user-facing content behavior changes, update corresponding `apps/public-docs` pages in the same change set.

## References
- `docs/project-public-docs.md`
- `docs/domain-template.md`
