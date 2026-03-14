# apps-dexdex-desktop-app-foundation

## Scope
- Project/component: DexDex desktop app contract
- Canonical path: `apps/dexdex`
- Product surface: Tauri-hosted React desktop client for DexDex orchestration

## Runtime and Language
- Runtime: Tauri + React
- Primary language: TypeScript (UI/data), Rust (host integration)

## Interface Contracts
- Desktop business communication is Connect RPC-first through `main-server`.
- Task creation UI is prompt-first:
- No title/description inputs.
- Required: prompt, repository group, coding agent.
- Optional: plan mode toggle (visible only if selected agent supports it).
- Task labels in lists/tabs/detail use prompt-derived summaries.
- Settings page is tabbed:
- `General`
- `Agents`
- `Repository Groups`
- `Repositories`
- `Agents` tab manages workspace default coding agent through workspace settings RPC.
- `Repositories` tab provides full repository CRUD.
- `Repository Groups` tab provides full repository-group CRUD with ordered members.
- Existing task/session/review/notification flows remain stream-driven and workspace-scoped.

## Data and UX Invariants
- `WorkspaceSettings.default_agent_cli_type` is the default agent for new task creation.
- Plan mode default is OFF.
- Plan mode toggle is shown only for agents where `supports_plan_mode=true`.
- Repository-group member order in UI maps directly to `display_order` payload sequence.
- Dialog UI surfaces must close with `Esc`.
- Forms with a single critical input must focus that input when shown.

## Build and Test
- Local tests: `pnpm --filter dexdex test`
- Build: `pnpm --filter dexdex build`
- Packaging: `pnpm --filter dexdex tauri:build`
- Required behavioral coverage:
- prompt-only task create submission payload
- plan mode visibility by agent capability
- tabbed settings navigation
- repository CRUD and repository-group CRUD interactions

## Dependencies and Integrations
- Shared RPC/contracts from `protos/dexdex/v1`
- Server-state with React Query + `@connectrpc/connect-query`
- Main-server event stream + unary RPC integrations

## Change Triggers
- Update this file with `docs/project-dexdex.md` when desktop UX contracts change.
- Update this file with `docs/protos-dexdex-v1-contract.md` when proto changes affect desktop behavior.
- Keep app/server/proto contracts synchronized in the same change.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/servers-dexdex-worker-server-foundation.md`
- `docs/domain-template.md`
