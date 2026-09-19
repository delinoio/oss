# async-commit-hook implementation evidence

The complete [issue requirements](cmds-async-commit-hook-requirements.md) remain intact. This matrix was created before runtime implementation and maps them to the delivered source and executable evidence. Verification below was performed on September 19, 2026.

The initial product version is `0.1.0` across the executable, app/client packages, installers and release metadata. Configuration, state, CLI JSON and API schema versions remain `1`. The `0.1.0` artifacts passed the full Go suite, frontend tests/build, release/workflow checks and all six target builds; native `ach version --json` reports `0.1.0`.

## Completion boundary

The owner approved exactly two changes to the original acceptance criteria: the real-machine campaign across all six targets is excluded, and public release/site deployment is prepared but not executed. Cross-builds, local automated integration and a nonpublishing release dry run remain required and passed. These amendments do not imply native Windows/Linux runtime qualification.

One requested validation remains pending: desktop Edge could not be launched because it is not installed/available through this environment's browser tools. The owner was asked whether to connect Edge or explicitly exclude that validation. No answer or additional exclusion is assumed. Chrome and automated accessibility tests passed. Accordingly, this document does not claim every original validation step is complete.

The third PR repair pass confirmed a Unix daemonization-before-sampling defect. The fourth pass replaces sampled ownership with kernel-backed per-check supervision and verifies detached descendants across normal exit, cancellation, replacement and recovery. The original requirement was retained; this was never an owner-approved exclusion. See the fourth-pass evidence and recovery limits below.

## Requirement-to-evidence matrix

Paths in the implementation column are relative to `cmds/async-commit-hook/internal/core` unless otherwise stated. Named Go tests are committed under that directory or `cmds/async-commit-hook/integration`.

| Requirement group | Implementation | Tests and completion evidence | Status |
| --- | --- | --- | --- |
| Runtime, ownership, six targets, English, sole executable | `model.go`, platform process/files adapters, command main, app and client packages | All six CGO-free archives built; only `ach`/`ach.exe` in each; local real-binary integration | Implemented; local/build checks passed |
| Strict v1 configuration and dependency graph | `config.go`, `service.go`, `runner.go` | `TestConfigurationRejectsInvalidGraphs`, no-applicable, optional, dependency and report tests; unknown fields/versions rejected | Passed |
| Trust registration, worktrees and clones | `store.go`, `git.go`, `service.go` | `TestWorktreeIdentityAndSeparateClone`; common-dir identity and per-worktree IDs, separate clones remain separate | Passed |
| Safe hooks and durable receipts | `hooks.go`, CLI submission, `Start` | `TestPostCommitDetachedModesAndWaitExpiry` proves Git returns before a barrier-controlled command in both modes; dedup/idempotency and atomic concurrent initialization tests; native/custom/Lefthook ownership tests | Passed |
| Source isolation, unsupported sources, recursive-hook prevention | `git.go` raw-blob materialization, managed hooks/flag | Committed source survives dirty worktree and branch switching; `TestSourceRefusesSubmoduleAndLFSBeforeCommands`, `TestRawCheckoutDoesNotInvokeSourceFilters`; workspace cleanup assertions | Passed |
| Group parallel/queue/replace and descendants | `runner.go`, process adapters, transactional check claims | FIFO, named groups, detached/reparented-child replacement, durable start barrier, kernel emptiness proof, lost-worker recovery and unrelated-check isolation | Passed on local macOS and Linux container; real six-target qualification excluded |
| Daemon/on-demand parity, startup, drain/force, viewer isolation | `lifecycle.go`, `api.go`, `Work` | Six concurrent starts converge to one daemon; `TestDrainAndForcedStop`, `TestTemporaryViewerExitLeavesWorkerAndHistory`; query-only MCP starts no daemon | Passed |
| Recovery, no replay, storage failures | `RunOne`, process identity snapshots, lifecycle locks | `TestRecoveryInterruptsWithoutReplayAndPIDReuse`, `TestStorageFailureReapsOwnedCommands`; pending requests recover through worker startup | Passed |
| SQLite records, evidence, retention and unavailable source | `store.go`, `query.go`, owned artifact files | `TestPruneCannotResurrectSuccess`, active inherited-evidence retention, dry-run and expired tombstones; source availability is exposed separately from history | Passed |
| Exact final gate and compatible latest acceptance | `service.go` common gate, accepted sequence/fingerprint | Latest queued result defeats prior success; changed declared public input produces no compatible gate; missing/tampered/pruned evidence rejected; no-applicable and optional-only cancellation tested | Passed |
| Required/optional checks and report semantics | `runner.go`, `reports.go` | Optional failure remains visible while required gate passes; required report absence/parse/failure defeats exit zero; JUnit and Go JSON passing/failing/incomplete/malformed fixtures | Passed |
| Failure identity, comparison and reruns | `query.go`, `service.go` rerun closure/inheritance | `TestComparisonAndEnvironmentCompatibility` checks new/continuing/resolved failures and incompatibility; rerun tests preserve predecessor and original successful-evidence provenance | Passed |
| Evidence collection and cleanup diagnostics | `reports.go`, `runner.go` finalization | Root-confined report reads, evidence digest checks, `TestCleanupFailureRemainsDurableAndFailsGate`; evidence remains available after cleanup failure | Passed |
| Inbox, explicit acknowledgement and context recovery | store/application service, guide | Idempotent acknowledgement and dedup test; status/inbox reads do not ack; real MCP ack shares SQLite state and leaves gate unchanged | Passed |
| Full CLI surface, JSON, stable errors and exit statuses | `internal/cli/cli.go` | Real CLI integration for init/run/status/wait/check/daemon/ui/pre-push/update ownership; strict parser and shared service tests; wait expiry returns 4 and execution continues; stdout parses as versioned JSON | Passed |
| Pre-push policies and actual multi-ref tips | `prepush.go`, explicit hook option | `TestPrePushAllTipsUsesExactSHA` covers non-HEAD tips, multiple branches, block/wait, tags/deletions; `TestPrePushRunAndWaitCreatesMissingAttempt` checks actual binary and final SHA gate | Passed |
| Official stdio MCP and three agent adapters | `mcp.go`, `agents.go`, embedded agent guide | All 13 tools discovered through official SDK; run/wait expiry/logs/failures/compare/ack/rerun/cancel workflow; actual Codex/Claude/OpenCode discover/connect in isolated settings; all adapters repeat/preserve conflict and comments | Passed |
| Skill workflow and client preservation | embedded guide, owned settings edits/backups | Receipt retention, independent work, context recovery, inert logs, explicit ack and final commit check included; client install/uninstall and conflict fixtures preserve unrelated content | Passed |
| Web navigation, views and controls | `apps/async-commit-hook/src`, generated query client | Typecheck, component tests, production route/CSP test; actual Chrome pairing/repository/branch/check detail, inert failure/report rendering, ack, cancellation, dialog Escape and focus restoration | Implemented; Chrome passed; Edge pending |
| Local API, pairing and revocation | `api.go`, protocol/client | Expiry/replay/revoke, exact Origin/Host and unauthenticated denial tests; IDs constrain artifact access; cursor filters apply before pagination; bounded reads and version errors | Passed |
| Secret references, redaction and owner-only files | `config.go`, `reports.go`, platform files | Every stream split boundary redaction test; declared-only environment and report redaction; traversal and symlink escape rejected; Unix modes and Windows ACL adapter | Passed locally; Windows adapter cross-built |
| Archives, installers and Homebrew | release builder, public installers, generated formula | Six target builds, archive-entry inspection, SHA256SUMS verification; installer bad signature/bad checksum/success fixtures; four-target Homebrew formula; package-owned self-update refusal | Passed locally/build; native six-target installer campaign excluded |
| Explicit authenticated update, backup and recovery | `update.go`, platform helper | Real Sigstore fixture with wrong identity rejected, forged bundle/tampered archive rejected; archive traversal/duplicates rejected; state backup reopens; atomic replacement and interrupted-journal recovery; active and Homebrew refusal | Passed locally; Windows helper cross-built |
| Manual release, static deployment and CI separation | release workflow, CI job, packaging contract | Actionlint and workflow tests pass; local dry run emits six archives, installers, formula, checksum and unsigned/unpublished compatibility record; no publication command was run | Passed within owner amendment |
| Internal/public docs and repository integration | project/domain contracts, AGENTS, public `/docs`, root dev entry | Docs-first commit; protocol freshness/lint/breaking; app build verifies docs/installers/security headers; root DevHud dev remains unchanged; fixed ach ports 46308/46309 | Passed |
| Actual six-target machine validation | Original acceptance criterion amended by owner | Explicitly not performed; local macOS results and six cross-builds reported separately | Excluded by owner |
| Public release and site deployment | Original delivery criterion amended by owner | No release tag, GitHub Release, tap push, signing credential creation or Pages deployment executed | Excluded by owner |

## Reproducible validation commands

The administrator embedded bundle was generated before the full Go suite. The following checks passed:

```sh
pnpm --filter devhud-admin build:embedded
go test ./...
go test -race ./cmds/async-commit-hook/...
go vet ./cmds/async-commit-hook/...
# From apps/async-commit-hook:
pnpm test
# From the repository root:
pnpm --filter @delinoio/async-commit-hook-api-client test
pnpm proto:check
pnpm ci:contracts
pnpm ci:workflows
node --test scripts/release/async-commit-hook.test.mjs
python3 scripts/release/build-async-commit-hook.py --output <empty-temporary-directory>
```

The local release dry run used `/tmp/ach-release-897-0.1.0`, outside the repository. It produced four `.tar.gz` and two `.zip` archives plus checksums, installers, formula and compatibility metadata. It does not contain newly published Sigstore signatures: signing is the explicitly unexecuted protected release path, while verification is covered with existing signed fixtures and tampering tests.

## Observed local compatibility

| Component | Observed version / verification |
| --- | --- |
| Host | macOS 26.6.2, arm64; does not qualify macOS 13 or the other five targets |
| Shell | `/bin/sh`, GNU bash 3.2.57(1)-release |
| Go | 1.25.8 |
| Node / pnpm | 24.11.0 / 10.26.2 |
| Chrome | 153.0.8010.48; real pairing, controls, inert content and accessibility tree/focus checks |
| Edge | Unavailable; user clarification pending, not an approved exclusion |
| Codex CLI | 0.145.0; owned isolated skill and MCP entry discovered as enabled |
| Claude Code | 2.1.126; isolated installation and MCP connection successful |
| OpenCode | 1.1.53; isolated installation and MCP connection successful |

Agent discovery used actual client executables with temporary HOME/configuration directories; no personal client configuration or credentials were edited. Full tool behavior was exercised through the official Go MCP client, without invoking paid agent-model sessions. Browser verification used a temporary registered repository and local state. No remote results, telemetry or diagnostic uploads were added.

## PR #901 review and CI repair evidence

The second repair pass merged `main` through `da21872e` and preserved both the Runmoor documentation and async-commit-hook CI plans. Product version remains `0.1.0`. Each review problem and independent CI root cause has its own commit.

| Review requirement | Repair and regression evidence |
| --- | --- |
| Renew terminal nonpassing pre-push attempts | `TestPrePushRunAndWaitRepairsTerminalEvidence` covers failed, cancelled, interrupted, expired and missing evidence; queued work is reused. The public pre-push guide matches the shared gate. |
| Idempotent pruning | `TestPruneCannotResurrectSuccess` repeats dry/live pruning without changing the expired tombstone and retries leftover artifact cleanup without restoring older success. |
| Arbitrary log/report bytes over protobuf | `TestEvidenceTextNormalizesInvalidUTF8WithoutChangingByteOffsets` marshals actual API responses containing invalid and split UTF-8 while preserving stored bytes and byte cursors. |
| Reports belong to the current check execution | `TestReportsMustBeProducedAfterTheirOwningCheckStarts`, `TestReportPathsCannotHaveCompetingOwners` and `TestReportPreparationCannotDeleteOutsideWorkspace` reject stale/competing evidence and protect paths outside the workspace. |
| Acknowledge completed results only | `TestAcknowledgementIdempotencyAndAutomaticDedup` rejects active-state acknowledgement and retains a later failure in the inbox; web tests verify disabled active/already-acknowledged controls. |
| Detached branch filtering | `TestDetachedRunFilterAndCursorScope` distinguishes detached, named and unfiltered queries through the API, including pagination and cursor scope. Web request tests cover detached checks and an unfiltered inbox. |
| Cancellation without an active worker | `TestUnstartedCancellationCompletesWithoutWorker` atomically finalizes unowned queued runs, including empty graphs; `TestCancellationDoesNotFinalizeRunOwnedByWorker` preserves worker ownership. |
| Project agent installation from a subdirectory | `TestProjectAgentsResolveNestedAndLinkedWorktreeRoots` covers all three adapters, nested paths, linked worktrees and repeated install/uninstall at the registered root. |
| Retry downstream release publication | CI workflow tests require separate release, Homebrew and Pages jobs. A real temporary Git remote verifies repeated Homebrew publication preserves the existing commit. Failed jobs can be retried within the same run's artifact-retention window without recreating its successful immutable GitHub release. |

CI repair evidence:

- Windows environment/redaction fixtures now use native PowerShell syntax and explicitly assert undeclared environment exclusion. The regression passes locally and its Windows test binary cross-compiles; Windows execution is left to hosted CI.
- Release fixtures use Node built-ins and run without workspace dependencies. YAML workflow checks moved to the dependency-installed CI contract suite. An isolated dependency-free fixture run passed, and all 154 release tests passed in a local Linux container with read-only source/Git mounts and networking disabled.
- The shared API/sweeper Docker builder now uses Go `1.25.8`, matching `go.mod`; a CI contract prevents future drift. Both prior OCI failures had rejected Go `1.25.7` before compilation. The local multi-architecture OCI build was stopped during slow base-image downloads, so a completed local OCI build is not claimed.

After the repairs, `go test -p 1 ./...`, async-commit-hook unit/integration race tests, the full Runmoor race suite, `go vet ./...`, frontend `pnpm test` (13 component tests plus typecheck/production/static checks), client tests, protocol lint/breaking/freshness, 30 CI contracts, workflow lint and six `0.1.0` archive cross-builds passed. The release dry run wrote only local artifacts under `/tmp/ach-pr901-review-repairs-release` and performed no publication.

Initial root Go runs exposed existing Runmoor shell-startup timing failures. Its guest-validation correctness fixture now allows 15 seconds while the dedicated transport timeout remains 50 ms; focused ordinary/race tests and the final serial root suite passed. The independent runner-startup fixture also failed intermittently before that successful root run; its production startup semantics were not changed.

The two owner-approved completion amendments and the pending Edge validation above remain unchanged. These local checks do not claim that the subsequent hosted CI run has completed.

## Third PR #901 repair pass

The incoming head `3d71383e` had no merge conflicts or failing hosted checks; the prior Windows, release-fixture and OCI failures passed in CI run `35425813089`. Twelve new bot findings were inspected. Eleven were repaired in separate commits; Unix process ownership was still unresolved at the end of that pass. The fourth pass below supersedes that status.

| Repaired finding | Regression evidence |
| --- | --- |
| UTF-8 evidence page boundaries | `TestEvidencePagesPreserveRunesAndIncompleteLiveTail` and the API serialization fixture cover small/default limits, Korean/emoji, invalid bytes and later completion of a live partial rune. |
| Git diff transport bytes | `TestChangesNormalizesRawGitBytesAfterTruncation` covers invalid content/path bytes and a valid rune split by the 2 MiB byte cap; Connect JSON/protobuf fields remain valid. |
| Automatic context deduplication | `TestAutomaticDeduplicationIncludesExecutionContext` verifies public input changes create a fresh compatible attempt, secret value changes do not, and explicit submissions remain distinct. |
| Current worktree branch | `TestRepositoryListingObservesCurrentWorktreeBranch` switches/deletes branches, detaches HEAD and preserves historical branch labels. |
| Raw source streaming | `TestWorkspaceStreamsManyAndLargeRawBlobsInOneBatch` instruments real Git, verifies exactly one batch for 256 files and a large binary blob, and checks exact content, symlinks and executable permissions. Existing filter/LFS/submodule tests still pass. |
| Unstarted preparation recovery | `TestRecoveryResumesOnlyEntirelyUnclaimedRuns` resumes preparing/running requests with untouched checks, discards partial workspaces and interrupts claimed checks without replay. |
| Structured failure bounds | `TestLargeJUnitSummariesStayReadableWithinRunBudget` collects a report larger than 8 MiB, caps summaries across two checks, serializes both detail/failure responses under the transport cap and retrieves full report-tail evidence. |
| Reused checkout paths | `TestExplicitRegistrationAfterCheckoutPathReusePreservesHistory` covers identical/different common-directory paths, explicit trust, stale API IDs and preserved history. `TestLegacyPathRegistryUpgradePreservesIDsAndRequiresExplicitTrust` verifies migration and foreign-key enforcement. |
| Persistence failure propagation | `TestCheckPersistenceFailureReleasesWorkerForReconciliation` injects SQLite failures for running/collecting/passed writes, checks worker lock release and confirms restart interruption. Permanent-storage-failure tests still pass. |
| Windows helper cleanup | `TestUpdateHelperCleanupSurvivesSuccessfulJournalRemoval` preserves live/changed helpers, retains retry metadata and removes authenticated exited helpers on a later open. Windows executable targets cross-compiled; this pass did not execute Windows locally. |
| Terminal detail polling | The frontend timer test observes running-to-passed polling stop and later explicit invalidation. A manual refresh action remains available. |

Passed: `go test ./cmds/async-commit-hook/...`, `go test -race ./cmds/async-commit-hook/...`, `go test -p 1 ./...`, `go vet ./...`, package-local frontend `pnpm test` (14 component tests plus typecheck/build/static checks), root `pnpm test` (all 12 selected Turbo tasks), protocol lint/breaking/freshness, 30 CI contracts, workflow lint and six unsigned `0.1.0` release archive builds under `/tmp/ach-pr901-repair3-release`. Generated repository-owned `dist` output is removed before delivery. No signing, release, tap update or deployment was performed.

The separate temporary Unix daemonization reproduction intentionally failed with `daemonized descendant survived successful ownership reconciliation`. It ran through a Go overlay without adding a passing or skipped test that would imply this defect is fixed, and explicitly killed the reproduced child on cleanup. Passing committed tests above therefore do not establish the missing Unix lifetime guarantee. The next required work is a supported process-ownership backend and proof of descendant termination across reparenting, cancellation, replacement and recovery, including macOS 13; the supported OS range and original requirement were not relaxed. Edge verification remains pending as before.


## Fourth PR #901 repair pass

The only incoming unresolved bot thread was `PRRT_kwDORRAKg86j9n3x`. The replacement runs each Unix check inside a same-binary supervisor whose ownership is established before the command can run. Linux uses subreaper adoption and an `ECHILD` emptiness proof. macOS uses a temporary background launchd job, a dedicated resource coalition, and kernel active-task accounting. The initial coalition experiment explicitly showed that `bootout` alone leaves a double-forked child alive; production cleanup therefore also drains and verifies the coalition.

Regression evidence:

- `TestScopeReapsDaemonizedDescendants`: same-binary fixtures create multiple process generations, new sessions, cleared environments and a changed working directory. No ownership sample is taken. Normal root exit, cancellation, worker socket loss and explicit recovery all remove the detached leaf.
- `TestReplaceReapsDaemonizedDescendantsBeforeNextStarts`: the next exclusive attempt cannot start while the prior detached leaf remains alive.
- `TestScopeCancellationDoesNotTouchAnotherCheck`: cancelling one scope preserves the other scope's detached leaf.
- `TestScopeStartBarrierAndOutput`: the start barrier, output transport and original nonzero command exit status remain observable.
- `TestScopeJournalNeverStoresResolvedEnvironment`: neither launchd plist nor ownership journal contains a resolved credential fixture.
- `TestScopeMissingJournalCannotConfirmCompletion` and `TestLegacyScopeCannotClaimUnknownDescendantsExited`: unavailable ownership proof cannot release a scheduling claim.
- `TestScopeRecoveryAfterSupervisorDeath` (macOS): recovery kills the supervisor's surviving descendants and verifies zero active coalition tasks.
- `TestScopeLostSubreaperFailsClosedUntilBootChanges` (Linux): a dead/reused supervisor identity without completion proof blocks recovery; a different kernel boot ID proves the old processes cannot remain.
- Existing FIFO, storage-failure cleanup and check-persistence-failure tests pass. Saving the preparing scope never returns a claimed check to queued state or replays it after a failed running-state write.

Executed successfully: `go test -p 1 ./...`, `go vet ./...`, the async-commit-hook Go suite and full race suite, focused lifecycle race tests, app-directory `pnpm test` (14 component tests, typecheck and production/static checks), and focused native Linux arm64 tests in a network-disabled `node:24-bookworm` container. The macOS supervisor-death regression passed on the local macOS 26.6.2 arm64 host. Six unsigned `0.1.0` target archives also built. These are local/container/build evidence, not macOS 13 or six-machine qualification. No elevated daemon, Endpoint Security entitlement, signing or publication was introduced. Edge validation remains pending as before.

A Linux supervisor that is itself forcibly killed before recording completion loses subreaper ownership. This case deliberately retains the group claim and requires a host reboot before recovery can independently prove the old tasks are gone; it never reports successful cancellation from an empty PID sample. Legacy unfinished sampled records similarly require explicit reconciliation. Normal worker death retains the independent supervisor, which drains descendants and allows interrupted recovery.


### Fourth-pass Windows CI repair

While local ownership repairs were running, incoming-head CI run `35427897838` finished with Windows Go failures in the collecting/passed variants of `TestCheckPersistenceFailureReleasesWorkerForReconciliation`. Native PowerShell startup exceeded that fixture's 15-second context budget; surrounding successful checks took up to about 25 seconds. The collecting case returned the intended injected SQLite error after its watchdog expired, while the passed case was cancelled before reaching the target write. No product timeout or storage logic caused this failure.

The separate CI repair gives this correctness fixture a two-minute deadlock watchdog and requires the returned error to contain the injected SQLite failure, preserving proof that the intended branch executed. It does not add a command runtime timeout or relax the storage, lock-release, process-cleanup and interruption assertions. Focused local ordinary/race execution and Windows test-binary cross-compilation validate the edit; native Windows execution remains hosted CI evidence, not a claimed local result.


### Independent supervisor lifecycle integration

Final integration inspection found that an independently surviving supervisor also needs an account lifecycle lease. The follow-up repair registers it before the start barrier under the existing account lock. Worker disappearance cannot make active commands invisible to mode/port/state changes or self-update. If the supervisor itself dies, backend or changed-boot proof remains required; a dead owner PID alone does not remove its lease. Retention clears proven inactive leases before deleting their ownership journals.

`TestSupervisorLeaseBlocksConfigurationWithoutWorker` covers rejection of configuration changes and updates with only a supervisor registered, later lease reclamation, and safe journal pruning. The macOS supervisor-death test now asserts that living coalition descendants retain that lease; the Linux lost-subreaper test asserts the same conservative behavior without a completion record. Focused race tests passed for these cases, detached replacement, storage failures and retention.


After lifecycle integration, the full async-commit-hook race suite, `go test -p 1 ./...`, `go vet ./...`, and focused Linux arm64 container lifecycle/retention tests passed again. The final six-target unsigned archive build uses `/tmp/ach-pr901-repair4-complete-release`; product version remains `0.1.0`. Public release, signing, Homebrew publication and deployment remain unexecuted. Generated `dist` directories are removed before delivery.

Four additional incoming-head reviews were first visible in the final inventory. They are handled without another status poll. `TestEnvironmentSecrecyConflictsRejectedBeforeAcceptance` rejects cross-check secret/public conflicts, including case variants, preserves consistent shared declarations and proves no run is accepted into SQLite.

`TestCommitSubjectsNormalizeDisplayWithoutChangingObjectIDs` writes a real Git commit with invalid UTF-8, verifies raw Git still returns those bytes, and marshals the actual ListCommits protobuf/JSON response while preserving both object IDs and valid Korean/emoji text.
