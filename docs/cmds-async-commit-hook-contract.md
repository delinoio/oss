# async-commit-hook command contract

## Scope
`cmds/async-commit-hook` owns the `ach` executable and its common application service. The [requirements snapshot](cmds-async-commit-hook-requirements.md) preserves the complete issue specification.

## Runtime and Language
Go; macOS 13+, Windows 10 22H2+, Ubuntu 22.04 LTS+, each amd64/arm64. Default shells are `sh` and Windows PowerShell. Explicit shell enums select sh, bash, powershell or pwsh. Git is required; Docker is not.

## Users and Operators
One developer OS account, explicitly trusted repositories and linked worktrees, humans and local coding agents.

## Interfaces and Contracts
`config validate` resolves the supplied repository directory to its Git worktree root and validates that worktree's current authoring file, including uncommitted edits. Nested `.config` files do not override the root file; linked worktrees use their own configuration. Run/plan validation remains commit-pinned.

Commands: init; config validate; run; status; wait; logs; check; inbox; ack; failures; plan; doctor; compare; rerun; cancel; ui; hooks install/uninstall; agent install/uninstall; agent-guide; daemon start/status/stop; browser list/revoke; mcp; pre-push; prune; self-update; version.

JSON responses carry schema_version=1. Exit codes: 0 operation success (gates only when passing), 1 failed/incomplete validation, 2 invalid usage/configuration, 3 runner/storage error, 4 wait expiration. Diagnostics use stable codes on stderr. Wait expiration never cancels work. CLI `wait --timeout` accepts integer seconds from 0 through 9223372036 (the maximum representable whole-second duration); larger values return `invalid-timeout` with exit 2 before waiting. Zero expires the query immediately.

Acknowledgement accepts only terminal runs, atomically with its idempotent timestamp. Premature calls return `acknowledgement-premature` without modifying inbox visibility; queries never acknowledge results.

Project TOML has version, optional diff_base, pre_push policy, and named checks. Checks declare command, depends_on, operating systems, shell, optional status, environment references, report declarations and scheduling policy/group. Personal TOML owns mode, api_port, state_dir, credentials and retention. Unknown fields/versions, cycles, absent dependencies and unsafe report paths fail validation. Accepted configuration and non-secret inputs are frozen; secrets are resolved from references and never fingerprinted by value.
Committed configuration reads consume at most 1 MiB plus one overflow-detection byte before parsing. Oversized objects return `invalid-config` (exit 2), and their Git reader is terminated and reaped without accepting a receipt. The exact 1 MiB boundary includes trailing newlines.

Registration is keyed by canonical Git common directory, not remote URL; each worktree has a separate ID. Automatic submission is idempotent within the same worktree, commit and execution fingerprint; changed declared public inputs produce a new attempt. Secret values remain excluded from the fingerprint. Explicit attempts are not deduplicated. Durable receipt precedes detached processing. Connect reruns return their accepted ID with an optional startup diagnostic if the runner cannot start; pre-acceptance failures remain transport errors. Startup failures log the accepted ID and a stable code, without exposing raw system errors in browser responses. Each run uses independent committed source with managed hooks disabled. Submodules and LFS are unsupported and diagnosed before execution.
LFS declaration detection follows the [Git attributes line format](https://git-scm.com/docs/gitattributes): ignore blank/comment lines, separate quoted or unquoted patterns from attributes, and match the exact `filter=lfs` value. Disabled examples and pattern text do not reject source. Raw LFS pointer blobs remain independently rejected during workspace preparation before any check starts.
Committed `.gitattributes` files are read sequentially through a 1 MiB per-file byte bound plus one overflow byte. Larger root or nested attribute files return `attributes-too-large` (exit 2) before receipt acceptance; the reader is terminated and reaped. Multiple attribute files are never buffered together.

Dependencies share a run workspace. Failed prerequisites block dependents. Groups coordinate in SQLite across workers; queue is FIFO, replacement waits for termination. Cancellation records its request before process control; terminal cancellation requires descendant reconciliation. Interrupted work is never replayed automatically. Unstarted requests recover. Daemon stop drains by default; force cancels. Temporary API viewers do not own check lifetimes. Account lifecycle control stays under the canonical home configuration directory independently of the selected personal TOML and state directory. Unix identities include precise kernel start times and observed descendant identities; Windows creates suspended children before assigning their Job Object. Configured retention runs after completed worker work; active inherited evidence is pinned against pruning.

Gate identity is repository, exact commit and configuration/execution fingerprint, including OS, architecture, shell and public declared inputs. The highest accepted sequence wins, including incomplete and pruned attempts. Optional failures are visible but do not fail the required gate. No applicable checks is incomplete. Exit status and required report evidence must agree; missing/malformed reports never pass. Failed reruns select failed/blocked checks plus prerequisite closure and explicitly reference inherited successful evidence.

Declared report output paths have a single owner across the graph (case-insensitively for portability). Before each check starts, its previous report files are removed with root-confined operations; committed or prerequisite-generated files cannot satisfy that check's required evidence. Unsafe output cleanup fails the check before command execution. Commands sharing a workspace must respect each other's declared output ownership; the workspace is not a sandbox against deliberately interfering commands.

Pre-push parses every actual branch-update object ID from stdin, ignores tags/deletions, and applies block (default), wait or run-and-wait. Hook/agent installation preserves unrelated data, records ownership, backs up edits and refuses conflicts. Codex conflict checks parse TOML key structure so quoted, escaped and dotted forms of the same MCP entry are equivalent. Comments and unrelated string values are preserved verbatim. Invalid input or a document that cannot be safely extended (such as an inline MCP parent table) fails before backups, ownership records, skill files or settings are published.
Hook write/sync/close or ownership-save failures remove the newly created file after checking its file identity and exact written contents. Retrying installation after a transient database failure remains idempotent. Concurrently edited/replaced hooks are preserved with a rollback-conflict diagnostic; successfully installed earlier hooks remain owned.

Project-scoped agent install/uninstall resolves `--repo` to the registered worktree root, including when invoked from a nested directory or linked worktree. User-scoped integration paths remain independent of the current repository.

`run-and-wait` submits a fresh attempt when the latest compatible result is terminal but nonpassing, including expired or missing evidence. It waits for an existing unfinished attempt without duplicating it; `wait` never submits work.
The shared pre-push service selects that attempt, evaluates retained passing evidence and inserts any necessary replacement inside one immediate SQLite transaction. Concurrent clients, including separate database connections, reuse the same pending attempt for the repository/commit/fingerprint. The transaction ends before starting workers or waiting. Ordinary explicit run/rerun requests still create independent attempts.

Cancellation of an unstarted queued run acquires its worker ownership lock and atomically completes the run and unfinished checks without launching a worker. If a worker owns the run or any process may have started, cancellation remains a request until that owner reconciles descendants. Terminal check publication reads the cancellation marker and writes the result in one immediate transaction. A request committed before that publication overrides the worker's proposed outcome, including during evidence collection; a request arriving after publication does not rewrite terminal evidence. Completion logs use the committed outcome.

## Storage
Run aggregation uses explicit precedence: interrupted, cancelled, replaced, expired, failed, blocked, then passed. Check names and traversal order cannot change it. Optional failed/blocked checks are ignored for this aggregate, while cancellation/replacement/interruption remain visible. A failed prerequisite therefore yields a failed run even when a later-sorted dependent is blocked. No applicable checks still produces a failed, nonpassing run.

SQLite WAL with foreign keys, transactional claims and durable accepted ordering. User-only state includes registry, attempts, checks, coordination, browser hashes, acknowledgements and diagnostics. Logs/reports are owned files with integrity metadata. Indefinite retention is default; pruning protects active work and retains authoritative expired-attempt records. Original-repository removal never deletes results.

Repeated pruning skips reclaimed expired records without appending diagnostics. If a crash leaves owned files after the expiry commit, pruning resumes their cleanup without adding another expiry diagnostic.

## Security
Reports are parsed from the bounded original in-memory bytes (at most 64 MiB). Redaction uses bounded buffers and writes an atomic evidence file while computing its digest; the complete expanded copy is never held in memory. Redacted evidence is limited to 640 MiB, the maximum possible expansion of a one-byte secret into `[REDACTED]`. Extracted failure text retains only a bounded redacted prefix with explicit truncation. Extracted test/message/file/command text and the separate persisted report copy are redacted before storage. A secret matching XML/JSON syntax therefore cannot corrupt validation; the redacted evidence copy may no longer be syntactically parseable and is displayed as inert text, never used to rederive outcomes.

Allowlisted system context plus declared inputs only. Credential references resolve locally; raw credentials/full environments never enter metadata. Streaming redaction covers chunk boundaries and report fields. Local state is account-restricted. Cancellation verifies process identity; cleanup is confined to owned paths. API security is specified in the protocol contract. A workspace isolates source, not hostile commands.

## Logging
Pending-run reconciliation/storage failures retry after 1, 2, 4, 8, 16, 32 and then at most one attempt per 60 seconds per run, with structured `retry_after_ms` diagnostics. Scheduling claims remain intact and other runs remain eligible. On-demand workers retain pending retries until shutdown or completion; this is a retry delay, not a command timeout. A run owned by another worker is probed no more often than every 200 ms.

Structured log/slog lifecycle events contain run/check IDs, outcomes and stable diagnostic codes. Never log credentials, pairing codes or complete environments. CLI colors honor NO_COLOR and nonterminal output.

An unexpected storage/worker failure reaps its owned commands even if the database can no longer persist cancellation. Restart reconciliation retains interruption instead of replaying commands. MCP preserves the same typed error codes, including `wait-expired`, as the CLI.

## Build and Test
`go test ./cmds/async-commit-hook/...`, race tests, six-target CGO-free builds, generated protocol checks, installer/update and lifecycle integration tests. Repository-wide Go compilation first generates the DevHud administrator embed. Real six-target machine validation is explicitly excluded by the owner.

Shared environment/redaction integration fixtures select native POSIX or PowerShell syntax while asserting the same environment exclusion, log masking and report failure behavior. Windows cross-compilation is recorded separately from executing these tests on Windows.

## Dependencies and Integrations
SQLite modernc, UUID v7, Connect, official MCP Go SDK, Git, OS process APIs, TOML, Sigstore verification. Direct updates require stopped checks/servers, a consistent state backup, authenticated artifacts and recoverable replacement; Homebrew installs are never replaced by self-update.

## Change Triggers
Update project index, protocol/client/app contracts and AGENTS when public behavior changes.

## References
- [Project](project-async-commit-hook.md)
- [Repository defaults](repository-defaults.md)

Repository/worktree listings discover the current checkout branch on access, including detached HEAD. A missing or mismatched worktree is unavailable and retains its last recorded label; historical execution branches never change with later checkouts.

Crash recovery resumes a preparing/running request when all local checks are still queued or skipped with no process identity or start timestamp (inherited results are already complete). Under the run ownership lock it removes partial source, resets the run to queued and prepares fresh committed source. A claimed/preparing check is ambiguous and remains interrupted; it is never automatically replayed.

Workspace materialization uses one raw `git cat-file --batch` session, validates each OID/type/size and streams regular blobs through a 32 KiB copy buffer. Large committed files are not loaded whole. Symlink targets are bounded at 64 KiB, LFS pointer prefixes are rejected before writing, and filters/hooks/line-ending conversion remain disabled. Batch framing follows the [Git cat-file contract](https://git-scm.com/docs/git-cat-file).

Check execution returns persistence/reconciliation failures through the worker completion channel. The worker cancels and joins its remaining commands before releasing ownership; a claimed check whose state could not be saved remains recoverable as interrupted instead of leaving a live scheduler waiting forever.

Structured failure text fields are capped at 4 KiB of valid UTF-8, with a 1 MiB aggregate JSON summary budget per run enforced transactionally across checks. Existing records are bounded on read too. Synthesized diagnostic failures share those field and aggregate limits with stored failures, with space reserved for a stable truncation notice when details are shortened or omitted. `failure-summaries-truncated` discloses omitted details; full redacted reports remain available through evidence pagination. Stable failure IDs are computed from the original identities before truncation. Synthesized diagnostics include their check, code, full message identity and occurrence among identical diagnostics. Distinct report paths and repeated identical diagnostics remain separate in comparisons and UI keys; reordering distinct diagnostics does not change their IDs.
JUnit failure identities encode the full suite ancestry, occurrence among equally named sibling suites/testcases, failure/error kind and occurrence within the testcase. Messages are excluded, and persisted failures are additionally scoped to their declared report kind/path. Identical duplicates are matched by occurrence order; removing one resolves one failure without collapsing all identical cases. Existing stored IDs are not rewritten, so comparisons across this parser upgrade can show old IDs resolved and new IDs added once.

Registry trust binds the canonical common directory to a non-secret, locally generated UUID in its `ach-repository-id` file; Git cloning does not copy that metadata. Reusing a checkout path cannot inherit trust. Explicit `init` creates a new repository/worktree registration for a replacement while keeping the original IDs, runs and unavailable source records. Current-source APIs verify that binding before accessing a registered path. The v1 registry extension transactionally removes path-only uniqueness without rewriting historical IDs; legacy path-only registrations remain historical until explicit reinitialization establishes a local identity.

## Unix process ownership

Each Unix check uses a separate supervisor re-executed from the same `ach` binary. Its non-secret ownership journal is retained under the run's owned evidence tree and pruned with that run. The worker persists the scope path while preparing, then the live ownership identity while running, before releasing the command start barrier. Commands and resolved environment travel only through an account-private Unix socket; launchd plists and journals never contain those values. A killed worker closes that socket and triggers supervisor cleanup independently of SQLite availability. Before releasing the start barrier, the worker also registers the supervisor as an independent account lifecycle component under the existing lifecycle lock and configuration fingerprint. That lease survives worker death and, when ownership is still unproven, supervisor death. Active queries use backend/boot proof rather than the owner PID alone. Proven inactive leases are removed before pruning their journals, so mode, state path, port changes and self-update cannot bypass detached work.

Linux uses `PR_SET_CHILD_SUBREAPER` before starting the shell. Orphaned descendants are adopted even across multiple forks and `setsid`. After the shell is reaped, `wait4` returning `ECHILD` proves no descendant remains. The supervisor remains alive and retains adoption ownership when inspection/termination cannot complete; cancellation returns an error and keeps the group claim. If the supervisor itself is killed before durable completion, recovery cannot infer emptiness from absent PIDs and fails closed. A changed kernel boot ID is independent proof that the old processes are gone. A completed journal plus matched owner termination permits ordinary restart recovery.

macOS uses one temporary `launchd` job in `user/<uid>` with `LimitLoadToSessionType=Background`, no keep-alive and no login/startup registration. Its resource coalition survives fork/exec, session changes and reparenting. The supervisor verifies a dedicated one-member coalition before allowing commands. Kernel task accounting, not an empty process-list sample, proves command descendants have exited. Cleanup removes the exact random job label and confirms zero active coalition tasks before the worker completes. Recovery is bound to the kernel boot UUID, coalition ID and precise process birth identities, including after the supervisor itself exits. `bootout` alone is insufficient: reparented descendants outside the original process group may survive it.

The kernel interfaces are present in Apple's macOS 13 [XNU 8792.41.9 coalition ABI](https://github.com/apple-oss-distributions/xnu/blob/xnu-8792.41.9/osfmk/mach/coalition.h) and [process information implementation](https://github.com/apple-oss-distributions/xnu/blob/xnu-8792.41.9/bsd/kern/proc_info.c). Calls validate returned shapes and fail closed on unavailable interfaces. This does not require Endpoint Security entitlements or an elevated service. Actual macOS 13 machine qualification remains within the owner's six-target testing exclusion.

Pre-upgrade sampled process records cannot prove that unobserved descendants exited. Finish active work before upgrading; unresolved legacy ownership remains a diagnostic requiring explicit reconciliation, never a synthesized cancellation. Existing successful terminal evidence is unchanged. Windows continues using Job Objects.

### Review provenance

PR #901 thread `PRRT_kwDORRAKg86j9n3x` correctly identified the old sampling race. The third repair pass reproduced successful reconciliation leaving a double-forked, reparented child alive. Faster polling and environment markers were rejected because neither proves ownership. XNU rejects the historic kqueue child-tracking flags, and a full Endpoint Security client would introduce an entitlement contract. The fourth pass instead validated inherited resource coalitions and Linux subreapers, including cancellation, normal exit, worker disconnection, recovery and exclusive replacement. See the evidence document for exact executed checks and their platform limits.

Environment secrecy is project-wide: declarations for the same case-insensitive environment name must agree on `secret`, including across checks and OS filters. Reject conflicts before collecting public inputs, fingerprinting or accepting a run.

Commit browsing normalizes invalid subject bytes for display and protobuf/JSON transport; commit and parent IDs remain exact Git object IDs. Raw commit objects are unchanged.
Branch-list responses likewise normalize display labels at the API boundary while preserving raw Git refs and their commit object IDs.
Repository/worktree listing names, display paths and branch labels use the same valid-UTF-8 transport boundary. Raw database paths and stable IDs are unchanged; source access continues to resolve the registered worktree ID through its original path.

Go test JSON accepts individual events within the 64 MiB report limit, including a final event without a newline and CRLF separators. Line traversal references the already bounded input without a second event buffer. Invalid or truncated JSON and incomplete tests still fail validation.

Go test failure IDs encode the check, separate package/test identity and occurrence among completed iterations of that test. Passed and skipped iterations also advance the counter, so fixing an earlier iteration preserves a later failure's identity. Package-level failures remain separate, messages are excluded, and declared report kind/path still namespace persisted IDs. Existing stored IDs are not rewritten; comparisons across this parser upgrade can show old identities resolved and new ones added once.

Go test JSON collection keeps at most 64 KiB of chronological output per active test in lazily allocated circular buffers. Appends copy only new bytes; terminal events release their buffers after failure summaries are materialized. Complete redacted report evidence is preserved.

Retention ages are accepted only in the inclusive range 0..106751 days, with zero meaning indefinite retention. Personal configuration and CLI/shared-service pruning reject larger values before any duration conversion or mutation. Byte quotas remain nonnegative int64 values.
