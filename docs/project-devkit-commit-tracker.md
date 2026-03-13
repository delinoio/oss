# Project: devkit-commit-tracker

## Goal
Define the commit-tracker product contract across web UI, API server, and collector components.

## Project ID
`devkit-commit-tracker`

## Domain Ownership Map
- `apps/devkit/src/apps/commit-tracker` (`web-app`)
- `servers/commit-tracker` (`api-server`)
- `cmds/commit-tracker` (`collector`)

## Domain Contract Documents
- `docs/apps-devkit-commit-tracker-web-app-foundation.md`
- `docs/servers-devkit-commit-tracker-api-server-foundation.md`
- `docs/cmds-devkit-commit-tracker-collector-foundation.md`

## Cross-Domain Invariants
- Component identifiers remain stable: `web-app`, `api-server`, `collector`.
- Event payload shape and timestamp semantics must remain consistent from collector to API to web UI.
- Query and filtering behavior exposed by the API must remain compatible with web UI expectations.

## Change Policy
- Any interface update must include this index plus all impacted component docs in the same change.
- Cross-component contract updates must keep route, API, and ingestion semantics synchronized.

## References
- `docs/project-devkit.md`
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
