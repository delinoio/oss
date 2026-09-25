## Summary

DeliDev is a personal desktop Agent Runner for managing multiple projects, AI accounts, harnesses, and execution machines. It does not inherit the web platform, organization, or billing scope of the historical issue #722. The desktop app, server, and Worker all target macOS, Windows, and Linux.

## Components and Interfaces

| Component | Responsibility |
|---|---|
| DeliDev Desktop | Tauri UI for projects, accounts, agent configurations, machines, and sessions. |
| `delidev-cli` | Go business logic, distributed as the standalone and bundled-sidecar `delidev` binary. |
| `delidev server` | Single-user authority for state, credentials, routing, schedules, API credential proxy, usage, integrations, and Workers; explicit `start`, `stop`, and `status`. Multiple desktop clients may connect. |
| `delidev worker` | Execution process on a local/remote machine; supports `start`; server manages registration, listing, status, updates, and deregistration. |

**Product Surface Parity and CLI Automation**

- Every public product capability has a Connect RPC operation and corresponding CLI subcommand with shared validation/execution semantics. This includes projects/repositories and inspection; Agent Workers; AI account login/logout/connect/disconnect/refresh/status/quota and custom providers; machines/settings/capabilities; every session, workspace, snapshot, queue/Steer/interaction, Plan, Archive/restore/delete/fork/Sidechat, session-app and local-review operation; schedules/history/Run now; integrations/PATs/GitHub issue and PR queries/associations/remediation; search/activity/inbox/events; usage and storage/recovery. Server startup and native OS presentation are infrastructure boundaries.
- Every product command supports versioned JSON, stable result/entity/session IDs, typed errors and exit codes, and progress separate from structured stdout. Mutating requests use request IDs with durable deduplication so retries cannot repeat an accepted operation. Non-interactive calls return actionable missing-input/confirmation-required errors rather than hanging or authorizing silently. Accept secrets through stdin or hidden input, never argv, shell history, ordinary output, or logs.
- Ordinary CLI commands never start the server automatically: return typed server-unavailable errors with explicit startup guidance. Starting the server is a separate command.
- Go owns business logic. Rust is limited to the Tauri shell, window/tray/OS integration, and local sidecar startup/supervision. TypeScript connects directly to the selected local or remote server through Connect RPC, including server streaming; Rust must not implement product logic or proxy agent traffic. Local operation is the default.

**Listening, Authentication, and Streaming**

- Standalone server, sidecar, and API proxy bind to `127.0.0.1` by default; support explicit `::1`. Non-loopback/wildcard binding requires explicit startup configuration such as `--listen <IP:port>`. Remote-server selection, Worker registration, and connection failures never widen listeners. Remote app/server/Worker connections are authenticated and encrypted; local RPC also requires authentication and allowed-origin enforcement.
- Status and secret-free structured logs expose the actual listener and failures. UI/CLI inputs, queue/Steer, and interaction responses use separate RPC calls; agent output/state/interaction events use Connect server streaming. Do not add WebSocket, standalone SSE, or Tauri IPC for client-facing agent traffic. Native harness HTTP/SSE and provider inference/streaming protocols are internal boundaries, not replacements for Connect.
- Server-owned persisted state, queues, and inbox survive client disconnection. Publish a coherent state snapshot with its event cursor; reconnect replays strictly after that position, detects gaps, deduplicates events, and resnapshots on invalid/expired cursors without duplicating execution. Fetch/update transcripts by indexed message identity/revision, never scan the entire history for each event. Bound streaming buffers and apply backpressure with explicit reconnect/recovery behavior for slow consumers.
- TypeScript interprets RPC notifications and gives Rust only native presentation/activation data; Rust does not subscribe to or route agent streams.

**Sidecar, Services, and Desktop Updates**

- Bundle the platform `delidev` so local startup needs no separate CLI installation; standalone/sidecar contracts are identical (Tauri sidecars). Start or reuse a compatible server for the same user/data scope, check readiness, and prevent duplicate startup. Never replace an incompatible server or interrupt its sessions implicitly.
- Closing or fully exiting the app disconnects only the client: server, proxy, and sessions continue. Reopening restores state/events/inbox. Full exit ends native notification delivery and Rust supervision; pending requests remain server-owned.
- While the app runs, supervise its local server with capped exponential backoff and jitter after startup failure/abnormal exit; recheck for a compatible same-scope server before every restart. Explicit stop suppresses restart. Do not restart remote servers or replace incompatible ones. Show retry/failure state in UI and sanitized structured logs.
- Optional native OS services for server and Worker expose install/status/start/stop/remove, idempotent setup, one supervisor per process/user/data scope, and no restart after explicit stop. Removing registration does not erase data.
- Desktop update notices and user-confirmed installation require signature-verified artifacts; verification failure blocks installation. Updating desktop/bundled files cannot implicitly replace a live server, update harnesses, interrupt server-owned sessions, or bypass compatibility. Reconnect to compatible servers; otherwise give explicit recovery guidance.
- Only the DeliDev Worker binary is automatically updated, after active sessions finish; retain the previous working version on failure. Automatic server and harness updates are excluded.

## Concepts

| Concept | Definition |
|---|---|
| Project | One or more repositories; independently restricts Agent Workers and AI Accounts. |
| Repository | Git identity separate from per-machine checkout locations. |
| Agent Worker | Reusable Harness + Model + Reasoning effort/options, candidate accounts, and routing configuration; distinct from the execution machine. Snapshotted immediately before first execution. |
| AI Account | Subscription/API identity, separate from GitHub PATs. Unlimited connected accounts, including multiple per provider; many-to-many Agent Worker links. |
| Agent Session | Persistent conversation/execution using one Agent Worker and machine. Worktree/Local selects one project; General Chat is projectless. Supports Archive/restore and native independent forks. |
| Workspace Type | Typed Worktree, Local, or General Chat choice. |
| Schedule | Server-owned cron template producing independent project sessions. |
| Harness | All four initial targets required: Codex, Claude Code, OpenCode, Grok Build; extensible adapters. |
| DeliDev Worker | Local/remote execution computer and its Worker process. |

**Project Selection Restrictions**

- Configure Agent Worker and AI Account restrictions independently. When no restriction is configured for a category, its selector shows all available entries on that server.
- When a restriction is configured, show only allowed entries in the corresponding selector. Also validate compatibility among the harness, model, and account.
- Enforce the same restrictions in the CLI, RPC APIs, account routing, and session resumption. If no valid combination exists, explain why and block execution.
- Clearly distinguish Agent Worker configuration from DeliDev Worker execution-machine selection. Every creation path selects one Agent Worker rather than independently overriding its harness/model/effort/account. Account-less Agent Worker drafts may be saved, but execution is blocked until an eligible account exists; validate configured account compatibility on save and again before dispatch.

**Sessions and Workspaces**

- Every session has an explicit workspace type and execution-machine identity:

| Workspace Type | Execution Behavior | Sidebar Icon |
|---|---|---|
| Worktree | Requires a project. Create a separate worktree for each project repository on the selected local or remote DeliDev Worker. This is the default for project sessions. | Branch/worktree |
| Local | Requires a project. Explicitly use its existing checkouts on the user's own computer. This option does not mean an arbitrary existing checkout on a remote Worker. | Computer |
| General Chat | Has no project. Use a separate session-owned working directory on the selected execution machine, with file and terminal capabilities. | Chat bubble |

- Local sessions retain the actual originating execution-machine identity. Viewing them from another client does not relocate execution or reinterpret which computer is local.
- General Chat working directories are isolated from other sessions and retained for resumption. Archiving does not erase their files.
- Show the workspace type with a distinct sidebar icon, tooltip, and accessible name; do not rely on color alone.
- Local uses every repository's existing checkout/current branch/tree as-is: no starting-branch selection, fetch, or worktree creation. Sharing is explicit and same-machine only. Multiple repositories form one workspace; start the harness in the project's designated primary repository (its worktree for Worktree mode).
- Retain conversation history, execution state, pending approvals, and the Agent Worker, account, and machine used by the session.
- Distinguish a disconnected connection from completed execution. Do not automatically duplicate an execution on another machine when its current state cannot be confirmed.

**Repository Inspection, Branches, and Workspace Preparation**

- Worker-scoped read-only repository inspection through RPC/CLI resolves the canonical Git root, repository name, and remote names from roots, subdirectories, and linked worktrees. It must not fetch or return credential-bearing remote URLs. Revalidate the selected remote against that Worker when saving configuration.
- Each project has ordered repositories and one designated primary repository. Select base and starting references independently per repository: base is the PR merge/comparison target; starting ref determines prepared code. Both local and remote branch references are supported.
- Default the starting ref to the default branch of the configured preferred remote, otherwise `origin`, otherwise the sole remote. Ambiguous remotes or an unavailable remote default require explicit configuration; never guess.
- Server-owned automatic remote fetch is enabled by default with identical UI/RPC/CLI semantics. On the execution Worker, fetch the selected remote branch and resolve its updated commit before creation (e.g. fetch `main` from `origin` for `origin/main`). Fetch/auth/network failure blocks preparation without stale fallback. When disabled, use the stored remote-tracking ref or fail if missing; local refs resolve locally.
- Prepare every repository successfully before starting the harness. Bound and cancel Git/filesystem work outside database transactions; commit the ready workspace/state atomically afterward. On failure, roll back only this attempt's new worktrees/files, preserve existing checkouts, and retain retryable cleanup state if removal cannot finish.
- New worktrees use detached HEAD at the exact resolved commit, without creating/checking out a working/tracking branch. Persist base/starting refs and actual commits. Ordinary preparation never pulls, merges, rebases, resets, or overwrites an existing checkout.
- Fresh starts reduce stale-base conflicts but cannot prevent later conflicts. Explicit PR remediation below separately permits conflict-resolution sessions and merge/rebase. Native forks preserve their recorded source commit/uncommitted state; automatic fetch must not move the fork point.

**First-Execution Configuration Snapshot**

- Immediately before first dispatch, atomically persist effective harness/model/effort/options, candidate account references, and resolved routing policy/weights. Session creation alone does not freeze configuration; edits before execution apply, later edits never rewrite subsequent turns/retries/Resume.
- Waiting schedules resolve at actual first execution. Ordinary forks inherit the execution snapshot and select accounts independently only when native fork/resume compatibility permits; Sidechat retains the fork-point account/snapshot. Both enforce current eligibility.
- Distinguish initial/current account and explicit change history from the immutable configuration; permitted changes after confirmed stop preserve past usage attribution.
- Store only non-secret settings/references, never keys/PATs/login/proxy secrets. Snapshots do not bypass current project/account restrictions, revocation, refresh, or compatibility.

**Native Agent Options and Instruction Templates**

- Expose per-Agent Worker native subagent model, reasoning effort, and maximum concurrency where the installed harness/version supports them. Subagents retain the parent session's selected account; a different child model must be usable through that same account. Do not route child accounts independently.
- Expose native permission/approval settings, including file read/write, command, and network controls where available, and an optional native approval-review model where supported. Preserve account and permission boundaries; these controls neither create a common DeliDev sandbox nor emulate or bypass native approval.
- Expose supported Fast/Priority or other native service-tier choices separately from reasoning effort. Default to the harness/provider's own setting unless explicitly selected. Show requested and observed effective values when available; unsupported or unobservable values must be explicit.
- Provide reusable additive instruction templates with ordered composition per Agent Worker. Preserve the harness's base instructions. The first-execution snapshot includes the exact applied template contents/order and all supported subagent, permission/approval, approval-review model, and service-tier settings. Later template or configuration edits do not rewrite existing snapshots, including on resume or native fork inheritance.
- Show DeliDev-owned applied instructions and snapshots read-only, never internal harness prompts. All these options require native capability/version checks and explicit unsupported states, without emulation.

## Schedules and Creation Sources

- The server persists schedules and history and executes without the desktop. A schedule has a name, enabled state, prompt, project, Agent Worker, execution Worker, Worktree/Local choice, applicable per-repository branch choices, cron expression, and IANA timezone. General Chat schedules are excluded; Local stays bound to the configured user's computer.
- RPC/CLI expose create/list/inspect/edit/delete/pause/resume, next run, occurrence history, and Run now. Run now applies the same configured overlap policy and records manual initiation. History records sessions, waiting/queued occurrences, failures, and skip reasons.
- Each accepted run creates an independent session with durable occurrence identity, preventing duplicates after retries/restarts. Cron creates sessions internally, independently of external one-shot CLI creation. CLI creation returns the session ID, accepts prompt/project-or-General-Chat/workspace/Agent Worker, requires a running server, and records `EXTERNAL_CLI` provenance rather than invoking scheduling.
- Default overlap policy is **Overlap**; Worktree runs get independent workspaces and Local runs retain explicit checkout sharing. **Skip** records a skipped occurrence while a previous run is active. **Wait** persists accepted occurrences and dispatches in FIFO order after preceding runs end. Approval/input waiting counts as active; disconnection is not completion.
- If the server or selected Worker is offline when due, record a skipped occurrence without catch-up backlog. Previously accepted Wait occurrences survive restart.
- Schedule edits/pause/deletion affect future scheduling, not already-created sessions. Deleting a referenced Project or Agent Worker configuration atomically disables affected schedules with an actionable reason; reconfiguration is required before re-enablement. Retained session snapshots/history are not rewritten.
- Manual, external CLI, scheduled, and remediation sessions participate in ordinary transcript, notifications, search, recovery, activity, and usage. Preserve source-specific links without making session lifetime depend on source objects. Do not add session dependencies or reserve a future dependency contract.

## Session Controls

**Native Harness Protocols and Plan**

- Codex, Claude Code, OpenCode, and Grok Build require installed-version capability validation. Normalize native messages/progress/plans/tools/questions/approvals/usage/subagents/terminal states through typed enum capabilities and provider extensions; do not scrape rendered TTY output or force unsupported features into a synthetic common behavior.
- Use Codex app-server and its expected-turn native Steer. Use Claude Code's machine-readable streaming interface with structured interactions and resume identity. OpenCode runs as a DeliDev-owned, authenticated loopback instance on the Worker, with health/version/operation readiness checks and credentials hidden from renderers/logs; never attach to an arbitrary user TUI server.
- Isolate harness instances/configuration by account/workspace without overwriting unrelated logins/sessions. Persist provider/session mapping and workspace/resume context. For OpenCode, map native create/read/resume, asynchronous prompt, abort, fork, and typed question/permission endpoints; reconcile message/part/status/usage revisions and pending requests after SSE reconnect. Asynchronous acceptance alone is not native mid-turn Steer.
- Apply the exact snapshotted model, effort, account, and supported options; native defaults or agent overrides must not silently replace them. Unknown/incompatible protocols fail explicitly. Keep native endpoints internal to Worker adapters; public contracts must not couple future server roles to a harness.
- Native Plan supports mode selection/current state, plan artifacts with provider/session/turn provenance, questions, revisions, and native transitions where available. Use native Plan/execution agents and permissions; never simulate with prompts. Follow the harness's own transition/approval rules without an additional common DeliDev execution-approval gate. A Plan label is not proof of read-only enforcement.
- Native Plan, Steer, fork, and interaction capabilities are consistent across UI/RPC/CLI: unsupported actions are visibly unavailable and return typed errors without emulation. Mode changes cannot change the account/snapshot/permission policy or bypass genuine native interactions.

**Native Context Controls**

- Display harness-reported context usage and compaction state, distinguishing unavailable information from measured values. Offer manual compaction only through a supported native harness action, using the session's selected account. Do not add a separate summarization model, cross-account compaction routing, or transcript-based replacement for native compaction.

**Queue, Steer, Interactions, and Recovery**

- While running, follow-ups default to a durable server-ordered queue; idle eligible unpaused sessions dispatch the oldest entry. Persist identity, acceptance order, content revision, intended mode at enqueue time, and delivery state. Later mode changes do not reinterpret queued entries. Only undelivered messages can be edited/deleted with expected revisions.
- Steer is explicit and uses verified native support against the expected active turn. Atomically claim a selected queued message; confirmed native acceptance removes ordinary-dispatch eligibility. Stale-turn/unsupported rejection leaves it queued. Never substitute cancel/resume or silently turn Steer into queueing.
- Serialize dispatch, queue edits/removal, Steer, Stop, Archive, and interaction responses per session. Queues cannot answer/bypass questions or approvals. Paused, archived, unavailable, or recovery-pending sessions cannot auto-dispatch.
- Persist interaction ID, kind, provider request, current authorization/state, read state, and response/delivery state. Validate every response against that original request; user-answer fields cannot overwrite a tool denial or Sidechat read-only policy. Reject malformed, stale, wrong-kind, duplicate, and conflicting concurrent responses.
- After uncertain prompt/Steer/interaction-response delivery, automatically inspect native provider state before retrying. Persist any confirmed acceptance/response and reconcile all clients. If acceptance cannot be established, retain recoverable uncertainty and pause subsequent sends; never blindly replay or promise exactly-once delivery from an incapable native API.
- After actual harness failure, evict dead provider instances, reconcile pending state, and require explicit Resume. After Worker restart, prefer reattachment to a surviving process only after verifying ownership, start identity, session mapping, and safe native reconnection. If uncertain, block replacement execution and expose recovery.
- Execution outcome, archive state, and recovery state are independent. Late idle/completion events cannot turn a failed execution into success. Reconnect restores readable current state and incremental events without resending accepted work.

**Stop, Archive, and Restoration**

- Stop ends the agent and pauses its dispatch until explicit Resume; it leaves session terminals/processes/port forwards running. Archive stops the agent and all session-owned terminals, processes, and forwards, without affecting unrelated sessions or account credentials.
- Before Archive, durably pause dispatch and record archive-in-progress, preventing concurrent queue/remediation starts. Confirm owned work has stopped before success. Stop/cleanup failure or uncertainty remains visible and retryable, with recovery operations reachable after restart; dispatch remains paused while reconciling.
- Archive hides sessions from ordinary lists but retains transcript, queue, workspace, interactions, local reviews, PR links, and access to the shared account browser profile. Archiving a completed or failed session preserves its execution result; Archive never deletes files or starts replacement execution.
- Restore/Unarchive only removes archive visibility state and restores project/ungrouped membership. Keep execution paused until explicit Resume and native eligibility/resumption checks; do not dispatch queued input merely on restoration. Reconcile obsolete questions/approvals on Stop, Archive, Resume, and notification activation.

**Native Session Forking**

- Use installed-version native fork only, never transcript replay. Create a new independent session ID retaining source/fork-point link, completed conversation context, project or General Chat association, and Agent Worker execution snapshot. Apply project/account/harness/model eligibility and compatible ordinary-fork routing; Sidechat has the account-preserving rule below.
- Never modify the source or copy active execution, pending queued input, or unresolved approvals into executable child work.
- Default project forks to separate detached-HEAD worktrees for every repository, preserving source commits and uncommitted state. Explicit Local sharing is same-machine only. General Chat copies session-owned files to a separate projectless directory without requiring Git.
- Make conversation/workspace fork points explicit and consistent; reject partial/inconsistent forks and keep source state intact. Automatic fetch cannot move the recorded source snapshot.

## Session-Side Apps and Agent Review

- Provide a right-side app area next to the main AI session, with the following apps. Opening, closing, and switching them must preserve the main session, its execution, and unsent input.

| App | Required Behavior |
|---|---|
| Embedded browser | Browse and inspect web content alongside the session. |
| Diff review | Inspect the session's file changes, add file/line review comments, and send grouped change requests to the session's agent. |
| File explorer | Browse the session's actual workspace on its execution machine. |
| Terminal | Interact with a terminal in that same workspace and execution machine, including a remote Worktree or General Chat workspace. |
| Sidechat | Hold a separate, read-only supporting conversation using context from the main session. |
| Subagent status | Show the real subagent hierarchy and status when exposed by the selected harness and installed version. |

- Diff review is an agent feedback workflow: review changes, write file/line comments, submit a **Request changes** to the current session's agent, and inspect the updated diff after the agent responds. Bind feedback to the reviewed repository, file/line, and diff revision so stale comments are recognizable.
- Sending a review request uses the existing session-input rules: an idle session can receive a new turn; while it is running, queue by default and use steering only when explicitly selected and supported. Avoid duplicate submissions on reconnect.
- Diff review does not publish GitHub PR comments, reviews, approvals, or change requests. GitHub PR inspection and session-local agent review are distinct product capabilities.
- Sidechat is a native fork at a completed parent conversation boundary, never partial streamed output or a live approval. Retain the parent's fork-point account and immutable execution snapshot without rerouting or following later parent changes. Record parent/boundary links; child transcript, queue, and execution state are independent, with no copied executable work, pending approvals, or queued input.
- Sidechat references the parent workspace without taking deletion ownership. Enforce read-only files/tools and no external writes through verified native execution permissions, including remote tools/hooks/plugins, not a prompt or an automatically approved Plan write. Reject creation if native fork/read-only enforcement or current account eligibility cannot be verified.
- The user may explicitly send selected findings to the main agent through ordinary queue/Steer rules; there is no automatic transcript merge. Parent deletion or workspace cleanup also deletes dependent Sidechats after confirmed stop and cleanup; keep access valid until then or report it unavailable.
- Subagent status uses real harness hierarchy/state/recent-output/usage, with explicit unsupported states, and cannot create/message/interrupt/restart subagents.
- Local diff uses Git comparisons only, without a session-start filesystem baseline; Worktree comparisons retain the resolved creation commit. Local comments support create/read/edit/delete and selected grouped submission, retaining repository/path/diff revision/old-or-new side/line range/context. Ambiguous anchors become stale; binary/non-line changes get no invented line locations. Atomically persist submission links and dispatch ownership; submitting neither auto-resolves comments nor publishes to GitHub.
- Session/workspace terminal input, resize, output, and lifecycle operate on the execution Worker. RPC output is a byte stream: preserve ordering and partial UTF-8 boundaries through incremental decoding and explicit truncation/reconnect handling.

**Browser Profiles and Development-Server Forwarding**

- Each computer maintains one protected browser profile per server/AI Account, shared across that account's sessions on that computer. Retain tabs, cookies, storage, history, and browser credentials locally; never synchronize them across computers or include them in Git/workspace snapshots.
- Session deletion, Archive, or closing a tool does not delete a shared profile. AI Account deletion requires durable per-client cleanup, including offline clients on reconnect; stop/close profile users and prevent late browser-process flushes from resurrecting deleted data. Do not claim distributed cleanup complete before required acknowledgements.
- Treat external web content as untrusted: isolate it from app origins, native privileges, protected app credential storage, and authenticated local RPC. Enforce browser-profile/account isolation and explicit allowed-origin authorization; browsing cannot acquire product capabilities.
- RPC/CLI support authenticated forwarding of an explicitly selected Worker and development-server port, bound to session ownership with observable start/status/stop and Archive cleanup. Use existing authenticated outbound Worker connectivity; do not implicitly expose arbitrary ports or widen listeners. This is separate from server-relative model API endpoints: no Worker-localhost model-provider tunnel is added.

## Desktop Settings, Inbox, and Notifications

**Settings Modal**

- Provide desktop settings in a dedicated modal inside the main app window, separate from the session surface.
- Opening or closing settings must preserve the current session and any unsent input. Settings operations use the same server-owned RPC and CLI functionality as other public product capabilities.

**Integrations: GitHub and Personal Access Tokens**

- **Settings > Integrations > GitHub** opens connection/PAT configuration in the settings modal. Initial provider: GitHub.com; extensible provider identifiers, shared PR/repository models, adapters, and explicit capabilities must permit future services without redesigning sessions/settings.
- Support multiple named PAT profiles with add/replace/validate/delete and authenticated identity/connection status. Each repository explicitly selects its profile; never fall back to another token/system credential. Deletion removes the secret, stops its use, and leaves affected associations requiring reconfiguration; replacement/revalidation refresh identity/access status.
- Prefer fine-grained PATs, also support classic. Open official creation forms with required scopes/permissions preselected; verify actual form behavior. Fine-grained profiles target selected repositories and appropriate resource owner, with separate owners using separate profiles. Request Metadata/Pull requests/Checks/Commit statuses and feature-specific read access for issues, rulesets, and reviewer verification.
- Request no unnecessary classic scopes for public repositories; explain that private-repository `repo` is broader than the supported read-only API feature. Validate each selected repository/feature, not token validity alone. Distinguish invalid/expired/revoked tokens, insufficient permissions, organization approval/SSO, rate limits, and transient/network failures when known.
- Store PATs in the server's protected OS credential store, separate from AI Accounts. Never return saved tokens or include them in SQLite, renderer/synchronized config, RPC reads, history, snapshots, logs, or Worker/harness credentials.

**GitHub Queries, PR Links, and Remediation**

- With an explicitly selected repository/PAT profile, provide PR list/search/details/diffs/checks/status/open-on-GitHub and issue list/search/details through RPC/CLI. Validate each repository/feature capability; unavailable/unauthorized checks are unknown, never passing. Request the read permissions needed for issues, rulesets, and reviewer validation in addition to PR/check metadata; report inaccessible features explicitly.
- A session can retain zero or many stable PR associations across its project's repositories, including after Archive or problem resolution. The server persists problem evidence, handling/dismissal state, and session links.
- GitHub API integration remains read-only: no PR create/edit, comment/review publication, approval/request-changes, or PR merge. Local diff-review feedback goes to the agent. The separately configured remediation flow authorizes native Git commit/push on the selected Worker; it never receives the server's read-only GitHub PAT.
- Support manual Fix now/Dismiss and three independently enabled automatic kinds: CI failure, published review/PR feedback, and merge conflict. **All default off.** Use server defaults with per-repository overrides for switches, reviewer selectors, session strategy, new-session Agent Worker/machine, conflict strategy, and consecutive-attempt limit.
- Review automation requires at least one matching selector, OR-combining exact GitHub user (including machine user), exact bot/App, and minimum effective repository collaborator permission (READ/TRIAGE/WRITE/MAINTAIN/ADMIN). Persist stable actor/App IDs; names or a `[bot]` suffix are not proof. Revalidate identity and required current permission immediately before execution. Unknown/nonmatching authors remain available for manual action; missing access/rate limits/network failures cannot authorize automation.
- Include **all published feedback**, including APPROVED-review comments, submitted review bodies/code-review threads, and ordinary PR conversation comments. Exclude unsubmitted drafts/PENDING reviews. Do not semantically classify whether feedback requires changes; the agent decides.
- Later approval or GitHub dismissal does not erase unhandled feedback. Exclude only evidence explicitly handled, resolved, or locally dismissed for its recorded content version. Deduplicate API representations by stable review/comment/thread identity and content revision; edited content becomes new eligible evidence. Keep author/state/content/source URL and handling provenance. Local dismissal is distinct from GitHub review dismissal.
- CI triggers only on terminal non-passing results of checks required by applicable **active rulesets** for the current base ref, aggregating requirements. Match context/check name, required App/integration, and GitHub's evaluated commit; retain ruleset/result provenance. Success/skipped/neutral/pending/missing do not trigger; missing/unknown is not success. Optional failures and checks required only by classic branch protection are status-only, with no fallback.
- Verify prerequisites **per remediation kind**: unknown CI/ruleset evaluation blocks CI automation but does not block independently verified review/conflict handling. Unknown mergeability must not be treated as a verified conflict. Re-evaluate on new PR head, base/ruleset/reviewer-policy changes, resolution, dismissal, new feedback, or a new conflict transition; dismissal is evidence/version-specific.
- Session strategy is configurable: reuse a suitable existing session first (default), or always create a dedicated one. Reuse the most recently active eligible linked session with a matching workspace; never automatically resume archived or explicitly paused sessions, including unarchived sessions awaiting Resume. Otherwise create a fresh PR-head worktree remediation session, including for conflicts.
- At most one active remediation session per PR; coalesce follow-up problems through normal queue rules. New sessions require explicitly configured Agent Worker and execution machine with valid project/account/harness eligibility; invalid/missing configuration blocks creation rather than guessing.
- The harness directly modifies files, validates/tests, commits, and pushes the PR branch using Git authentication the user already prepared on that selected Worker. DeliDev does not substitute a Go-controlled publisher, inject its lookup PAT, or fall back to another credential. Missing Git access yields actionable failure. Preserve unrelated checkout work and verify the intended repository/branch/head before writes.
- Conflict resolution is configurable per server/repository: **merge by default**, rebase optionally. Rebase may push only with `force-with-lease` against the expected remote head; never unrestricted force. A changed remote head fails safely for reconciliation.
- Bound consecutive automatic fix/push attempts per remediation chain, **default 3**, configurable by server/repository policy. Persist chain identity/count across restarts, session reuse/replacement, and new resulting failures so retries cannot reset the limit. Coalesce/deduplicate evidence without concurrent duplicate fixes. At the limit, record the cause and wait for explicit resumption/policy change; Archive/explicit pause is never overridden.

**Inbox and Native Desktop Notifications**

- Persist harness questions, approval requests, and requests for additional user input on the server, and show them in the inbox with their associated session and current response state.
- Reading a request and resolving it are separate states. UI/RPC/CLI expose list, inspect, mark-read/unread, and respond; reading never authorizes execution. Retain completion/failure records alongside interactions, with configured native notifications.
- While the desktop app process is running, also display interaction requests as platform-native desktop notifications. Activating a notification opens the corresponding request in the app.
- Notification permission denial or delivery failure must not remove or resolve the inbox request. Requests remain accessible even when desktop notifications cannot be shown.
- Reconnecting must not deliver duplicate notifications for the same request. Responses from multiple clients must not apply the same interaction more than once, and every client must reflect the server's current request state.
- Fully exiting the desktop app stops desktop notification delivery; the server continues retaining pending requests for the next connection. Archiving and resumption must update request validity in the inbox and when opening a notification.

**Setup, Tray, and macOS Widget**

- First launch shows a prerequisite checklist with settings/guidance links, without a step-by-step wizard, automatic harness installation, or overwriting existing setup.
- The tray/menu-bar panel shows server/Worker connectivity, active-session/pending-interaction counts, today's usage, per-account quotas, and links to relevant app views.
- Provide an initial macOS widget with privacy-safe status/usage/quota summaries. Use account aliases and exclude secrets, raw email addresses, prompts, and conversation content. Show the last successful refresh and stale state; background refresh follows OS best-effort scheduling and must not promise continuous updates. Other OS widgets are outside this initial scope.

**Portable Configuration**

- Export/import non-secret configuration and instruction templates through the shared product surfaces. Validate the input and show a change preview before applying it. Credentials/devices require fresh authentication and machine references require explicit remapping; never silently bind another account or computer.
- Exclude authentication secrets and full session/history backups. Preserve existing settings when validation or application fails, and report conflicts without silently discarding content.

## Account Routing, Credentials, and Worker Operations

**Account Routing**

Support six typed policies, with a server default and explicit per-Agent Worker inheritance/override. Initial server default: **Sequential exhaustion**.

| Policy | Selection Behavior |
|---|---|
| Fixed | One explicitly selected eligible account. |
| Priority | First eligible account in configured order. |
| Round-robin | Persisted rotation across eligible candidates with relative weights, equal by default. |
| Remaining quota | Greatest comparable remaining fraction; score each account by the minimum fraction across its applicable blocking windows, never sum windows/providers. |
| Reset-window | Nearest fresh, trustworthy, comparable quota reset. |
| Sequential exhaustion | Continue assigning new sessions to the current eligible ordered account until confirmed quota exhaustion; then advance the persisted per-Agent Worker cursor. Earlier recovery does not move it backward before traversal completes. |

- Candidates intersect current project restrictions, snapshotted ordered account links, harness/model compatibility, and enabled/authenticated readiness. General Chat omits only project restrictions. Confirmed exhausted accounts are ineligible; unknown is not measured zero. Account-less drafts remain saveable but cannot execute.
- Resolve the effective policy/weights with the first-execution snapshot and atomically commit selected account, session dispatch state, sequential cursor, rotation, and tie-break state. Concurrent dispatch/restart must not lose updates. Quota-score ties rotate persistently per Agent Worker.
- Refresh supported quota observations every five minutes and on request. Retain per-window observation/reset timestamps and unknown/stale/failed/unsupported distinctions. Expired data or a passed reset timestamp is not evidence of recovery. Compare only meaningful fresh evidence; Remaining quota/Reset-window fall back to configured priority when none is comparable.
- Keep the selected account throughout active execution, including approval/input waiting. Authentication/quota errors never trigger automatic account/model failover. Explicit changes require confirmed stop/completion, current eligibility, and native resume compatibility; otherwise require a new session. Failed/uncertain stop blocks changes. Preserve original/current accounts and historical attribution; other sessions are unaffected.
- One account may serve multiple Agent Workers without merging their configuration. An account-level exclusion removes it from all five automatic new-session policies; explicit Fixed and existing sessions remain subject to ordinary eligibility.
- Provide read-only preview and actual selection records with candidate eligibility/exclusion reasons, effective policy, and quota evidence. Preview makes no reservations, authentication/model calls, or routing/tie-break/cursor mutations; dispatch revalidates and records the actual result.

**Credentials, Account Health, and Official Authentication**

- The server exclusively owns AI login/logout/refresh and custom-provider connect/configure/validate/disconnect. UI/CLI initiate shared RPCs; native browser/OS presentation does not transfer ownership. Isolate each subscription account's state and refresh, never substitute an unrelated system login.
- Authoritative secrets remain in protected server storage. Subscription harnesses temporarily receive only necessary execution credentials in isolated account runtimes. API upstream keys never reach Worker/harness; use the proxy below. Keep secrets out of ordinary config/RPC output, logs, and history; authenticate/encrypt remote connections.
- API presets include Vercel AI Gateway and OpenRouter with endpoint defaults, key-acquisition guidance, model selection, and validation. Custom OpenAI Chat Completions, OpenAI Responses, and Anthropic Messages endpoints configure authentication/models according to actual harness/protocol compatibility. They are execution connections, not provider-wide reporting integrations, and require no extra usage/billing/admin key.
- Show account quota windows, remaining amounts, reset/observation times, authentication health, individual and refresh-all actions. Distinguish measured zero, unknown, unsupported, stale, and failed; timestamp older evidence. No pooled quota/capacity percentages.
- Use aliases and masked email by default in lists/tray/widgets; explicit reveal is account-detail-only. Diagnostics use non-secret references without raw emails. Optional per-account quota-recovery notifications default off, persist in inbox, and follow native notification/deduplication rules while the app runs; no webhook.
- Official provider-supported browser/device-code flows support headless/remote servers through desktop/CLI, with shared progress/cancel/expiry and explicit unsupported states. Credential import is explicit and user-selected through official paths only, preserving the source login and isolated accounts on success/failure; reject unisolatable paths, never scan silently.
- Where official interfaces support reset credits/coupons, expose status and require explicit confirmation for each consumption. Reconcile uncertainty or retry only with the same provider-supported operation identity; absent safe reconciliation, retain unresolved state and never redeem blindly twice.
- Never send inference merely to warm idle accounts or activate quota windows. Credential refresh, quota observation, and non-inference connection validation remain supported.

**Provider and Model Catalog**

- Extend the existing Vercel AI Gateway and OpenRouter presets with OpenAI, Anthropic, xAI, and DeepSeek, plus Ollama, LM Studio, and vLLM. Support keyless local endpoints and custom provider configuration using directly compatible harness/API protocols. Show defaults, authentication, validation, and model selection with explicit compatibility limits.
- Model API endpoints resolve from the DeliDev server: localhost means the server machine. Show this origin; no Worker-localhost model-provider tunnel is added. Explicit development-server forwarding is separate.
- Support automatic provider model discovery, manual model registration, and disabling discovery per provider. Discovery failures remain visible and do not remove manually registered models. Newly discovered models appear automatically with a NEW marker, without changing existing session configurations.
- Support model search, provider groups, hide/show, and custom order as display preferences separate from execution permissions. Provide custom display names and CLI aliases; resolve aliases to unambiguous canonical provider/model IDs before dispatch and retain those IDs in snapshots, diagnostics, and usage history. Reject alias collisions and ambiguity.
- Show context limits, input/output modalities, tool support, and reasoning capabilities as advisory metadata, distinguishing known, user-declared, and unknown information. This metadata adds no execution gate and does not weaken the existing harness/model/account compatibility validation.

**API Credential Proxy**

- The server owns `Harness -> DeliDev API proxy -> configured API provider`, preserving compatible request/response/streaming protocols and the separate desktop/CLI Connect boundary.
- Upstream keys exist only in protected server storage/proxy execution. Exclude them from Worker/harness environment, argv, config, workspace, history, ordinary RPC output, logs, and errors.
- Harnesses receive only connection information and revocable credentials scoped to the current execution, selected account, allowed model/API operations. The server resolves the authorized upstream and injects authentication. Callers cannot select other accounts/destinations or leak keys through supplied headers, redirects, errors, or diagnostics.
- Keep the active account fixed; end/stop/account revocation invalidates access, and resume authorizes a fresh execution credential. Proxy outage or incompatible protocol fails explicitly without upstream-key delivery or account fallback.
- Always set outbound `HTTP-Referer: https://deli.dev`; harness headers cannot override it. Apply the same loopback default, explicit exposure, and authenticated/encrypted remote policy. Closing the UI does not stop the proxy for server-owned sessions.
- Do not add protocol translation, account/model failover, external IDE/app proxy access or access keys, or proxy-owned retries (including HTTP 429). Users may configure external conversion gateways. Relay errors and applicable Retry-After safely; native harness retry remains native.
- This protects keys from the Worker/agent runtime, not unrestricted same-user access to server files/processes, and adds no OS sandbox. Usage still comes solely from harness JSON.

**Remote Workers and Executables**

- SSH setup inspects the target, installs/registers/starts/checks only DeliDev Worker, manages SSH credentials, and verifies host identity. Repeated setup must preserve registrations/workspaces. Worker updates wait for active sessions and retain the previous working version on failure.
- Users install/update all four harnesses; DeliDev never bundles/downloads/auto-installs them. Per Worker/harness, an explicit executable path takes precedence; search that Worker's `PATH` only when unset. An invalid explicit path must not silently select another binary.
- Worker-owned discovery/launch validates file existence, executability, version, and required protocol before execution; expose resolved path/version/readiness with typed missing/permission/incompatibility errors and installation/path/upgrade guidance. Do not leave partially started execution after validation failure. Rust/renderer do not discover or launch harnesses.
- Query and refresh models through real supported harness/provider interfaces and also accept explicit manual model IDs. Unsupported discovery is visible; it neither invents model availability nor removes manual configuration.
- Structured sanitized logs trace registration/connections/routing/lifecycle/update outcomes. Track owned child processes by start identity plus session ownership, never PID alone. Release exited PTYs, handles, buffers, and provider instances; cleanup is idempotent on every supported OS.

**Pairing and Outbound Connectivity**

- Pair desktop clients and manually installed Workers with short-lived, single-use codes; expose per-device status/revocation. Reject expired/reused codes. Revocation terminates that device's connection and rejects its old authorization. Preserve the single-user server model and SSH-based automatic Worker setup.
- Workers initiate authenticated, encrypted outbound connections to the server and reconnect without duplicating executions. Normal operation must not require an inbound Worker port. SSH remains an installation/setup path; harnesses and workspaces remain on the selected execution Worker.
- Support explicit HTTP(S) and SOCKS5 outbound proxy settings and bypass rules separately for the server and each Worker. Store proxy credentials as secrets. Show effective network settings and native harness support/application status; never claim unsupported traffic is proxied. These settings never implicitly widen listeners.

DeliDev retains Worker-local execution.

## DeliDev Usage Dashboard

- Dashboard scope is DeliDev activity only: manual, external CLI, scheduled, projectless General Chat, and observable Sidechat/subagents. Show total tokens, per-model/API usage, and verifiable actual API cost; exclude unrelated provider-account activity. Default to 30 days with time/model/account/session/project filters and identify General Chat separately.
- Sole pipeline: `Harness structured JSON -> Worker adapter -> server durable aggregation -> dashboard`. Use Codex app-server and the corresponding native structured OpenCode/Claude interfaces, extending adapters as needed; apply the same capability requirement to Grok Build. Report unsupported/incomplete telemetry, never invent values or use proxy/preset/account-wide pollers or additional reporting credentials.
- Retain provider/model, token counters, execution/session, and actual event-time account identity; later changes never relabel history. Normalize input/output/cache/reasoning/total according to native meanings, including subset versus separate counters.
- Deduplicate incremental/cumulative events/results across retries, reconnects, server/Worker restarts, resume, counter resets, and forks without charging inherited history. If parent totals include children, retain observable child detail without counting twice. Missing/partial usage is incomplete, not zero.
- Actual API cost is verifiable cost attributable to DeliDev API execution, not subscriptions, account-wide billing, or token-price estimates. Unknown/estimated actual cost is unavailable, not zero. Show known subtotals/incomplete totals and separate currencies; no cross-currency sum without an explicit conversion feature.

**Estimated Costs and Session Budgets**

- Show separate labeled token-price estimates with pricing source, as-of date, currency, covered usage, exclusions, and missing prices/usage. Retain historical pricing basis and attribution; use only the same deduplicated harness JSON. Estimates never become actual spend, and unknown is never zero.
- Optional per-session estimated-cost budgets alert and gate new turns, Resume, and queued turns consistently across UI/RPC/CLI. At known subtotal >= threshold, block new execution without discarding queued/pending input. Do not cancel in-flight work or claim a hard billing ceiling.
- With incomplete evidence below the threshold, warn and allow, without claiming verified compliance. Keep currencies separate and incompleteness explicit. Archive restoration still cannot start execution. Token-count budgets are excluded.

## Diagnostics and Storage Operations

**Request Diagnostics and Doctor**

- Expose observable per-model-request correlation IDs, canonical provider/model and non-secret account references, latency, requested/effective reasoning effort/service tier, and typed errors. Mark missing observations. Exclude request/response bodies, instructions, secrets, and raw email addresses from diagnostic logs; this metadata is not an alternate usage ingestion source.
- Provide read-only doctor in settings/RPC/CLI for installed/running server/Worker versions, connectivity, harness installations/capabilities, credential-store health, and resource/storage state. Return concrete recovery guidance without automatic repair or model inference probes. No dedicated external metrics exporter is required.

**SQLite, Backups, and Maintenance**

- Each server exclusively owns its local SQLite database in its data directory, including migrations/transactions. Renderers, Rust, and Workers do not access it directly. Persist projects/repositories, canonical transcripts, Agent Worker/account references, routing cursors/ties, snapshots' metadata, settings, schedules/history, queues/delivery, execution/archive/recovery, interactions/inbox, local reviews/submissions, PR evidence/handling, search/activity, and deletion progress.
- Public repository-owned entity IDs are canonical lowercase UUID v7; use typed enums for object kinds, capabilities, modes, delivery/lifecycle/source/account/routing/problem/selector states. Reject cross-kind ID collisions and stale entity revisions. Commit state and its events atomically with rollback; routing/session and review/queue ownership changes cannot leave partial persisted results.
- Keep workspace/snapshot files on their execution Worker, with server metadata/references only. DeliDev-managed credentials stay in protected secure storage, outside SQLite/transcripts/workspace snapshots; GitHub PATs use the server OS secure credential store. Browser profiles are separate protected device data.
- Support consistent manual database backup/restore, including committed WAL data, and mandatory automatic backup before destructive migration. Version migrations; on failure, corruption, or newer unsupported schema, stop with recovery guidance and preserve original data/backups instead of silently recreating/resetting the DB. Restore validates integrity/version and respects durable deletion obligations.
- Isolate scheduled preparation, GitHub, quotas, cleanup, and other maintenance as bounded cancellable jobs. A blocked Git command or failed job must not stall unrelated jobs, other schedules, or the server.

**Permanent Session Deletion**

- Session deletion is immediate and permanent: no trash, recovery grace period, or retention expiry. First persist irrevocable deletion intent and pause dispatch; confirm owned execution/resources are stopped, then remove transcript/interactions/reviews, session files/worktrees/attachments/snapshots, search and session-linked activity/usage data, and every DeliDev-managed DB backup containing that session. Remove dependent Sidechats too. Never delete/overwrite an original Local checkout or a shared AI Account browser profile.
- Track durable per-Worker/resource deletion progress; offline/unavailable Workers or cleanup failures remain pending and retry on reconnect. Do not report completion or reclaimed bytes until actual cleanup is confirmed. Retain only the minimum non-content deletion tombstones needed for retries and to prevent stale restore/reconnect from resurrecting data. Managed backup removal must participate in completion; do not claim to erase uncontrolled external copies.

**Workspace Snapshots and Manual Storage Management**

- Show server/Worker capacity/usage and preview scope/size for manual cleanup of DeliDev-owned inactive/archived workspace, attachment, and cache data. Protect active work and original Local checkouts. Archive remains stop-and-preserve, not deletion; no automatic cleanup or retention-based removal.
- Workspace cleanup retains the session and a restorable Worker-local snapshot. Support inspect/restore/delete of snapshots with conflicts/unavailable machines reported truthfully. A single manifest covers all repositories or the General Chat directory; snapshots never push to a remote.
- Preserve base/starting commits, unpushed commits, tracked/untracked/**Git-ignored** regular files, working changes, and symbolic links. User files may contain secrets; protect snapshots as private sensitive data; do not dereference links outside ownership or silently omit unsupported special files—block cleanup when faithful recovery cannot be guaranteed.
- Acquire and verify recoverable copies of **every** repository before deleting any source. Bound/cancel snapshot operations; failure, disk exhaustion, or interruption preserves source data and exposes recovery. Validate restoration, avoid overwriting existing paths/Local checkouts, and do not claim a partial multi-repository restore succeeded.
- Parent workspace cleanup also permanently deletes dependent Sidechats after confirmed stop/cleanup. Retain pending cleanup state and references until safe. Report actual reclaimed space, accounting for retained snapshots. Configuration export is not a credentials/session backup.

**Search, Activity, and Safe Diagnostics**

- RPC/CLI expose retained conversation search, including Archive, with project/Agent Worker/resolved account/execution-state/archive filters. Unified chronological activity covers execution/completion/failure, schedules/Run now, and PR handling with origin links. Permanent deletion removes related search/content; apply the same secret boundaries.
- Use structured logs with safe original error causes plus operation/session/Worker and correlation IDs. Preserve actionable distinctions for SQLite/filesystem/Git/provider failures without leaking raw stderr or credentials. Sanitize before RPC errors, history/events, or logs, including credential-bearing URLs and provider details. Inbox records remain readable/unread separately from resolving an action; completion/failure activity cannot implicitly approve or execute anything.

## Explicit Product Boundaries

- Keep the four required initial harnesses: Codex, Claude Code, OpenCode, and Grok Build. Adapter extensibility does not add more required initial harnesses.
- Model web search, image analysis, and image/video generation remain with native harnesses and user-configured tools; DeliDev may display their outputs but adds no search/vision sidecar, image-to-text substitution, or image/video generation bridge.
- Clients remain desktop/CLI; exclude browser clients, generic external-app proxies, container images/Dockerfiles/Compose, and benchmark links/rankings/snapshots. Distribute binaries and optional OS services.
- Do not add DeliDev dictation, audio transcription, or realtime voice. Ordinary text input, including OS dictation, remains usable.

## Acceptance and Integration Evidence

All behavior and exclusions above are required acceptance criteria across desktop, Connect RPC, and CLI; unsupported native combinations must be explicit. Cover success, failure, concurrent clients, disconnect/restart, and secret handling, including:

- CLI/sidecar parity, explicit startup, JSON/errors/exit codes/request-ID deduplication, hidden/stdin secrets; local/remote streaming, loopback/auth/origins; singleton readiness, backoff/explicit stop, services, pairing/revocation, proxies, signed updates, and desktop exit with server sessions/inbox preserved.
- All four real harnesses and supported account/OS combinations: executable path precedence/readiness, model discovery/manual IDs, native Plan/Steer/fork/compaction/options/templates, selected-account fidelity, unsupported protocols, and owned-process start identity. Test PTY natural exit, partial byte boundaries, handle/buffer/provider cleanup, safe reattachment, and crash requiring Resume.
- Coherent snapshot/cursor startup, incremental reconnect, gaps/deduplication/resnapshot, indexed per-message access and bounded buffers under large histories/slow consumers. Test late events after failure, uncertain prompt/Steer/reply acceptance, simultaneous replies, queue edit/mode/claim races, and no duplicate delivery.
- Cross-kind IDs, stale revisions, atomic state/events and routing updates, rollback, failed/destructive migrations, consistent backups/restore, corrupt/newer DB rejection without reset, and blocked-job isolation.
- Primary-repository cwd, remote/default-ref resolution, detached HEAD, fetch failure without stale fallback, all-repository preparation outside DB transactions, fork consistency, and original Local protection. Test snapshots of unpushed/ignored files and symlinks, unsupported files, disk-full/cancel/interruption, all-repository restore verification, offline cleanup, dependent Sidechat deletion, and managed-backup erasure.
- Separate outcome/archive/recovery: completed sessions retain results, failed/interrupted Archive stays recoverable, restoration does not dispatch, and Stop versus Archive resource scopes hold. Test read-only Sidechat authorization including malformed question-answer bypass, account browser sharing/deletion on offline clients and late flush, untrusted web/native/credential isolation, and authenticated explicit port forwarding.
- Six routing policies, initial Sequential exhaustion, persistent cursors/ties and traversal, weights, exclusions, preview purity, comparable minimum-window scores/reset evidence, stale quota non-recovery, and no failover. Validate restrictions, empty drafts, immutable first-execution snapshots, schedules/Run now/Overlap/Skip/FIFO Wait, offline skip/no catch-up, and reference deletion.
- Real GitHub issue/PR/PAT access and token-creation forms; every feedback type/revision/dismissal, stable reviewer identities/permissions, ruleset context/App/commit and per-kind unavailable gates, session reuse/dedicated strategy, one active fix per PR, Worker Git auth, merge/rebase lease safety, and durable default-three attempt limits.
- Retained search/activity/PR links, local review revisions, inbox/notification races, and all preserved settings/setup/tray/widget/import behaviors. Validate metadata-only diagnostics/doctor and seeded-secret leak checks across RPC/history/logs, retaining sanitized original causes/correlation.
- Verify harness-only usage counters/retries/forks/parent-child deduplication, actual versus estimated costs, pricing provenance/currencies/incompleteness, budget gates, and proxy scope/revocation/header/no-retry/no-translation boundaries.

Use real temporary SQLite/Git/PTY/process resources for integration tests and keep protocol/GitHub fixtures out of production paths. Record actual harness/version, account capability, OS, GitHub, and native lifecycle evidence. Fixtures, simulated replies, or passing builds alone must not be reported as real-environment validation; identify unverified combinations.

This integrates the non-UI lessons from delino-apps issue 111 and delino-apps PR 112, including all 20 review findings. DeliDev's explicit choices above supersede their conflicting defaults; new UI layout choices are deferred while this issue's existing UI requirements remain required.
