# Feature: architecture

## Architecture
- Main server (`servers/dexdex-main-server`) is the control-plane Go service scaffold
: It serves `WorkspaceService` (`GetWorkspace`, `GetWorkspaceOverview`), `RepositoryService` (`GetRepositoryGroup`, `ListRepositoryGroups`), `TaskService` (`GetUnitTask`, `GetSubTask`, `ListUnitTasks`, `ListSubTasks`, `SubmitPlanDecision`, `RunSubTaskSessionAdapter`), `SessionService` (`GetSessionOutput`, `ListSessions`), `PrManagementService` (`GetPullRequest`, `ListPullRequests`), `ReviewAssistService.ListReviewAssistItems`, `ReviewCommentService.ListReviewComments`, `BadgeThemeService.GetBadgeTheme`, `NotificationService.ListNotifications`, and `EventStreamService.StreamWorkspaceEvents` over Connect RPC.
: It keeps workspace task/subtask/event state in memory and starts with an empty workspace set.
: It provides filtered list/index read models with page-size + page-token pagination for product UI list surfaces.
: It provides replay + live-tail stream delivery with retention validation and keepalive heartbeat frames.
: It orchestrates worker-driven session adapter normalization and materializes session/subtask stream events in order.
: It uses structured logs via `log/slog`.
- Worker server (`servers/dexdex-worker-server`) is the execution-plane Go service scaffold
: It serves `WorkerSessionAdapterService.NormalizeSessionOutputFixture` over Connect RPC.
: It validates ordered real commit-chain metadata emitted by SubTask execution.
: It normalizes provider-native CLI output streams into one session-output contract.
: It supports fixture presets and raw JSONL input for deterministic session adapter execution.
: It uses structured logs via `log/slog`.
- Desktop app (`apps/dexdex`) is the orchestration client shell
: It starts at a workspace picker (`/`) and requires workspace selection before entering desktop role routes.
: It resolves workspace mode into one normalized Connect RPC connection contract during picker open.
: It renders a three-panel desktop information architecture (left navigation, center page container, right action center) with dark-first styling.
: It exposes route-scoped multi-page workflows aligned with Codex desktop roles (`/projects`, `/threads`, `/review`, `/automations`, `/worktrees`, `/local-environments`, `/settings`).
: It guards desktop role routes when no active workspace session is selected and redirects to startup picker.
: It provides a shared React Query + Connect Query transport scaffold for RPC data flows.
: It replaces RPC card dashboards with product-first page containers:
: `Projects` -> workspace overview + repository groups + active task summary.
: `Threads` -> inbox + detail timeline + action center.
: `Review` -> PR queue + review assist/comment context.
: `Worktrees` -> session list + stream timeline with incremental cache updates.
: It provides local-store backed usable pages for `Automations`, `Local Environments`, and `Settings` (create/update/delete/toggle + last-selected restore + diagnostics history).
: It surfaces right-panel action context for plan decision/session adapter execution based on current shared selection state.
: It applies resolved workspace token values as `Authorization: Bearer <token>` request headers when token is present.
: It binds selected `workspace_id` globally and shares selected IDs (`selected_unit_task_id`, `selected_sub_task_id`, `selected_session_id`, `selected_pr_tracking_id`) across page containers and action center.
: It stores workspace profile metadata locally (without token persistence) and keeps active session state in memory.
: Post-resolution behavior stays identical between `LOCAL` and `REMOTE` modes.
- Shared proto (`protos/dexdex/v1/dexdex.proto`) is the canonical contract surface for cross-runtime integrations.

