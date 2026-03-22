# Project: dexdex

## Goal
Define DexDex as a Connect RPC-first orchestration platform for CLI coding agents across desktop client, control-plane server, execution-plane server, and shared proto contracts.

This project index is the canonical architecture and behavior contract for DexDex in this repository.
When implementation details differ from documented contracts, follow-up sync work is required.

## Project ID
`dexdex`

## Domain Ownership Map
- `apps/dexdex` (`desktop-app`)
- `servers/dexdex-main-server` (`main-server`)
- `servers/dexdex-worker-server` (`worker-server`)
- `protos/dexdex/v1` (`shared-v1-contract`)

## Domain Contract Documents
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/apps-dexdex-ui-contract.md`
- `docs/apps-dexdex-user-guide-contract.md`
- `docs/apps-dexdex-notification-contract.md`
- `docs/apps-dexdex-workspace-connectivity-contract.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/servers-dexdex-worker-server-foundation.md`
- `docs/servers-dexdex-event-streaming-contract.md`
- `docs/servers-dexdex-pr-management-contract.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/protos-dexdex-api-contract.md`
- `docs/protos-dexdex-entities-contract.md`
- `docs/protos-dexdex-plan-mode-contract.md`

## Cross-Domain Invariants
- Connect RPC is the canonical business contract for DexDex; Tauri-native APIs are integration-only.
- Main server is the canonical business API boundary for clients; direct client-to-worker business calls are out of scope.
- Workspace is the top-level scope boundary and supports two connectivity types:
  - `LOCAL_ENDPOINT`
  - `REMOTE_ENDPOINT`
- RepositoryGroup is the execution unit, and repository ordering is deterministic.
- `CreateUnitTask` accepts a repository-group selector or repository selector; repository selector resolves to a deterministic singleton RepositoryGroup.
- Execution is worktree-only for task runs; direct local-folder editing is out of scope.
- Worker output that changes code must produce a real git commit chain.
- PR creation and commit-to-local flows must use commit-chain metadata as source of truth.
- Plan mode is explicit and decision-driven (`APPROVE`, `REVISE`, `REJECT`) at SubTask execution boundaries.
- Event streaming is workspace-scoped, sequence-based, and reconnect-safe within retention policy.
- Notifications are event-stream driven; the in-app center is authoritative.
- UI behavior is keyboard-first and includes multiline submit (`Cmd+Enter`) and tab lifecycle shortcuts.
- Dialog UI surfaces close with `Esc`, and forms with a single critical input auto-focus when shown.

## Implementation Status (as of 2026-03-18)

### Proto (`protos/dexdex/v1/dexdex.proto`)
- `CreateUnitTaskRequest` is prompt-first and supports repository-group or repository selectors (`repository_group_id` / `repository_id`) with exactly-one validation.
- `AgentCapability` includes `supports_plan_mode`.
- `SubmitPlanDecision` supports explicit decision actions.
- Event stream payloads are workspace-scoped and typed.
- All core enums implemented: `WorkspaceType`, `BadgeColorKey`, `ReviewAssistStatus`, `ReviewInlineCommentStatus`, `DiffSide`.
- All service methods implemented across `WorkspaceService`, `TaskService`, `SessionService`, `PrManagementService`, `ReviewAssistService`, `BadgeThemeService`, and `EventStreamService`.

### Main Server (`servers/dexdex-main-server`)
- Repository and repository-group contracts are normalized and execution-order-aware.
- Repository selector requests create/reuse deterministic system-managed singleton groups (`auto-repo-singleton-<repository_id>`) and enforce singleton-group invariants.
- System-managed singleton groups are reserved for internal orchestration and blocked from user update/delete APIs.
- Workspace settings and task orchestration contracts are Connect RPC-first.
- Plan-mode and capability validations enforce typed error outcomes.
- PR auto-detection from agent session output after execution completes.
- Review assist items auto-created from GitHub review comments on CHANGES_REQUESTED.
- ReviewAssistUpdatedPayload added to event streaming.
- GitHub client supports CreatePullRequest and ListPullRequestComments.
- Workspace CRUD operations: `CreateWorkspace`, `UpdateWorkspace`, `DeleteWorkspace`, `SetActiveWorkspace`.
- Task cancellation and subtask management: `CancelUnitTask`, `CancelSubTask`, `CreateSubTask`, `ListSubTaskCommits`, `RetrySubTask`.
- PR tracking and auto-fix control: `TrackPullRequest`, `RunAutoFixNow`, `SetAutoFixPolicy`.
- `RunAutoFixNow` creates and immediately dispatches a remediation SubTask.
- Poller-driven automatic remediation for `auto_fix_enabled` policy remains planned.
- Review assist resolution: `ResolveReviewAssistItem`.
- Badge theme management: `ListBadgeThemes`, `UpsertBadgeTheme`.
- Agent session lifecycle: `ListAgentSessions`, `GetAgentSessionLog`, `StopAgentSession`.

### Worker Server (`servers/dexdex-worker-server`)
- Agent capability and execution contracts expose plan-mode support boundaries.
- Execution remains repository-group scoped and worktree-only.
- Worker logs and outputs are normalized for main-server and client consumption.
- Agent process execution includes timeout enforcement, idle detection, and stderr capture.
- Exit codes are mapped to specific error messages for diagnostic clarity.
- Commit chain extraction from worktree after successful execution with stream delivery.

### Desktop App (`apps/dexdex`)
- Task creation flows and sidebar-first repository administration (`/repository-groups`, `/repositories`) are aligned with workspace/repository-group/agent contracts.
- Create Task dialog uses a unified repository-target selector (Repository Groups + Repositories) and sends selector-specific fields to `CreateUnitTask`.
- System-managed singleton groups are hidden in Repository Groups management views.
- Task list/detail metadata renders repository labels for singleton-group-backed tasks.
- Plan-mode visibility follows capability metadata.
- Dialog surfaces close with `Esc` and single critical-input forms auto-focus on open.
- Review assist Accept action creates auto-fix UnitTask via CreateUnitTask API.
- Global shortcut navigates to waiting session context with input form auto-focus.
- Tauri backend implements credential management (file-based) and tray status IPC.
- Tauri development runtime forwards WebView `console.*` logs to terminal output through the log plugin bridge.
- Cancel/Stop buttons for running UnitTask and SubTask flows with immediate propagation.
- Workspace switching with dynamic selector for workspace-scoped navigation; all components use active workspace from app store with query invalidation on switch.
- Startup workspace reconciliation migrates persisted legacy workspace ID `workspace-default` to canonical `ws-default`, falls back to the first available workspace when persisted data is invalid, and keeps active workspace empty when no workspace exists.
- Workspace-scoped query/stream/tray/global-shortcut flows are guarded and skip RPC calls when active workspace is empty.
- Sidebar workspace dropdown includes click-outside-to-close and create workspace action.
- PR management list and detail pages with track/auto-fix controls.
- PR detail page includes review comment actions (resolve/reopen/delete/edit), new comment form with file/side/line anchoring, and commit chain display from linked subtasks.
- Inbox page with real notification data from event stream.
- Repositories page blocks create/update/delete actions when no active workspace is selected and renders inline validation/mutation error messages.
- Enhanced keyboard shortcuts fully wired: `Cmd+T` (new task), `Cmd+W` (close tab), `J`/`K` (navigate list with visual selection), `A` (approve plan), `V` (revise plan), `Shift+X` (reject plan/cancel task), `Cmd+Shift+[`/`]` (switch tabs).
- Diff viewer component with unified/split view toggle, file-level navigation for multi-file diffs, and line-level comment anchor buttons.
- Draft form state preservation via Zustand store with localStorage persistence per workspace.

## Developer Setup and Validation
Repository layout for DexDex in this monorepo:
- `apps/dexdex`
- `servers/dexdex-main-server`
- `servers/dexdex-worker-server`
- `protos/dexdex`

Prerequisites:
- Go (as pinned by repository toolchain)
- Node.js + pnpm
- Rust toolchain for Tauri host runtime
- SQLite for single-instance mode
- PostgreSQL + Redis for scale mode

Bootstrap and validation checklist:
- `pnpm install`
- `go test ./servers/dexdex-main-server/...`
- `go test ./servers/dexdex-worker-server/...`
- `cd apps/dexdex && pnpm test`
- `cd protos/dexdex && buf lint && buf build`

Recommended runtime environment keys:
- `DEXDEX_DEPLOYMENT_MODE`
- `DEXDEX_HTTP_ADDR`
- `DEXDEX_DATABASE_URL`
- `DEXDEX_REDIS_URL` (`SCALE` mode)
- `DEXDEX_PR_POLL_INTERVAL_SEC`
- `DEXDEX_WORKTREE_ROOT`

## Release Distribution Contracts
- Release workflow: `.github/workflows/release-dexdex.yml`.
- GitHub Releases publish signed desktop and server artifacts (`SHA256SUMS` + cosign signatures).
- Homebrew distribution:
  - `dexdex` cask consumes the macOS desktop DMG release artifact.
  - `dexdex-main-server` and `dexdex-worker-server` formulas consume prebuilt server release artifacts for `darwin/amd64`, `darwin/arm64`, and `linux/amd64`.
  - Linux `arm64` Homebrew prebuilt server artifacts are currently out of scope.
- winget distribution remains prebuilt-asset based for:
  - `DelinoIO.DexDex`
  - `DelinoIO.DexDexMainServer`
  - `DelinoIO.DexDexWorkerServer`

## Change Policy
- Any DexDex API, entity, plan-mode, event-streaming, or connectivity contract change must update `docs/project-dexdex.md` and the related domain contract docs in the same change.
- If remote-source contract behavior is adopted before local proto/code sync, keep an explicit alignment note in the changed docs.
- Any path ownership or component-boundary change must update this index and `AGENTS.md` files in the same change.

## References
- `docs/README.md`
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/protos-dexdex-api-contract.md`
- `docs/protos-dexdex-entities-contract.md`
- `docs/protos-dexdex-plan-mode-contract.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/servers-dexdex-worker-server-foundation.md`
