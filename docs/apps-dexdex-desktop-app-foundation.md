# apps-dexdex-desktop-app-foundation

## Scope
- Project/component: DexDex Tauri app contract (desktop-first with mobile-capable contract surface)
- Canonical path: `apps/dexdex`
- Product surface: React client hosted in Tauri runtime for task/PR/review orchestration

## Runtime and Language
- Runtime: Tauri + React + Connect RPC clients
- Primary language: TypeScript (web client), Rust (Tauri host integration)

## Users and Operators
- End users running DexDex desktop/mobile clients
- Product engineers implementing task/PR/review workflows
- Operators validating stream reliability and workspace connectivity

## Interfaces and Contracts
Contract alignment note:
- This contract defines the DexDex app behavior target for this repository.
- Local implementation may temporarily lag while API/entity contracts are synchronized.

Core app rules:
- Business communication is Connect RPC-first.
- Tauri APIs are integration-only (window lifecycle, keychain, file picker, deep links).
- Client consumes normalized session output contracts only.
- Client does not call worker business APIs directly.

Client architecture:
- React UI layer for Workspace, UnitTask, PR Management, PR Review Assist, Settings, Notifications.
- Data layer with `@connectrpc/connect-query` and `@tanstack/react-query`.
- Stream subscriber with sequence resume behavior.

Behavior contracts:
- Workspace switching is first-class and workspace-scoped.
- Multi-tab UI preserves tab order, active tab, and draft form state by workspace.
- Event stream subscription reconnects with sequence resume.
- Plan mode UX supports `Approve`, `Revise`, and `Reject` decision actions.
- Inline comment UX supports create/edit/resolve/reopen/delete with stream synchronization.
- Multiline submit contract: `Enter` newline, `Cmd+Enter` submit.
- Stop actions are immediate for running UnitTask and SubTask flows.
- Approved diff flow exposes `Create PR` action and uses commit-chain metadata.

Notifications contract:
- Notification dispatch uses Web Notification API.
- In-app notification center is authoritative.
- Duplicate prevention is sequence-driven.

## Storage
Client-owned state contracts include:
- workspace list and active workspace pointer
- workspace-scoped tab state and draft preservation
- stream sequence checkpoint per workspace
- notification permission cache and unread/read presentation state
- local UI preferences (appearance, shortcuts, badge settings)

## Security
- Non-localhost workspaces use TLS and bearer-token based auth.
- Workspace credentials stay in platform-secure storage integrations.
- Business payload rendering uses normalized, validated stream/event contracts.

## Logging
Client logs should include:
- stream open/reconnect/resume outcomes
- notification permission and dispatch outcomes
- plan decision submissions
- immediate stop action dispatch and result states

## Build and Test
- `cd apps/dexdex && pnpm test`
- `cd apps/dexdex && pnpm build`
- `cd apps/dexdex && pnpm tauri:build`

## Dependencies and Integrations
- Proto/API/entity contracts:
  - `docs/protos-dexdex-v1-contract.md`
  - `docs/protos-dexdex-api-contract.md`
  - `docs/protos-dexdex-entities-contract.md`
- Main-server contract: `docs/servers-dexdex-main-server-foundation.md`
- Worker contract: `docs/servers-dexdex-worker-server-foundation.md`
- UI detail contracts:
  - `docs/apps-dexdex-ui-contract.md`
  - `docs/apps-dexdex-user-guide-contract.md`
  - `docs/apps-dexdex-notification-contract.md`
  - `docs/apps-dexdex-workspace-connectivity-contract.md`

## Change Triggers
- Any client workflow or UX contract change must update this file and `docs/project-dexdex.md` in the same change.
- API/entity/plan-mode changes must synchronize this file with proto and server docs in the same change.

## References
- `docs/project-dexdex.md`
- `docs/apps-dexdex-ui-contract.md`
- `docs/apps-dexdex-user-guide-contract.md`
- `docs/apps-dexdex-notification-contract.md`
- `docs/apps-dexdex-workspace-connectivity-contract.md`
- `docs/protos-dexdex-v1-contract.md`
