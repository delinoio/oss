# apps-dexdex-desktop-app-foundation

## Scope
- Project/component: DexDex desktop app contract
- Canonical path: `apps/dexdex`
- Product surface: Tauri-hosted DexDex client contract for desktop-first workflows with mobile-ready parity rules

## Runtime and Language
- Runtime: Tauri application shell with React web UI and Rust adapter boundary
- Primary language: TypeScript (UI/data layer) and Rust (host integration)

## Users and Operators
- End users orchestrating coding-agent workflows across repositories and pull requests
- Reviewers and operators triaging plan decisions, remediation flows, and notifications
- Maintainers shipping multi-OS desktop artifacts and evolving mobile-compatible UX contracts

## Interfaces and Contracts
- Stable component identifier: `desktop-app`.
- Business communication is Connect RPC-first and flows through `main-server` APIs.
- Tauri-specific APIs are adapter-only and are restricted to platform integration (window lifecycle, keychain/storage wrappers, file picker, deep links).
- Client must not call worker business internals directly.
- Workspace contract:
- supports `LOCAL_ENDPOINT` and `REMOTE_ENDPOINT` connectivity types
- workspace-scoped routing, tab state, and cache boundaries are required
- Multi-tab contract:
- opened items are tab-addressable routes (tasks, subtasks, PRs, review items, settings)
- tab order, active tab, and unsaved draft state are preserved per workspace
- tab indicators expose running, action-required, and unread signals
- Event stream contract:
- subscribe via `EventStreamService.StreamWorkspaceEvents`
- maintain and persist last applied sequence
- reconnect with sequence resume and idempotent reducers
- stream-driven cache update/invalidation is required for near-real-time UX
- Keyboard and input invariants:
- shortcut matching uses physical key codes and modifiers (IME-language independent)
- global + screen-scoped shortcuts are required for primary screens and tab management
- multiline forms use `Enter` for newline and `Cmd+Enter` for submit across task, plan, PR, review, and inline comment flows
- Plan mode UI contract:
- show proposal state and explicit decision controls (`Approve`, `Revise`, `Reject`)
- submit decisions through task RPC and preserve decision history in timeline UX
- Inline comment contract:
- line-level anchors (`filePath`, `side`, `lineNumber`) in diff context
- create/edit/resolve/reopen/delete flows via review comment RPCs
- `INLINE_COMMENT_UPDATED` stream events synchronize thread state
- Approved-diff PR action contract:
- after diff approval, UI exposes `Create PR`
- action triggers `TaskService.CreateSubTask` semantics with `type = PR_CREATE` and prompt `Create A PR`
- resulting subtask/session progress stays in the same task timeline
- Stop action contract:
- in-progress UnitTask -> immediate stop action (`CancelUnitTask`)
- in-progress SubTask -> immediate stop action (`CancelSubTask`)
- cancellation status is stream-synchronized
- Notification contract:
- permission request at app startup
- in-app notification center is authoritative
- dedup key uses workspace + sequence + notification type semantics
- Web data access contract:
- unary RPC flows use generated `@connectrpc/connect-query` hooks
- server-state management follows `@tanstack/react-query` patterns
- business flows must not use ad-hoc `fetch` calls
- query keys and cache scopes are workspace-isolated
- Implemented-vs-planned alignment:
- current runtime implementation under `apps/dexdex/src` is scaffold-phase UI (logo/greeting form) and does not yet implement the full workflow contract above
- this document remains the target product contract for staged reintegration work

## Storage
- Client-local persisted state includes active workspace pointer, workspace-scoped tab metadata, and user UI preferences (appearance, notification preference, shortcut discoverability).
- Notification read/unread and dedup markers persist across restarts and synchronize with server-side records.
- React Query cache is workspace-scoped to avoid cross-workspace leakage.
- Current scaffold implementation stores only transient in-memory form state; reintegration must explicitly document persistence semantics per feature surface.

## Security
- Remote workspace traffic uses authenticated Connect RPC flows with workspace-scoped authorization semantics enforced server-side.
- Client must avoid storing secrets in plaintext logs or unscoped local storage.
- Credential bridge/import flows and worker-env profile settings are staged features and require least-privilege scoping and auditable handling.
- Notification payload rendering and deep-link routing must avoid leaking sensitive task/session payloads.

## Logging
- Client-side structured logs are required for stream connect/disconnect, resume/replay outcomes, notification permission/dispatch outcomes, and user-triggered remediation actions.
- Logs should include workspace/task/session correlation IDs when available and redact secrets or auth tokens.
- Scaffold-phase logs should remain minimal while preserving safe diagnostics for integration debugging.

## Build and Test
- Local validation: `pnpm --filter dexdex test`
- Build validation: `pnpm --filter dexdex build`
- Packaging contract: `pnpm --filter dexdex tauri:build`
- CI alignment: `node-dexdex-test` and `dexdex-desktop-build` workflow contract

## Dependencies and Integrations
- Runtime stack: Tauri host (`src-tauri`) + React UI (`src`).
- Primary business integration target: `servers/dexdex-main-server` via shared `protos/dexdex/v1` contracts.
- Downstream workflow relationships: PR/remediation/review/comment/notification flows backed by main-server control-plane services.
- Worker execution details are consumed indirectly through normalized main-server APIs and stream events.
- React Query and Connect Query are the expected server-state and RPC integration layer once business UX reintegration progresses.

## Change Triggers
- Update this file and `docs/project-dexdex.md` when client UX contracts, keyboard rules, workspace semantics, or adapter boundaries change.
- Update this file with `docs/protos-dexdex-v1-contract.md` when RPC enums, message shapes, or stream payload contracts affecting client behavior change.
- Synchronize with `docs/servers-dexdex-main-server-foundation.md` when business routing, notification, or event-stream orchestration contracts change.
- Keep scaffold-vs-target notes current when implementation coverage changes materially.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/servers-dexdex-worker-server-foundation.md`
- `docs/domain-template.md`
- Upstream source docs merged into this contract:
- `https://github.com/delinoio/dexdex/blob/main/docs/tauri-app.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/ui.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/user-guide.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/workspace-connectivity.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/developer-setup.md`
