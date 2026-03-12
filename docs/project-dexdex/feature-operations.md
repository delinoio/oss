# Feature: operations

## Storage
Main server scaffold ownership:
- In-memory task/subtask maps per workspace with empty-on-boot default state
- In-memory workspace/repository/session/pr/review/badge/notification read-model maps per workspace
- In-memory workspace event ring buffer with configurable retention
- In-memory live subscriber registry per workspace
- Non-blocking subscriber fan-out with explicit drop policy when subscriber buffers are full
- Empty workspace entries created for stream-only sessions are garbage-collected when the last subscriber disconnects

Worker server scaffold ownership:
- In-memory commit-chain validation logic (`sha`, parent links, message, timestamp ordering)
- In-memory session-output normalization logic and fixture-backed parser validation

Desktop scaffold storage contract:
- Saved workspace profile metadata is persisted in local storage (`workspaceId`, `mode`, optional `remoteEndpointUrl`, `lastUsedAt`)
- Active workspace session (`workspaceId` + `ResolvedWorkspaceConnection`) remains in-memory only
- Remote token values are never persisted and are entered per open action

Future deployment mode storage contract (reserved):
- `SINGLE_INSTANCE`: SQLite + in-process event broker
- `SCALE`: PostgreSQL + Redis streams/pub-sub


## Security
- Use TLS for non-localhost Connect RPC endpoints.
- Enforce bearer token authentication and workspace-scoped authorization in full server implementations.
- Validate repository URLs, branch refs, prompts, and review payloads before execution.
- Keep provider-native raw payloads worker-local; never expose them in main-server APIs.
- Never log secrets, tokens, or plaintext sensitive material.
- Desktop `LOCAL` mode resolution must avoid token value logging and expose normalized Connect metadata only.


## Logging
- Main and worker Go server scaffolds use `log/slog` structured logging.
- Required correlation fields for full runtime implementations:
: `workspace_id`
: `unit_task_id`
: `sub_task_id`
: `session_id`
: `pr_tracking_id`
: `request_id`
- Baseline scaffold events:
: server scaffold start (`component`, `result`)
: plan decision/replay validation failures with typed error codes
: stream open/close transitions and heartbeat send failures
: subscriber backpressure drops with fixed `policy=drop`
: commit-chain validation failures with typed error codes
- Prohibited log content:
: raw provider tokens
: provider-native secret payloads
: plaintext secret material


## Build and Test
Current local validation commands:
- `cd protos/dexdex && buf lint`
- `cd protos/dexdex && buf build`
- `./scripts/generate-go-proto.sh`
- `pnpm --filter dexdex run gen:proto`
- `go test ./servers/dexdex-main-server/...`
- `go test ./servers/dexdex-worker-server/...`
- `go test ./...`
- `cargo test`
- `pnpm --filter dexdex test`
- `pnpm --filter dexdex run test:visual`
- `cd apps/dexdex && pnpm test`
- `cd apps/dexdex && pnpm run test:visual`
- Visual baseline references (phase 1):
: `https://developers.openai.com/codex/overview`
: `https://developers.openai.com/codex/features`
: `https://developers.openai.com/codex/review-comments`
: `https://developers.openai.com/codex/projects`
: `https://developers.openai.com/codex/local-environments`
: `https://developers.openai.com/codex/settings`
- Distribution pipeline:
: `.github/workflows/release-dexdex.yml`
: tag trigger: `dexdex@v*`
: `workflow_dispatch` supports `version` and `dry_run`
- Release artifact contract:
: Desktop: `dexdex-desktop-linux-amd64.AppImage`, `dexdex-desktop-darwin-universal.dmg`, `dexdex-desktop-windows-amd64.msi`
: Main server: `dexdex-main-server-{linux|darwin|windows}-{amd64|arm64}.(tar.gz|zip)`
: Worker server: `dexdex-worker-server-{linux|darwin|windows}-{amd64|arm64}.(tar.gz|zip)`
: Integrity/signature set: `SHA256SUMS` + per-artifact cosign signatures (`*.sig`, `*.pem`)
- Package-manager publication integration:
: Homebrew updates via `scripts/release/update-homebrew.sh` (`dexdex`, `dexdex-main-server`, `dexdex-worker-server`)
: winget updates via `scripts/release/update-winget.sh` (`DelinoIO.DexDex`, `DelinoIO.DexDexMainServer`, `DelinoIO.DexDexWorkerServer`)
- Desktop signing/notarization contract:
: macOS signing/notarization uses GitHub Actions secrets (`DEXDEX_APPLE_CERTIFICATE_BASE64`, `DEXDEX_APPLE_CERTIFICATE_PASSWORD`, `DEXDEX_APPLE_SIGNING_IDENTITY`, `DEXDEX_APPLE_ID`, `DEXDEX_APPLE_PASSWORD`, `DEXDEX_APPLE_TEAM_ID`)
: Windows signing uses GitHub Actions secrets (`DEXDEX_WINDOWS_CERTIFICATE_BASE64`, `DEXDEX_WINDOWS_CERTIFICATE_PASSWORD`)

Main server runtime configuration:
- `DEXDEX_MAIN_SERVER_ADDR` (default: `127.0.0.1:7878`)
- `DEXDEX_MAIN_STREAM_RETENTION` (default: `256`)
- `DEXDEX_MAIN_STREAM_HEARTBEAT_INTERVAL` (default: `15s`, Go duration format)
- `DEXDEX_WORKER_SERVER_URL` (default: `http://127.0.0.1:7879`)

Worker server runtime configuration:
- `DEXDEX_WORKER_SERVER_ADDR` (default: `127.0.0.1:7879`)

Acceptance-focused scenarios:
1. Approve decision resumes current SubTask from waiting-plan state.
2. Revise decision requires non-empty revision note and creates queued request-changes SubTask.
3. Revise decision server-generates a new SubTask ID with deterministic prefix `<workspace_id>-subtask-`.
4. Reject decision cancels current SubTask and creates no follow-up SubTask.
5. Replay uses exclusive cursor semantics (`sequence > from_sequence`).
6. Replay rejects non-monotonic sequence streams.
7. Replay reports cursor-out-of-range with earliest available sequence details.
8. Live tail receives newly published SubTask update events after replay completion.
9. Stream subscriber lifecycle is cleaned up on client-side cancellation.
10. Backpressure policy drops events for saturated subscriber buffers without blocking publishers.
11. Worker accepts ordered real commit chains with valid parent linkage.
12. Worker rejects empty chains, missing parent links, and non-monotonic commit time.
13. Desktop workspace resolution continues to return normalized `CONNECT_RPC` connection metadata.
14. Worker normalizes Codex CLI `turn.failed` events as terminal session output errors.
15. Worker normalizes Claude Code stream deltas and final assistant text into distinct event types.
16. Worker preserves OpenCode `step_start` -> `text` -> `step_finish` event ordering.
17. Worker converts malformed JSON source lines into non-terminal parse-error output events.
18. Main server unary handlers return `NotFound` for unknown workspace/resource IDs and `InvalidArgument` for missing required fields.
19. `GetSessionOutput`, `ListReviewAssistItems`, `ListReviewComments`, and `ListNotifications` return empty arrays when workspace exists but no records are present.
20. `GetWorkspaceOverview`, `ListRepositoryGroups`, `ListUnitTasks`, `ListSubTasks`, `ListSessions`, and `ListPullRequests` require `workspace_id` and return `items + next_page_token` envelopes.
21. New `List*` methods apply enum-based filters (`status`, `cli_type`) and deterministic page-size/page-token pagination.
22. `List*` methods return empty `items` with empty `next_page_token` for existing workspaces with no matching data.
23. Worker `NormalizeSessionOutputFixture` accepts fixture presets and raw JSONL, then returns normalized `SessionOutputEvent[]` with a derived `session_status`.
24. Main `RunSubTaskSessionAdapter` rejects missing input oneof and `unit_task_id`/`sub_task_id` ownership mismatches with typed Connect errors.
25. Main `RunSubTaskSessionAdapter` persists session output under `session_id` and returns the updated SubTask state.
26. Main stream emits session adapter events in ordered sequence (`SUBTASK_UPDATED` -> `SESSION_OUTPUT` -> `SESSION_STATE_CHANGED` -> final `SUBTASK_UPDATED` when status terminal).
27. Desktop startup always renders workspace picker at `/`, and desktop navigation exposes seven Codex-role pages after workspace selection.
28. Desktop route guard redirects `/projects`, `/threads`, `/review`, `/automations`, `/worktrees`, `/local-environments`, and `/settings` to `/` when no active workspace session exists.
29. Desktop `Threads` route provides inbox + detail timeline, and shared selection state drives Action Center context.
30. Desktop `Threads` flow supports selecting a task/subtask/session, then executing `SubmitPlanDecision` and `RunSubTaskSessionAdapter` from Action Center in one continuous workflow.
31. Desktop `Worktrees` route merges session list and event timeline, and stream updates incrementally refresh React Query caches.
32. Desktop `Projects` route renders workspace overview, repository groups, and active task summaries from read APIs.
33. Desktop `Review` route renders PR queue with review assist/comments and propagates selected PR context to Action Center.
34. Desktop `Automations`, `Local Environments`, and `Settings` routes provide real local read/write UX (create/update/delete/toggle, diagnostics history, last-selected restore).
35. Desktop Connect transport sets `Authorization: Bearer <token>` only when a resolved token exists.
36. Desktop workspace profile persistence excludes remote token values from local storage payloads.
37. Desktop UI exposes no `RPC Dashboard` surface or dependency in route containers.

