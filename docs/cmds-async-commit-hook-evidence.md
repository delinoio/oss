# async-commit-hook implementation evidence

The complete [issue requirements](cmds-async-commit-hook-requirements.md) remain intact. This matrix was created before runtime implementation and maps them to the delivered source and executable evidence. Verification below was performed on September 19, 2026.

The initial product version is `0.1.0` across the executable, app/client packages, installers and release metadata. Configuration, state, CLI JSON and API schema versions remain `1`. The `0.1.0` artifacts passed the full Go suite, frontend tests/build, release/workflow checks and all six target builds; native `ach version --json` reports `0.1.0`.

## Completion boundary

The owner approved exactly two changes to the original acceptance criteria: the real-machine campaign across all six targets is excluded, and public release/site deployment is prepared but not executed. Cross-builds, local automated integration and a nonpublishing release dry run remain required and passed. These amendments do not imply native Windows/Linux runtime qualification.

One requested validation remains pending: desktop Edge could not be launched because it is not installed/available through this environment's browser tools. The owner was asked whether to connect Edge or explicitly exclude that validation. No answer or additional exclusion is assumed. Chrome and automated accessibility tests passed. Accordingly, this document does not claim every original validation step is complete.

## Requirement-to-evidence matrix

Paths in the implementation column are relative to `cmds/async-commit-hook/internal/core` unless otherwise stated. Named Go tests are committed under that directory or `cmds/async-commit-hook/integration`.

| Requirement group | Implementation | Tests and completion evidence | Status |
| --- | --- | --- | --- |
| Runtime, ownership, six targets, English, sole executable | `model.go`, platform process/files adapters, command main, app and client packages | All six CGO-free archives built; only `ach`/`ach.exe` in each; local real-binary integration | Implemented; local/build checks passed |
| Strict v1 configuration and dependency graph | `config.go`, `service.go`, `runner.go` | `TestConfigurationRejectsInvalidGraphs`, no-applicable, optional, dependency and report tests; unknown fields/versions rejected | Passed |
| Trust registration, worktrees and clones | `store.go`, `git.go`, `service.go` | `TestWorktreeIdentityAndSeparateClone`; common-dir identity and per-worktree IDs, separate clones remain separate | Passed |
| Safe hooks and durable receipts | `hooks.go`, CLI submission, `Start` | `TestPostCommitDetachedModesAndWaitExpiry` proves Git returns before a barrier-controlled command in both modes; dedup/idempotency and atomic concurrent initialization tests; native/custom/Lefthook ownership tests | Passed |
| Source isolation, unsupported sources, recursive-hook prevention | `git.go` raw-blob materialization, managed hooks/flag | Committed source survives dirty worktree and branch switching; `TestSourceRefusesSubmoduleAndLFSBeforeCommands`, `TestRawCheckoutDoesNotInvokeSourceFilters`; workspace cleanup assertions | Passed |
| Group parallel/queue/replace and descendants | `runner.go`, process adapters, transactional check claims | FIFO across independent workers; named groups across repositories; replacement reaps child before new start; PID birth mismatch does not signal unrelated process | Passed locally; all adapters cross-built |
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
