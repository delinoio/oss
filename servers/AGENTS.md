### Instructions for `servers/`

- Follow root `AGENTS.md`, project index docs, and relevant `docs/servers-*.md` contracts before implementation.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Write all source and comments in English.
- Prefer enums or typed constants over free-form strings for API contracts.

### Scope in This Domain

- `servers/thenv`: Backend for secure environment sharing.
- `servers/delibase`: Planned Go/PostgreSQL/sqlc organization, billing, and usage service owned by project `delibase`.
- `servers/internal`: Repository-shared Go package boundary consumed by delibase; not a project-owned delibase subcomponent or an unrelated project.

### Server Language and Data Rules

- Servers in this domain must be implemented in Go.
- SQL queries and type-safe data access must use `sqlc`.
- Protobuf definitions should live at `proto/<service_name>/v1/*.proto` unless a project contract explicitly uses a shared cross-runtime proto root.
- Each server project must provide a local protobuf generation script and a `go generate` entrypoint.
- Keep API boundaries explicit and versionable.
- Prioritize Connect RPC-based communication for business flows over Tauri-specific bindings.
- Keep authorization and audit behavior documented and testable.
- Never expose secret values in logs or default API responses.

### Fixed Server Project Structure

Stateful server projects under `servers/<service_name>/` should follow this minimum structure:

- `cmd/<service_name>/main.go`
- `internal/service/`
- `internal/contracts/`
- `internal/logging/`
- `db/query/`
- `db/migrations/`
- `db/sqlc.yaml`
- `proto/<service_name>/v1/*.proto`
- `buf.yaml`
- `buf.gen.yaml`
- `scripts/generate-go-proto.sh`
- `generate.go` (with `go:generate` directive)

Scaffold-only service projects may start with a smaller structure (`main.go` + `internal/service`) when documented in the project index and matching server-domain contract docs, but must adopt explicit contract/data/logging subdirectories before persistence and public API rollout.

### Integration Rules

- Changes to server interfaces must be synchronized with related CLI and app contracts.
- Update `docs/project-thenv.md` and `docs/servers-thenv-server-foundation.md` for every thenv interface or trust model update.

### Delibase Rules

- Follow `docs/project-delibase.md`, `docs/servers-delibase-server-foundation.md`, `docs/protos-delibase-api-contract.md`, and `docs/servers-internal-foundation.md` before implementation.
- The canonical future API origin is `https://delibase.deli.dev`; do not activate or deploy a runtime for issue #722.
- Use PostgreSQL and sqlc for persistence, UUID v7 for persisted IDs, signed int64 USD micro-units for money, signed int64 units for usage, and transactional/locked append-only ledger/reservation invariants.
- Keep Logto identity validation separate from delibase authorization; Polar owns payment settlement/invoices, while delibase owns local organization, team, membership, ledger, reservation, and audit state.
- The six Connect services are `AccountService`, `OrganizationService`, `TeamService`, `CatalogService`, `BillingService`, and `UsageService`. Human calls use user tokens except for anonymous `CatalogService` reads; usage mutations validate M2M and forwarded end-user context.
- Shared reusable auth/JWKS, Connect interceptors, redaction, request/trace IDs, HTTP defaults, structured logging, and UUID v7 code belongs under `servers/internal`; business policy remains in delibase.
- Required checks once code exists include `gofmt`, `go vet ./...`, `go test ./servers/delibase/...`, sqlc/migration checks, Protobuf generation/compatibility, PostgreSQL concurrency tests, and Docker validation.
- Issue #722 artifact scope excludes public activation/deployment, production SLO/RPM controls, dashboards/alerts, kill switches, feature flags, operator RPCs, and manual replay tooling. Future GHCR release scope is signed `delibase@v*` multi-architecture `vX.Y.Z` and `latest` only.

### Multi-Component Contract Sync

- `servers/thenv` changes must keep CLI contracts synchronized.

### Testing and Validation

- Run relevant Go tests (`go test`) when code in this domain changes.
- Keep operational logging sufficient for incident debugging and audit reconstruction.
