# Manual PR fix implementation evidence

Recorded on 2026-09-30 for issue #1081. The implementation starts from
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7` and is retained in commits
`7508c2b92ad68b5bc830d070b3427596a6182d41`,
`557d6cbd4` and `d67b34ceb031064d010df37dd844275c76d43c8d`.
The owner explicitly selected the issue's rebase default; existing explicit
merge policies retain their meaning.

## Implemented boundary

Manual Fix now, authenticated Connect RPC and equivalent CLI accept exact
original retained evidence revisions/content versions with an explicit project.
Acceptance atomically binds one stable-PR owner, eligible linked session or
explicitly configured complete PR-head workspace, original input and job.
Dispatch independently refreshes source/gates. The Codex API profile requires
explicit write permission and exposes a typed capability; other harnesses fail
without fallback. The Worker retains its native Git identity separately from
provider credentials, and the harness invokes a bounded, assignment-bound Git
bridge to edit, commit and push. Independent original remote/local/cleanup proof
is required before exact evidence versions become handled. A native success
without a push leaves them unhandled; uncertainty retains ownership.

The extension adds a service and optional historical fields without a destructive
schema migration. Scoped AGENTS and integration, workspace, desktop, protocol,
client and normative default contracts changed with the implementation.

## Executed checks

All commands below ran in the isolated issue checkout unless stated otherwise.
Fixtures use temporary state, test-owned provider responses/native scripts and
local Git repositories. They do not use user GitHub/provider credentials.

| Command | Observed result |
| --- | --- |
| `go test -p 1 ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/store -run 'TestPRFix\|TestManualPRFix' -count=1` | Passed all three packages. Covers canonical revisions, atomic acceptance/receipt replay, initial concurrent identical requests, PR ownership, retained review and approved-review comments, conflict selection, paused-session exclusion, explicit missing configuration, complete companion/primary workspace selection, and verified/unchanged/uncertain or mismatched completion proof. |
| `go test -p 1 ./cmds/delidev-cli/internal/cli -run 'TestPRFix' -count=1` | Passed six CLI receipt, source, request and capability cases. |
| `go test -p 1 ./cmds/delidev-cli/internal/workspace -run '^TestPRGitToolForkPush' -count=1 -timeout=8m` | Passed (151.425 seconds). Actual Git push to the test-owned fork, original-source proof, missing write access, changed remote head, push-only destination rewrite, no-push success, exact lease argument restriction and replay rejection. Earlier runs hit the bounded preparation timeout; no production deadline was relaxed. |
| `go test -p 1 ./cmds/delidev-cli/internal/workspace -run '^TestPRGitPushOnlyRewrite' -count=1` | Passed. Real Git configuration read rejects a matching `pushInsteadOf` and permits an unrelated rewrite. |
| `go vet -p 1 ./cmds/delidev-cli/...` | Passed, including the final repeat with added CLI/domain fixtures. |
| `pnpm proto:check` | Passed formatting, lint, pointer-only baseline breaking check and forced generation without drift. |
| `pnpm exec vitest run tests/legacy-imports.test.ts` in the API-client package | Passed both compatibility/reflection cases. |
| `pnpm typecheck` in `apps/delidev` | Passed for the final frontend source. |
| `pnpm exec vitest run src/pr-fix.test.tsx src/pr-problems.test.tsx` | Passed 12 cases across the two files, including exact decimal revisions, typed capability gating, retained uncertain requests across navigation and malformed acknowledgment after unmount. |
| `pnpm exec vitest run src/pr-remediation-history.test.tsx --maxWorkers=1 --no-file-parallelism` | Passed all seven existing history cases. |
| `pnpm test:bundle-dry-run`, `pnpm test:desktop-launch`, `pnpm test:widget`, `pnpm build` in `apps/delidev` | Passed separately after the main test script stopped at Vitest: eight bundle fixtures, sixteen desktop/asset fixtures, widget checks and production build. |

## Full-suite qualifications

The required `pnpm test` ran in `apps/delidev`. API-client generation and
frontend typecheck passed. Vitest reported 957 passing and seven failing tests
across 85 files: three existing App five-second timeouts and four settings
integration DOM-query failures. The subsequent packaging/build commands did not
run through this failed script; their separate successful execution is recorded
above. A serial rerun, `pnpm exec vitest run --maxWorkers=1
--no-file-parallelism`, passed 961 of 964 tests (84 of 85 files); only three
existing App five-second timeouts remained. These runs are failures, not a green
full frontend suite.

A control run of `src/App.test.tsx` in the primary checkout, without applying
this implementation, passed 33 of 37 tests and reproduced four five-second App
timeouts. The observed primary revision after that run was
`545b40804a918505ef9387d6658ff36e850667b1`. Its existing AGENTS edit and deleted
icon were preserved. This control supports an existing timing limitation; it
does not prove every full-suite failure has the same cause.

The required Go command, `go test -race -p 1 -timeout=20m
./cmds/delidev-cli/...`, is still running at this record's initial publication.
It has already failed the existing CLI acceptance fixture and native Grok,
OpenCode and Claude probe/stream cleanup fixtures. No race detector finding has
been observed so far. A separate unchanged-primary-checkout control of
`go test -race -p 1 ./cmds/delidev-cli/internal/harness -run
'^TestDiscoveryVerifies(Grok|OpenCode)WithoutExecution$'` reproduced a Grok
probe timeout (OpenCode passed that control run). Remaining results must be
recorded when the original suite exits; this is not a full Go pass.

## Limits and generated output

No real provider account, actual inference-driven PR remediation, Windows/Linux
native execution, release signing or packaged platform acceptance was performed.
The fixture verifies actual local Git transport and closed conflict/lease
operands, not a live GitHub rebase conflict or every supported native harness.
Other harness fix profiles and automatic remediation remain unavailable.
Repository Rust source was not modified, so root Cargo testing was not required.

The required desktop icon LFS object was hydrated before consuming it. The
API-client and frontend `dist` output was explicitly generated for consumers and
removed after checks; generated output and private logs are not tracked.

## Current-main integration repair

The PR base advanced to `9d110ced702e66bb50974c5ec98e830b86adbe5b` after
publication. A normal merge preserved both new Activity contracts and manual-fix
contracts. Manual completion now retains the upstream dedicated Activity proof
in the same transaction as its exact handled versions, only after original
native, cleanup and push verification. Dismissal and unverified outcomes cannot
create this record. `go test -race -p 1 ./cmds/delidev-cli/internal/store
-run 'TestPRFix|TestPRActivity' -count=1` passed after the merge; new assertions
check the exact original proof references and absence for every unverified or
dismissed scenario. This controlled evidence is not real native/account
acceptance.

## Initial identical-request repair

A later focused race run reproduced two initial identical requests colliding in
the existing exclusive provider inspection before receipt publication. The
plain earlier fixture pass did not establish this concurrent boundary. Exact
actor/input request coalescing now occurs before provider reads; waiters re-read
the durable receipt. Ownership is cancellable and bounded to 64 original
requests, while accepted receipt replay bypasses that capacity. Different actors
or input cannot share an original request. This gate retains no native grant.

`go test -race -p 1 ./cmds/delidev-cli/internal/server
-run 'TestPRFix|TestManualPRFix' -count=5` passed (17.691 seconds), including all
three retained problem scenarios and the new exact/capacity/cancellation cases.
The fix is retained separately from the base-merge repair.

## Merged CI and frontend validation

`go test -race -p 1 ./cmds/delidev-cli/internal/server
-run '^TestManualPRFixRPCExactAcceptanceReplayAndPausedExclusion/ci$'
-count=3` passed (8.888 seconds). The additional required-CI scenario exercises
retained exact result versions, known test-merge state, fresh CI/detail source
binding, fork identity and concurrent identical acceptance. An incomplete
fixture initially produced no retained trigger, consistently with the existing
unknown-CI gate; the completed observation is explicit, not a production bypass.

After the base merge, `pnpm proto:check` and `go vet -p 1
./cmds/delidev-cli/...` passed. API-client `dist` was regenerated for consumers;
frontend typecheck and a serial run of `pr-fix`, `pr-problems`,
`pr-remediation-history`, `activity` and `pr-workflow` Vitest files passed all
24 tests in five files. The generated client output was removed afterward.

The original broad race suite remains running as of this repair's publication.
It has additionally reported existing Codex approval/continuation/interaction
fixture failures. These are not a green native suite, and the unchanged-primary
control established only the specifically recorded App/Grok timing failures.
The separately repeated manual-fix race fixtures pass after the coalescing
repair. No Go data-race warning has been observed in the retained logs so far.

## Review repair: separately owned Git authentication

The first review correctly identified that reversible prefixed native lookup
variables let a shell-capable harness bypass the client bridge. Native
configuration/authentication now remains in the independently owned Worker
process. The harness receives only an authenticated closed loopback capability.
The Worker pins original scope/configuration and executable bytes, isolates local
commands from native authentication, retains the explicit native commit identity,
authenticates push claims with a private memory-only key, and cancels/joins the
bounded bridge before proof. Restart cannot reconstruct a lost capability/key.

`go test -p 1 ./cmds/delidev-cli/internal/workspace
-run '^TestPRGitToolForkPush' -count=1 -timeout=10m` passed (98.100 seconds) after
the ownership change. It additionally rejects mutable scope substitution,
forged push claims, command replay after closure and native lookup variables in
the harness environment. `go vet -p 1 ./cmds/delidev-cli/internal/workspace
./cmds/delidev-cli/internal/worker` passed. These are isolated real-Git fixtures,
not a claim of OS isolation for an explicitly full-access harness or live account
acceptance.

The initial broad race run crossed a base merge while its package inputs were
already snapshotted. Its later Activity-related build failures are therefore a
mixed-source validation limitation, not evidence about the merged source build.
It also timed out in the existing Grok package. A fresh immutable-source broad
run is required after the review and dispatch repairs finish; no full Go pass is
claimed from the initial run.

## Review repair: noninteractive rebase continuation

The allowed resolved-conflict `rebase --continue` now selects a fixed no-op
editor, retaining the original commit message without inheriting a harness
editor. `go test -p 1 ./cmds/delidev-cli/internal/workspace
-run '^TestPRGitToolRebaseConflictContinuesWithoutHarnessEditor$'
-count=1 -timeout=10m` passed. The real isolated Git fixture creates an actual
conflicting index, resolves it, continues with a dumb terminal and no editor,
publishes through the exact original-head lease, and independently verifies
that new fork head. This POSIX transport fixture does not establish Windows or
live-account acceptance.

The initial broad race command has now completed unsuccessfully. Alongside the
previously recorded native fixture failures and Grok timeout, its workspace
compile also crossed the bridge-source edit. Both later compile failures are
mixed-source limitations; this run cannot establish a full merged-source result.

## Review repair: Worker clock skew

Manual completion now uses the retained server-observed assignment/report
ordering and independent cleanup barriers, without comparing Worker wall time
to server attempt time. Proof identity, exact selection and outcome validation
remain mandatory; handling audit uses server transaction time. Verified proof
with Worker clocks 24 hours ahead or behind handles exactly once and releases
its original owner, while foreign/missing/uncertain proof remains gated.

`go test -race -p 1 ./cmds/delidev-cli/internal/store
-run 'TestPRFix|TestPRRemediation.*Finish|TestPRRemediation.*Completion'
-count=3` passed, including the unchanged existing server-report time and
original assignment contradiction cases. These synthetic retained-state
fixtures establish ordering policy, not live remote publication.

## CI repair: private dispatch actor context

Current-head macOS/Linux job logs from Actions run `36687252519` each showed
18 top-level dispatch fixture failures caused by the new retained PR-history
read lacking owner context. The private coordinator now establishes its own
server-owner context; public RPC/store actor checks remain separate.

A race run covering the affected initial/continuation/budget/harness/workspace
and PR cases removed every original permission error but failed the existing
claimed-work fixture: the optional PR preflight returned `missing-input` before
the ordinary lifecycle conflict. That distinct regression is repaired separately.
The focused `TestInitialDispatchOffProviderPreservesQueuedInputAndRouting`,
`TestManualPRFixRPCExactAcceptanceReplayAndPausedExclusion` and
`TestWorkspaceReadRelayWhileExecutionRemainsClaimed` race run passed, including
public Worker mutation denial. Full CLI Go vet also passed after the review
repairs and owner-context change.

## CI repair: ordinary dispatch error precedence

The optional PR preflight now skips only its absent queued-input candidate,
letting the ordinary claim transaction validate lifecycle and report its
original error. Claimed work still returns conflict and cannot redispatch;
explicit continuation and newly queued manual PR input preserve their ownership.

`go test -race -p 1 ./cmds/delidev-cli/internal/server
-run '^(TestInitialDispatchAtomicConfigurationRollbackAndCurrentReceipt|TestContinuationExplicitResumeWithQueuedAndFutureInput|TestManualPRFixRPCExactAcceptanceReplayAndPausedExclusion)$'
-count=1` passed (16.285 seconds). No production lifecycle gate was relaxed.
The current broad result must come from an immutable committed-source snapshot,
separate from the earlier mixed-source failure.

## Immutable-source final broad race result

`go test -race ./cmds/delidev-cli/...` ran from an archive of committed source
`c9896e1a589dce24a452eb1ecc02dd9e6daf9e29`, from 08:32:47 to 08:43:22 UTC on
2026-09-30, and exited 1. No tracked source changed in that snapshot. Seventeen
tested packages passed, five failed, and two had no tests. Store (569.950s),
Worker (471.061s), Codex (264.049s), OpenCode (98.579s), domain, provider and
harness discovery packages passed. No race-detector warning was observed.

The failures remain unresolved local full-suite evidence:

- CLI session acceptance reported an unavailable fixture workspace file reader.
- Claude oversized-stream/flood cases did not confirm native scope cleanup.
- Grok reached the default ten-minute package timeout during original initial
  plan mode/acknowledgment cases.
- Server reached that package timeout during the Doctor exact-reference fixture.
- Workspace reported a timed-out unborn diff read and then reached the package
  timeout while the new rebase conflict fixture was running. This broad run
  therefore does not establish completion of that final fixture under race.

These are recorded outcomes, not a green full suite or a blanket baseline-cause
claim. The earlier unchanged-primary control proves only its specific App/Grok
observations. Isolated manual Git race verification is recorded separately below.
`go vet ./cmds/delidev-cli/...` passed after both dispatch repairs.

## Isolated final Git race result and latest-base integration

`go test -race ./cmds/delidev-cli/internal/workspace -run '^TestPRGit'
-count=1 -timeout=10m` passed (169.023s) against the same immutable
`c9896e1a589dce24a452eb1ecc02dd9e6daf9e29` source. All manual Git bridge fixtures
completed under race, including original fork proof/replay/tamper, exact push
rewrite rejection and real noninteractive conflict/rebase/lease publication.
The broad package timeout cannot be presented as this passing isolated result.

GitHub's final snapshot showed a newer base conflict. A normal merge of
`b1b3e9e7c55511086a284021850426d48484b127` preserves the new Claude failed-resume
rules alongside manual PR rules in all three scoped instruction conflicts. No
Rust source resolution was needed. Current-head Windows job `109796174896`
reported the same private dispatch owner-context regression, with no independent
failing package in its log.

After that merge, the race command covering `TestPRFix`, `TestManualPRFix`,
`TestInitialDispatchAtomicConfigurationRollbackAndCurrentReceipt` and
`TestInitialDispatchOffProviderPreservesQueuedInputAndRouting` passed in domain,
server (12.410s) and store (9.829s). `go vet ./cmds/delidev-cli/...` passed on the
merged source. Required frontend validation runs separately on that merged tree;
the immutable broad result above remains bound to its recorded source revision.
