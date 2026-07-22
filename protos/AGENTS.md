### Instructions for `protos/`

- Follow the root `AGENTS.md`, the owning project index, and the relevant domain contract before changing a shared API.
- `protos/delibase/v1` is owned by project `delibase`; its source of truth is the versioned `delibase.v1` Protobuf contract.
- Generate Connect-compatible Go and TypeScript artifacts from the root source. Never edit generated output as a second source of truth.
- The six stable services are `AccountService`, `OrganizationService`, `TeamService`, `CatalogService`, `BillingService`, and `UsageService`.
- Preserve released `delibase.v1` additively. Breaking changes require a new API version and synchronized app/server migration docs.
- Keep UUID v7, signed-int64 money/usage, opaque cursors, Logto token metadata, redacted forwarded-user context, and stable enum error details aligned with the server and app contracts.
- Run Protobuf lint, breaking checks, generation, generated Go formatting/vet/tests, and the consuming app type checks when implementation exists.
- Changes require synchronized updates to `docs/project-delibase.md`, `docs/protos-delibase-api-contract.md`, `docs/servers-delibase-server-foundation.md`, `docs/apps-delidev-app-foundation.md`, and affected `AGENTS.md` files.
