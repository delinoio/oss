### Instructions for `protos/`

- Follow the root `AGENTS.md`, the owning project index, and the relevant domain contract before changing a shared API.
- `protos/delibase/v1` is owned by project `delibase`; its source of truth is the versioned `delibase.v1` Protobuf contract.
- Generate Connect-compatible Go and TypeScript artifacts from the root source. Never edit generated output as a second source of truth.
- Checked-in Go artifacts live under `protos/delibase/gen/go`; protobuf-es v2 TypeScript artifacts live under `protos/delibase/gen/ts` and are exported by the workspace package `@delinoio/delibase-connect`.
- The six stable services are `AccountService`, `OrganizationService`, `TeamService`, `CatalogService`, `BillingService`, and `UsageService`.
- Preserve released `delibase.v1` additively. Breaking changes require a new API version and synchronized app/server migration docs.
- Keep UUID v7, signed-int64 money/usage, opaque cursors, Logto token metadata, the redacted `x-delibase-forwarded-user-token` forwarded-user context, and stable enum error details aligned with the server and app contracts.
- Keep distinct stable idempotency operations for invitation acceptance and revocation; invitation creation does not carry idempotency fields.
- Run Protobuf lint, breaking checks, generation, generated Go formatting/vet/tests, and the consuming app type checks when implementation exists.
- Use root `pnpm generate:proto` for generation plus the workspace package `dist` build, and `pnpm check:proto` for Buf lint, the `delibase.v1` descriptor compatibility check, and a clean regeneration diff. Local checks use the checked-in initial descriptor; CI uses the immutable descriptor from the pull request base or pre-push commit once that descriptor exists there. TypeScript checks use `pnpm --filter @delinoio/delibase-connect typecheck`; Go checks include `go test ./protos/delibase/...` and `go vet ./protos/delibase/...`. CI runs all four validation commands in the change-scoped `proto-delibase` job.
- Changes require synchronized updates to `docs/project-delibase.md`, `docs/protos-delibase-api-contract.md`, `docs/servers-delibase-server-foundation.md`, `docs/apps-delidev-app-foundation.md`, and affected `AGENTS.md` files.
