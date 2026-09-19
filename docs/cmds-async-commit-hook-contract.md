# async-commit-hook command contract

## Scope
`cmds/async-commit-hook` owns the `ach` executable and its common application service. The [requirements snapshot](cmds-async-commit-hook-requirements.md) preserves the complete issue specification.

## Runtime and Language
Go; macOS 13+, Windows 10 22H2+, Ubuntu 22.04 LTS+, each amd64/arm64. Default shells are `sh` and Windows PowerShell. Explicit shell enums select sh, bash, powershell or pwsh. Git is required; Docker is not.

## Users and Operators
One developer OS account, explicitly trusted repositories and linked worktrees, humans and local coding agents.

## Interfaces and Contracts
Commands: init; config validate; run; status; wait; logs; check; inbox; ack; failures; plan; doctor; compare; rerun; cancel; ui; hooks install/uninstall; agent install/uninstall; agent-guide; daemon start/status/stop; browser list/revoke; mcp; pre-push; prune; self-update; version.

JSON responses carry schema_version=1. Exit codes: 0 operation success (gates only when passing), 1 failed/incomplete validation, 2 invalid usage/configuration, 3 runner/storage error, 4 wait expiration. Diagnostics use stable codes on stderr. Wait expiration never cancels work.

Acknowledgement accepts only terminal runs, atomically with its idempotent timestamp. Premature calls return `acknowledgement-premature` without modifying inbox visibility; queries never acknowledge results.

Project TOML has version, optional diff_base, pre_push policy, and named checks. Checks declare command, depends_on, operating systems, shell, optional status, environment references, report declarations and scheduling policy/group. Personal TOML owns mode, api_port, state_dir, credentials and retention. Unknown fields/versions, cycles, absent dependencies and unsafe report paths fail validation. Accepted configuration and non-secret inputs are frozen; secrets are resolved from references and never fingerprinted by value.

Registration is keyed by canonical Git common directory, not remote URL; each worktree has a separate ID. Automatic submission is idempotent, explicit attempts are not. Durable receipt precedes detached processing. Each run uses independent committed source with managed hooks disabled. Submodules and LFS are unsupported and diagnosed before execution.

Dependencies share a run workspace. Failed prerequisites block dependents. Groups coordinate in SQLite across workers; queue is FIFO, replacement waits for termination. Cancellation records its request before process control; terminal cancellation requires descendant reconciliation. Interrupted work is never replayed automatically. Unstarted requests recover. Daemon stop drains by default; force cancels. Temporary API viewers do not own check lifetimes. Account lifecycle control stays under the canonical home configuration directory independently of the selected personal TOML and state directory. Unix identities include precise kernel start times and observed descendant identities; Windows creates suspended children before assigning their Job Object. Configured retention runs after completed worker work; active inherited evidence is pinned against pruning.

Gate identity is repository, exact commit and configuration/execution fingerprint, including OS, architecture, shell and public declared inputs. The highest accepted sequence wins, including incomplete and pruned attempts. Optional failures are visible but do not fail the required gate. No applicable checks is incomplete. Exit status and required report evidence must agree; missing/malformed reports never pass. Failed reruns select failed/blocked checks plus prerequisite closure and explicitly reference inherited successful evidence.

Declared report output paths have a single owner across the graph (case-insensitively for portability). Before each check starts, its previous report files are removed with root-confined operations; committed or prerequisite-generated files cannot satisfy that check's required evidence. Unsafe output cleanup fails the check before command execution. Commands sharing a workspace must respect each other's declared output ownership; the workspace is not a sandbox against deliberately interfering commands.

Pre-push parses every actual branch-update object ID from stdin, ignores tags/deletions, and applies block (default), wait or run-and-wait. Hook/agent installation preserves unrelated data, records ownership, backs up edits and refuses conflicts.

Project-scoped agent install/uninstall resolves `--repo` to the registered worktree root, including when invoked from a nested directory or linked worktree. User-scoped integration paths remain independent of the current repository.

`run-and-wait` submits a fresh attempt when the latest compatible result is terminal but nonpassing, including expired or missing evidence. It waits for an existing unfinished attempt without duplicating it; `wait` never submits work.

Cancellation of an unstarted queued run acquires its worker ownership lock and atomically completes the run and unfinished checks without launching a worker. If a worker owns the run or any process may have started, cancellation remains a request until that owner reconciles descendants.

## Storage
SQLite WAL with foreign keys, transactional claims and durable accepted ordering. User-only state includes registry, attempts, checks, coordination, browser hashes, acknowledgements and diagnostics. Logs/reports are owned files with integrity metadata. Indefinite retention is default; pruning protects active work and retains authoritative expired-attempt records. Original-repository removal never deletes results.

Repeated pruning skips reclaimed expired records without appending diagnostics. If a crash leaves owned files after the expiry commit, pruning resumes their cleanup without adding another expiry diagnostic.

## Security
Allowlisted system context plus declared inputs only. Credential references resolve locally; raw credentials/full environments never enter metadata. Streaming redaction covers chunk boundaries and report fields. Local state is account-restricted. Cancellation verifies process identity; cleanup is confined to owned paths. API security is specified in the protocol contract. A workspace isolates source, not hostile commands.

## Logging
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
