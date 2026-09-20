# async-commit-hook command ownership

- The common application service owns scheduling, final validation, acknowledgement, retention and comparison. CLI, Connect and MCP adapters must not implement competing decisions.
- Validate every dependency edge across all supported OS values: prerequisites must apply wherever the dependent applies, including optional prerequisites; empty OS lists mean all platforms.
- Aggregate check outcomes independently of names/order: interrupted, cancelled, replaced, expired, failed, blocked, then passed. Optional failed/blocked outcomes do not fail the run; lifecycle loss remains visible, and no applicable checks is failed.
- Newly accepted pre-push attempts record the pushed local branch, or the remote destination branch for HEAD/object-ID refspecs; never substitute the checkout branch or relabel reused attempts. Branches remain navigation context, not gate identity.
- Pre-push `run-and-wait` renews terminal nonpassing attempts and waits for existing unfinished attempts; `wait` never submits work.
- Pre-push hashes terminal evidence outside SQLite transactions, then reselects the latest attempt and compares its full metadata snapshot in a short immediate transaction before reuse or replacement insertion. Concurrent clients reuse one pending attempt without blocking unrelated workers during file I/O.
- Acknowledgement is idempotent and accepts terminal runs only; check terminal state and persist the timestamp in one transaction.
- Account lifecycle control remains in the canonical home configuration directory even when `--config` or `state_dir` changes. Tests must use isolated homes or explicit injected `Paths`; never touch developer credentials or real account state.
- Stream NUL-delimited Git tree records directly into validation/materialization, retaining no full listing or entry slice; bound one record to 1 MiB and kill/reap readers on failure.
- Committed source is streamed through one validated raw Git blob batch per workspace, with bounded buffers. Never enable original hooks, filters, submodules or LFS implicitly. Hook installation discovery must not use the execution helper's disabled-hooks override.
- Initialize managed Git workspaces with the source repository's storage object format (`sha1` or `sha256`) before fetching the exact commit; never rely on Git's default hash algorithm.
- Read committed project configuration through a 1 MiB plus one-byte stream limit before parsing; terminate and reap oversized Git readers rather than buffering the full blob.
- Raw LFS detection inspects the complete candidate only below the upstream 1024-byte cutoff and requires valid version, SHA-256 OID, nonnegative size and extension syntax; header-only examples remain ordinary source.
- LFS declaration checks ignore attribute comments and pattern text, match exact filter values, and retain independent raw-pointer rejection before commands start.
- Stream committed attribute files one at a time with a 1 MiB per-file limit plus one overflow byte; reject overflow before accepting a run and reap the Git reader.
- Connect rerun returns its durable run ID with an optional startup diagnostic after post-acceptance startup failure; only pre-acceptance failures are transport errors.
- Persist the receipt before starting any worker. Queue order uses accepted sequence, including older queued checks awaiting dependencies; cancellation/replacement cannot release a group before owned processes are reconciled by birth identity or a Windows Job Object.
- Fingerprint an explicit execution projection (version/check graph, public inputs, OS and architecture); push policy and diff base never affect compatibility. Preserve complete settings in snapshots and the constant legacy default-policy serialization slot for existing default v1 digests.
- Automatic deduplication includes the worktree, commit and execution fingerprint; changed public inputs must receive a compatible new attempt.
- Unix checks use a separate same-binary supervisor and a durable start barrier: Linux subreaper ownership and macOS temporary background launchd resource coalitions survive reparenting. Only kernel emptiness proof permits completion/replacement. Never substitute PID sampling or environment markers. Lost journals or a killed Linux supervisor fail closed; boot identity permits recovery after a host reboot. Stop pre-upgrade runs before installing ownership backend changes.
- Independent supervisors retain an account lifecycle lease after worker death. Reclaim it only with backend completion/emptiness or changed-boot proof, and clear completed stale leases before pruning ownership journals. Mode/state/port changes and updates must reject active or unproven leases.
- Finalize captured logs (flush redaction, sync, close and digest) before terminal publication even when startup fails or cancellation arrives before launch; empty logs retain valid integrity metadata and I/O failures remain diagnostic.
- Terminal check publication reads and applies the cancellation marker in the same immediate transaction as its state write, after process reconciliation; log the committed outcome.
- Cancellation may finalize an unstarted queued run only while holding its worker ownership lock, with check and run transitions committed together. Owned or previously started work still requires worker reconciliation.
- Report and log reads use owned artifact IDs and bounded, root-confined reads. Keep known-secret redaction before persistence, including streamed chunk boundaries and structured failures.
- Stream report redaction into atomic evidence files with bounded buffers and a 640 MiB maximum redacted size for 64 MiB inputs; cap extracted text during redaction instead of buffering expanded summaries.
- Parse bounded original report bytes only in memory before redacting report evidence and extracted text for persistence; secret values overlapping XML/JSON syntax must not alter validation outcomes.
- Evidence text responses must be valid UTF-8 while pagination and integrity continue to use the original stored bytes.
- Preserve complete UTF-8 runes across page boundaries and defer incomplete live tails; never replace valid split characters merely because a page budget ends.
- Normalize Git diff text for protobuf only after applying its raw-byte truncation limit.
- Each declared report path has one check owner across the graph. Clear its previous file through the owned workspace root before starting that check; stale committed or prerequisite reports must never satisfy validation.
- Pruning is idempotent for expired records; resume incomplete owned-file cleanup without growing tombstone diagnostics.
- Use synchronization barriers, not elapsed-time assertions, to prove asynchronous behavior in integration tests. Run `go test -race ./cmds/async-commit-hook/...` for lifecycle changes, and the root Go suite after generating administrator assets.
- Inspect Codex MCP ownership through parsed TOML keys, including equivalent quoted/escaped/dotted forms; validate the merged document before any backup, ownership or skill publication while preserving unrelated text.
- Agent integration tests use isolated settings and preserve unrelated entries and comments. Keep the object-form MCP output schema compatible with supported clients.
- Ordinary hook uninstall visits post-commit and pre-push without a repeated opt-in flag, deletes ownership for already-missing owned hooks, skips unrelated hooks and refuses modified owned files. Installation still requires explicit pre-push opt-in.
- Reinstall unchanged product-owned hooks using the current executable/configuration, backing up and atomically replacing stale bodies. Persist both exact versions before refresh so retries/removal recover interrupted publication; preserve user edits.
- Failed hook publication or ownership persistence rolls back only the newly created, identity-and-content-matching file; concurrent edits remain untouched with a rollback conflict diagnostic.
- Worker-correctness fixtures must distinguish injected failures from deadlock watchdog expiry; allow native shell cold startup under CI load instead of imposing an undocumented command-startup SLA.
- Shared runner security tests must use syntax for the selected native shell, retaining Windows PowerShell coverage rather than running POSIX fixtures under PowerShell.
- Project agent installation and removal resolve any supplied subdirectory to its registered Git worktree root; linked worktrees retain independent integration paths.
- Working-tree configuration validation resolves `--repo` through Git discovery, including nested directories and linked worktrees, before reading the selected root's uncommitted file.
- Initial project configuration discovery, staging and atomic no-replace publication use the selected worktree's root-confined handle; symlink escapes must fail before repository trust registration or external filesystem writes.
- Repository listings resolve the current checkout branch, including detached HEAD, without rewriting historical run branches.
- Workspace-preparation failure must not finalize a run until every affected check outcome is persisted; return a check-save error so the run remains recoverable.
- Propagate every check state persistence failure to the run worker; never discard an execution error and leave a claimed check unscheduled.
- Unexpected storage/worker exits must cancel and reap owned commands even when SQLite can no longer record a cancellation. A normal daemon stop still drains.
- Retain pending-run failures with per-run exponential retry delays from one second to one minute; never hot-loop reconciliation or release an unproven scheduling claim. On-demand workers wait for pending retry work and stop promptly on shutdown.
- Resume interrupted preparation only when every local check is provably unclaimed and unstarted under the run lock; remove its partial workspace first. A claimed check remains interruption recovery, never an automatic replay.
- Release verification and replacement follow `docs/cmds-async-commit-hook-release-contract.md`; never weaken the pinned workflow identity or package-manager ownership checks.
- Apply the same failure field and aggregate response budgets to synthesized diagnostics; reserve a stable truncation notice so omitted failures remain visible.
- Bound structured failure fields and the aggregate per-run summaries before persistence and on legacy reads; disclose truncation and preserve complete paginated report evidence.
- Synthesized diagnostic identities include check, code, original message and occurrence among identical diagnostics; compute IDs before display truncation.
- Structured source lines are positive int32 values (1..2147483647); omit malformed, nonpositive or overflowing report locations as zero before persistence and on legacy/API reads, retaining the report failure itself.
- JUnit identities include suite ancestry and deterministic sibling/test/failure occurrences; namespace persisted report failures by report kind/path to prevent duplicate comparison and UI keys.
- Windows updater helper cleanup survives replacement-journal removal; retain digest/birth-scoped cleanup metadata until a later launch confirms exit and removes the exact helper.
- Update helpers revalidate the exact prepared journal under the lifecycle lock after waiting for their parent; a recovered or replaced journal must never be replayed from memory.
- Recovery of an absent executable requires a regular nonsymlink backup matching OriginalSHA256 within the executable-size bound. Atomically publish the authenticated bytes without replacing an existing path, retain the backup, and preserve the journal on every validation/restoration failure.
- Repository trust requires the common-directory local UUID as well as its canonical path. Reused paths need explicit initialization; preserve historical IDs and reject source access through stale worktree registrations.

- Reject duplicate environment names within each check and conflicting secret/public classifications across the complete project graph, using case-insensitive names for Windows portability, before reading public snapshot inputs or resolving credentials.

- Normalize arbitrary Git commit-subject bytes into valid UTF-8 for display/transport while preserving commit and parent object IDs.
- Branch-list transport labels also normalize invalid Git bytes to valid UTF-8 without changing the referenced commit IDs or raw Git refs.
- Repository/worktree transport names, paths and branch labels must be valid UTF-8; preserve raw database paths for filesystem/Git access and use IDs for API selection. Reload operational run source paths from the worktree registry and historical branch identity from the run column, never lossy JSON display snapshots; unavailable checkouts must not block history reads.

- Accept Go test JSON events up to the bounded report size; slice the existing report lines without a smaller scanner token limit or a second full event buffer.
- Go test failure IDs include separate package/test identity and a deterministic completed-iteration occurrence, counting pass/skip as well as fail; messages never determine identity.
- Collect Go test output in lazily grown bounded tails; appending an event must not copy the entire retained tail. Release per-test output after terminal events while preserving failure text.

- Bound CLI wait timeouts to 0..9223372036 seconds before conversion to time.Duration; zero expires the query immediately and never cancels work. Check context expiration before loading a run and again before accepting the read result, including already-terminal runs.

- Bound retention ages to 0..106751 days in both personal configuration and the shared prune service before converting days to time.Duration or mutating evidence; zero remains indefinite retention.

- Connect list projections omit check arrays and diagnostics and include total check_count; preserve complete check detail for GetRun and CLI/MCP so list size never multiplies by the graph size.

- Account lifecycle compatibility includes personal credential reference mappings, never their resolved values. Reject reuse/startup under changed references while any old owner remains.

- Reject non-UTF-8 declared public environment values before snapshot serialization or fingerprinting; JSON replacement must never change execution inputs or collapse their identity.

- Completion publication must unblock on worker ownership cancellation; error shutdown joins all runs even when their count exceeds the completion buffer. Do not impose a concurrency cap to avoid shutdown deadlocks.

- Credential file reads require a regular nonsymlink file, verify the opened identity, and consume at most 64 KiB plus one overflow byte. Unix opens must not block on replacement FIFOs; diagnostics exclude paths and values.

- Large response fixtures must assert the expanded-byte threshold and complete large-graph detail without duplicating maximal SQLite graphs across every pagination row; retain native Windows test budget for execution coverage.

- Page Connect registry reads across worktrees, including within one repository, before Git probing or serialization. Bound display labels without changing raw paths or IDs; cursor validation precedes storage access.

- Accept interleaved Go build-output/build-fail events using ImportPath, separate bounded output tails and build failure identities. Preserve package/test failures and reject build events without an import path.

- Agent install/reinstall/uninstall rejects final-component symlinks and nonregular configuration/skill files before backup, ownership or publication. OpenCode fallback discovery uses Lstat so dangling JSONC links cannot be bypassed.

- Stream branch refs into count- and byte-bounded pages; use worktree-scoped lexical cursors and cancel/reap partial Git readers. Never buffer all refs before bounding a response.

- Disable replacement objects on every managed Git invocation, including streamed tree/blob/config reads. Local refs/replace cannot reinterpret exact-commit receipt identity or execution source.

- Reject ACH_MANAGED environment declarations case-insensitively across the complete graph before snapshotting or credential resolution; it is a runner-owned recursion guard.

- Agent merges snapshot file identity and exact bytes before parsing; revalidate both immediately before atomic replacement or removal, and use no-replace creation for initially absent paths. Rollback must compare exact published skill ownership before restoring/removing it.

- Automatically publish hooks only into the canonical native common-directory hooks folder, never a custom or symlink-redirected folder even when empty. Supply worktree-scoped manual commands for shared locations; owned uninstall remains available.
