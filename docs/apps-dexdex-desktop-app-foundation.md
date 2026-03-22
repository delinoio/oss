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
- React UI layer for Workspace, UnitTask, PR Management, PR Review Assist, Repository Groups, Repositories, Settings, Notifications.
- Data layer with `@connectrpc/connect-query` and `@tanstack/react-query`.
- Stream subscriber with sequence resume behavior.

Behavior contracts:
- Workspace switching is first-class and workspace-scoped, with a dynamic selector for switching between workspaces.
- Active workspace selection is reconciled against the fetched workspace list at startup and refresh time:
  - legacy persisted ID `workspace-default` must be migrated to canonical `ws-default` when present
  - invalid persisted IDs must fall back to the first available workspace
  - if no workspaces exist, active workspace remains empty and the UI must show a workspace creation hint
- Multi-tab UI preserves tab order, active tab, and draft form state by workspace.
- Event stream subscription reconnects with sequence resume.
- Create Task uses a unified repository-target selector that includes both Repository Groups and Repositories.
- Repository-target submission must send exactly one selector field to `CreateUnitTask` (`repository_group_id` or `repository_id`).
- Tasks created from repository selector must render repository-friendly metadata instead of internal singleton-group IDs.
- Workspace-scoped query/stream/tray/global-shortcut flows must not execute workspace RPC calls when active workspace ID is empty.
- Plan mode UX supports `Approve`, `Revise`, and `Reject` decision actions.
- Inline comment UX supports create/edit/resolve/reopen/delete with stream synchronization.
- Multiline submit contract: `Enter` newline, `Cmd+Enter` submit.
- Cancel/Stop controls provide immediate cancellation for running UnitTask and SubTask flows via `CancelUnitTask` and `CancelSubTask` APIs.
- Approved diff flow exposes `Create PR` action and uses commit-chain metadata.
- PR management pages: list view of tracked PRs, detail view with auto-fix controls (`RunAutoFixNow`, `SetAutoFixPolicy`), and `TrackPullRequest` action.
- Repository administration pages are first-class sidebar routes (`/repository-groups`, `/repositories`) and are not nested under Settings tabs.
- Repositories screen must block create/update/delete actions when no active workspace is selected and display inline validation/mutation errors for failed actions.
- Inbox page renders real notification data from the event stream with read/unread state management.
- Enhanced keyboard shortcuts:
  - `Cmd+T`: create new task
  - `Cmd+W`: close active tab
  - `J` / `K`: navigate up/down in list views
  - `A`: approve waiting plan
  - `V`: open revise input for waiting plan
  - `Shift+X`: reject waiting plan or cancel running task
- Diff viewer component for inline review of commit changes with side-by-side and unified modes.

Data and UX invariants:
- `WorkspaceSettings.default_agent_cli_type` is the default agent for new task creation.
- Plan mode default is OFF.
- Plan mode toggle is shown only for agents where `supports_plan_mode=true`.
- Repository-group member order in UI maps directly to `display_order` payload sequence.
- System-managed singleton repository groups (`auto-repo-singleton-*`) are hidden from Repository Groups management screens.
- Dialog UI surfaces must close with `Esc`.
- Forms with a single critical input must focus that input when shown.

Notifications contract:
- Notification dispatch uses Web Notification API.
- In-app notification center is authoritative.
- Duplicate prevention is sequence-driven.

## Storage
Client-owned state contracts include:
- workspace list and active workspace pointer (active pointer may be empty when no workspaces exist)
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
- Tauri development runtime forwards WebView `console.*` logs to terminal output for local debugging.

## Build and Test
- `cd apps/dexdex && pnpm test`
- `cd apps/dexdex && pnpm build`
- `cd apps/dexdex && pnpm tauri:build`
- CI iOS smoke build (unsigned): `pnpm --filter dexdex exec vite build`, then `pnpm --filter dexdex exec tauri ios init --ci`, then `pnpm --filter dexdex exec tauri ios build --ci --debug --target aarch64-sim --no-build`

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
