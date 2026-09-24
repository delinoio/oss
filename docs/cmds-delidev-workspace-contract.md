# DeliDev Worker workspace contract

## Scope
`cmds/delidev-cli/internal/workspace` owns Worker-local Git inspection, reference resolution, and all-repository preparation. It is independent of server SQLite and never receives a server GitHub PAT. Preparation is dispatched by authenticated outbound Worker jobs and published atomically into session metadata. Native harness execution, forks and uncertain-preparation reconciliation remain separate pending boundaries in the evidence ledger.

## Runtime and Language
Go and the execution machine's installed Git. No harness or Git installation is performed automatically.

## Users and Operators
The single-user server submits validated workspace jobs to the selected Worker. Local workspace identity is the actual originating machine, not the computer currently viewing a session.

## Interfaces and Contracts
Inspection accepts an absolute root, subdirectory, or linked worktree and resolves its canonical working-tree root, display name, remote names, and locally recorded remote defaults. It neither fetches nor returns remote URLs. An invalid preferred remote fails. Default reference selection uses the configured preferred remote, otherwise `origin`, otherwise the sole remote; missing or ambiguous defaults require input.

Worktree preparation resolves each repository's independently configured base and starting references. Automatic fetch updates the exact selected remote branch before resolving its commit; a failure blocks preparation without stale fallback. Explicitly disabled fetch uses the stored tracking ref. New worktrees are detached at the resolved commit, with no working branch creation. Identical base/starting references share the same resolved commit.

Local preparation uses existing checkouts and their current HEAD/tree, with no fetch, branch change, or worktree creation. It requires matching execution/origin machine IDs. General Chat creates an independent session-owned directory without Git. The primary path is the designated primary repository or the General Chat directory.

Preparation serializes by session while independent sessions can proceed concurrently. Git work is bounded and occurs outside database transactions. A durable ownership manifest is written before side effects; readiness is published only after every repository succeeds. Identical retries reuse a ready manifest; changed input cannot overwrite it. Partial preparation rolls back only newly owned worktrees. Failed cleanup retains a recoverable manifest; explicit retry cannot delete a ready workspace. Session Stop/Archive now cancel queued or claimed preparation through the owning job and retain ready workspaces. Active harness lifecycle and snapshot cleanup use separate product operations. Failure to acquire/inspect an existing ownership scope or persist its initial journal is uncertainty, not proof that cleanup completed; only confirmed rollback may permit another preparation attempt.

## Storage
The Worker owns private `workspaces`, `locks`, and empty hook directories under its explicit data scope. UUID-v7 session/repository IDs derive managed paths. Manifests record original checkouts separately from deletion-owned paths. Cleanup recomputes owned paths from identities, reconciles Git registration even when a directory is absent, and never removes original Local checkouts. These local resources intentionally override the repository R2 default.

Canonicalize the private Worker root before creating ownership paths. Git's registration namespace can resolve parent aliases (including macOS `/var` to `/private/var`); comparing it to an unresolved path can leave an orphaned Git registration after rollback. Parent-alias regression tests must verify both filesystem removal and Git registration removal.

## Security
Git receives a bounded system/Git/SSH environment, not inherited API keys, server authorization, or repository-redirection variables. Authentication uses the Worker's prepared Git credential helpers/SSH agent. Interactive Git credential prompts are disabled. Preparation disables repository hooks for its own Git invocation without modifying user configuration. Worker stream loss/revocation and heartbeat expiry cancel active owned Git work before further assignment. Canceled inspection retains the cancellation classification rather than being reported as an invalid checkout. Raw Git stderr and remote URLs do not enter diagnostics. Reads reject replacement links at private workspace roots.

## Logging
Structured preparation start/ready/failure/cleanup records contain session and machine IDs, typed workspace mode, and safe failure codes. Paths, Git remote URLs, file contents, and secrets are excluded.

## Build and Test
Run `go test -race ./cmds/delidev-cli/internal/workspace` and package vet. Tests create real temporary Git repositories, local remotes, linked worktrees, dirty Local trees, and separate General Chat directories. Validate fetch advancement/failure, detached commits, multi-repository rollback, idempotency, cancellation, original-checkout preservation, and missing-default rejection.

## Dependencies and Integrations
Worker jobs pass typed preparation requests/results over authenticated Connect. The server validates complete manifests against the immutable accepted preparation input and records ready/failed/canceled/uncertain state with the job in one transaction. Cancellation is persisted outside the claimed envelope so reconnect can deliver a precanceled assignment without invalidating its journal identity. The Worker gives each job an independent cancellation context; a late control cannot cancel the next job. Files remain on the Worker, with references and resolved commits recorded on the server. Native forks, snapshots, terminal ownership, and full child-process recovery are additional lifecycle boundaries, not implied by passing preparation tests.

## Change Triggers
Update this contract, the command contract, project index, scoped AGENTS, and evidence ledger when ownership, reference selection, cleanup, or Worker integration changes.

## References
- [Project](project-delidev.md)
- [Complete requirements](cmds-delidev-requirements.md)
- [Repository defaults](repository-defaults.md)

## Native process ownership
Worker Git operations run through the [owned process contract](cmds-delidev-process-contract.md), with the accepted job or session UUID as owner. A canceled or failed Git operation cannot authorize workspace rollback until its owned descendants are proven stopped. Uncertain process ownership retains the cleanup-pending manifest; retry cleanup reconciles that owner's indexed native process scopes before removing owned files.
