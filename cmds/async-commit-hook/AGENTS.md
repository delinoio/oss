# async-commit-hook command ownership

- The common application service owns scheduling, final validation, acknowledgement, retention and comparison. CLI, Connect and MCP adapters must not implement competing decisions.
- Pre-push `run-and-wait` renews terminal nonpassing attempts and waits for existing unfinished attempts; `wait` never submits work.
- Acknowledgement is idempotent and accepts terminal runs only; check terminal state and persist the timestamp in one transaction.
- Account lifecycle control remains in the canonical home configuration directory even when `--config` or `state_dir` changes. Tests must use isolated homes or explicit injected `Paths`; never touch developer credentials or real account state.
- Committed source is streamed through one validated raw Git blob batch per workspace, with bounded buffers. Never enable original hooks, filters, submodules or LFS implicitly. Hook installation discovery must not use the execution helper's disabled-hooks override.
- Persist the receipt before starting any worker. Queue order uses accepted sequence; cancellation/replacement cannot release a group before owned processes are reconciled by birth identity or a Windows Job Object.
- Automatic deduplication includes the worktree, commit and execution fingerprint; changed public inputs must receive a compatible new attempt.
- Unix checks use a separate same-binary supervisor and a durable start barrier: Linux subreaper ownership and macOS temporary background launchd resource coalitions survive reparenting. Only kernel emptiness proof permits completion/replacement. Never substitute PID sampling or environment markers. Lost journals or a killed Linux supervisor fail closed; boot identity permits recovery after a host reboot. Stop pre-upgrade runs before installing ownership backend changes.
- Independent supervisors retain an account lifecycle lease after worker death. Reclaim it only with backend completion/emptiness or changed-boot proof, and clear completed stale leases before pruning ownership journals. Mode/state/port changes and updates must reject active or unproven leases.
- Cancellation may finalize an unstarted queued run only while holding its worker ownership lock, with check and run transitions committed together. Owned or previously started work still requires worker reconciliation.
- Report and log reads use owned artifact IDs and bounded, root-confined reads. Keep known-secret redaction before persistence, including streamed chunk boundaries and structured failures.
- Evidence text responses must be valid UTF-8 while pagination and integrity continue to use the original stored bytes.
- Preserve complete UTF-8 runes across page boundaries and defer incomplete live tails; never replace valid split characters merely because a page budget ends.
- Normalize Git diff text for protobuf only after applying its raw-byte truncation limit.
- Each declared report path has one check owner across the graph. Clear its previous file through the owned workspace root before starting that check; stale committed or prerequisite reports must never satisfy validation.
- Pruning is idempotent for expired records; resume incomplete owned-file cleanup without growing tombstone diagnostics.
- Use synchronization barriers, not elapsed-time assertions, to prove asynchronous behavior in integration tests. Run `go test -race ./cmds/async-commit-hook/...` for lifecycle changes, and the root Go suite after generating administrator assets.
- Agent integration tests use isolated settings and preserve unrelated entries and comments. Keep the object-form MCP output schema compatible with supported clients.
- Worker-correctness fixtures must distinguish injected failures from deadlock watchdog expiry; allow native shell cold startup under CI load instead of imposing an undocumented command-startup SLA.
- Shared runner security tests must use syntax for the selected native shell, retaining Windows PowerShell coverage rather than running POSIX fixtures under PowerShell.
- Project agent installation and removal resolve any supplied subdirectory to its registered Git worktree root; linked worktrees retain independent integration paths.
- Repository listings resolve the current checkout branch, including detached HEAD, without rewriting historical run branches.
- Propagate every check state persistence failure to the run worker; never discard an execution error and leave a claimed check unscheduled.
- Unexpected storage/worker exits must cancel and reap owned commands even when SQLite can no longer record a cancellation. A normal daemon stop still drains.
- Resume interrupted preparation only when every local check is provably unclaimed and unstarted under the run lock; remove its partial workspace first. A claimed check remains interruption recovery, never an automatic replay.
- Release verification and replacement follow `docs/cmds-async-commit-hook-release-contract.md`; never weaken the pinned workflow identity or package-manager ownership checks.
- Bound structured failure fields and the aggregate per-run summaries before persistence and on legacy reads; disclose truncation and preserve complete paginated report evidence.
- Windows updater helper cleanup survives replacement-journal removal; retain digest/birth-scoped cleanup metadata until a later launch confirms exit and removes the exact helper.
- Repository trust requires the common-directory local UUID as well as its canonical path. Reused paths need explicit initialization; preserve historical IDs and reject source access through stale worktree registrations.

- Reject conflicting secret/public classifications for an environment name across the complete project graph, using case-insensitive names for Windows portability, before reading public snapshot inputs.

- Normalize arbitrary Git commit-subject bytes into valid UTF-8 for display/transport while preserving commit and parent object IDs.

- Collect Go test output in lazily grown bounded tails; appending an event must not copy the entire retained tail. Release per-test output after terminal events while preserving failure text.
