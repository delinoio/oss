### Instructions for `servers/`

- Follow `docs/project-monorepo.md` and server-specific docs before implementation.
- Write all source and comments in English.
- Prefer enums or typed constants over free-form strings for API contracts.

### Scope in This Domain

- `servers/thenv`: Backend for secure environment sharing.

### Server Rules

- Keep API boundaries explicit and versionable.
- Prioritize Connect RPC-based communication for business flows over Tauri-specific bindings.
- Keep authorization and audit behavior documented and testable.
- Never expose secret values in logs or default API responses.

### Integration Rules

- Changes to server interfaces must be synchronized with `cli/thenv` and `apps/devkit/src/apps/thenv` contracts.
- Update `docs/project-thenv.md` for every interface or trust model update.

### Testing and Validation

- Run relevant Go tests (`go test`) when code in this domain changes.
- Keep operational logging sufficient for incident debugging and audit reconstruction.
