### Instructions for `servers/`

- Follow root `AGENTS.md` and the owning project/domain contracts.
- Keep service contracts, ownership, security, retention, and deployment prerequisites in `docs/` before runtime implementation.
- Write Go code and comments in English; use `log/slog` structured logging and never log secrets or sensitive payloads.

### Scope in This Domain

- `servers/devhud-api`: planned stateless Go DevHud API.

### DevHud Rules

- `servers/devhud-api` uses fixed development port `46307`, PostgreSQL, Cloudflare R2 signed uploads, external Logto, Connect RPC, and a non-root OCI artifact.
- It must not proxy GitHub or upload bodies, broker credentials, poll Decks, receive GitHub webhooks, or persist Deck results.
- Keep `docs/servers-devhud-api-contract.md`, `docs/protos-devhud-v1-contract.md`, and `docs/project-devhud.md` synchronized with every interface or operational change.
