### Instructions for `servers/`

- Follow root `AGENTS.md`, project index docs, and relevant `docs/servers-*.md` contracts before implementation.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Write all source and comments in English.
- Prefer enums or typed constants over free-form strings for API contracts.

### Scope in This Domain

- `servers/thenv`: Backend for secure environment sharing.
- `servers/commit-tracker`: Commit Tracker API server component.
- `servers/dexdex-main-server`: DexDex control-plane Go server scaffold.
- `servers/dexdex-worker-server`: DexDex execution-plane Go server scaffold.

### Server Language and Data Rules

- Servers in this domain must be implemented in Go.
- SQL queries and type-safe data access must use `sqlc`.
- Protobuf definitions should live at `proto/<service_name>/v1/*.proto` unless a project contract explicitly uses a shared cross-runtime proto root.
- DexDex server contracts use shared proto definitions at `protos/dexdex/v1/*.proto`.
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
- Update `docs/project-devkit-commit-tracker.md` and `docs/servers-devkit-commit-tracker-api-server-foundation.md` for every commit-tracker API contract update.
- Update `docs/project-dexdex.md` and relevant DexDex server-domain docs for every server interface or ownership contract update.

### Multi-Component Contract Sync

- `servers/commit-tracker` changes must keep collector and web contracts synchronized.
- `servers/thenv` changes must keep CLI and web-console contracts synchronized.
- `servers/dexdex-main-server` and `servers/dexdex-worker-server` changes must keep proto and desktop contracts synchronized.

### Testing and Validation

- Run relevant Go tests (`go test`) when code in this domain changes.
- Keep operational logging sufficient for incident debugging and audit reconstruction.
