# Issue #1079: PR #1231 budgets, accounting and capacity repairs

This independent record preserves the earlier failures, isolated reruns and source
revision distinctions in `current-main-workspace-storage.md`,
`source-46452512-completed-race.md` and `pr-1231-review-repairs.md`.
It does not rewrite the historical ledger.

## Reconciliation and repaired source

The initial repair inventory on 2026-09-30 found PR #1231 open and conflicting
at `461cb6abab47dce4aaccd8668a461ee0d2761987`, with no failing checks among
37 checks. Pending checks, if any, are not claimed passed by that inventory.
Main was merged without rebasing in
`57887aaa46e1b40e73a5f5211a49030f7f866ba3`; the independent reconciliation
record is `pr-1231-main-98df29c4.md`.

Each review problem has its own commit:

- `a3615c13b1fc97d72cd1a78652600566fc71493a` shares a single remaining
  copy budget across workspace files and every independently copied Git store.
  Git roots count once and overlay directories count only when newly created.
  Excess files are rejected before destination creation, and a growing file's
  extra probe bytes cannot be written past its reservation. Capture conservatively
  reserves 8 MiB manifest headroom and 256 config-rewrite bytes per repository;
  the owning storage contract explicitly retains that near-limit admission
  qualification. This addresses [aggregate copying](https://github.com/delinoio/oss/pull/1231#discussion_r4144674918).
- `3ee2f100793886879b7ad9657dd089e054ce75a1` reports the verified snapshot's
  pinned source size and the session's post-action retained inventory for successful
  inspect, restore and delete. Other sessions are excluded; deleting a retained
  snapshot does not count as removed live-source bytes. Failure to obtain the
  inventory after a native effect retains recovery-required uncertainty. This
  addresses [read/removal accounting](https://github.com/delinoio/oss/pull/1231#discussion_r4144674931).
- `3530d3d9d51c20622b113b52f4a4dad479de4043` holds one Worker-wide,
  cross-process publication gate from capacity admission through durable
  publication. Create/cleanup reject at 4,096 before staging; a concurrent publisher
  receives conflict without output or source changes. Preview remains usable at
  capacity and deletion releases a slot. This addresses [capacity admission](https://github.com/delinoio/oss/pull/1231#discussion_r4144674940).

## Focused verification

Go checks ran at the repository module root on macOS arm64 with Go 1.26.8
and `GOMAXPROCS=2`, using isolated temporary repositories/state only.

- The first `go test -race -p 2 -timeout=15m
  ./cmds/delidev-cli/internal/workspace
  -run 'SnapshotCopy|SnapshotCreateStopsAtAggregate|WorkspaceSnapshotFaithfullyRestoresEveryRepository|SnapshotMaximumInventoryRemainsDeletable'
  -count=1` failed (234.485s) solely in the new aggregate-entry regression's
  measurement setup at `snapshot_budget_test.go:70`: its temporary target used
  macOS's noncanonical temporary-directory alias, so native Git-location validation
  returned recovery-required. The fixture was corrected with `EvalSymlinks`,
  matching existing Git-copy fixtures. The faithful multi-repository restoration
  and maximum-inventory deletion cases reported no failure in that run.
- After that fixture correction, `go test -race -p 2 -timeout=15m
  ./cmds/delidev-cli/internal/workspace
  -run 'SnapshotCopy|SnapshotCreateStopsAtAggregate' -count=1` passed (127.428s).
  Tests cover a byte allowance shared by separate source trees, reused overlay
  directories and native two-repository capture reaching the complete entry bound
  before the second Git store. Rejected capture preserves sources and removes
  unpublished scratch without publishing metadata.
- `go test -race -p 2 -timeout=10m ./cmds/delidev-cli/internal/workspace
  -run '^TestSnapshotReadRestoreAndDeleteAccounting$' -count=1` passed (1.918s).
  It checks pinned source bytes after live data changes, multiple retained copies,
  another session's exclusion, cleanup/restore, partial inventory deletion and
  the final zero-retained transition while restored live files remain.
- `go test -race -p 2 -timeout=15m ./cmds/delidev-cli/internal/workspace
  -run '^TestSnapshotCapacity' -count=1` passed (45.523s). The fixture seeds
  bounded metadata headers from a verified native snapshot to exercise 4,096-slot
  admission; it does not claim thousands of fully copied native snapshots. The
  concurrent test uses independent Manager instances and session locks in one
  Worker root, blocks one native repository copy after admission, rejects a second
  publisher, and verifies deletion plus replacement publication at capacity.
- After adding failure-path goroutine joining to that test fixture, its exact
  isolated race rerun passed (31.526s), with production source unchanged.
- `go vet -p 2 ./cmds/delidev-cli/...` and
  `go build -p 2 -o <temporary-cli-binary> ./cmds/delidev-cli` passed.
- `GOOS=windows GOARCH=amd64 go test -c -o <temporary-test-binary>
  ./cmds/delidev-cli/internal/workspace` passed. This compiles source-3530d3d9d and its
  regressions; the later config guard is cross-compiled separately below. It is
  not native Windows execution or acceptance.

Merged frontend/protocol verification passed: API-client 44 tests plus build;
root proto generation/lint/breaking and forced freshness; desktop `pnpm test`
with 1,275 component tests, eight bundle/package tests, sixteen launch/asset tests,
widget checks and production build. The LFS-backed desktop icon was hydrated.
No native CEF launch or release is inferred from those scripted fixtures.

## Full repaired-source race

The command runs against source `3530d3d9d51c20622b113b52f4a4dad479de4043`:

```sh
GOMAXPROCS=2 go test -race -p 2 -timeout=30m -count=1 ./cmds/delidev-cli/...
```

The full command completed with exit status 1. Workspace passed its complete
suite (1003.889s), including the three review regressions; Worker passed (413.507s),
store passed (662.947s), and CLI passed (156.493s). All other packages passed except
Grok and server. Production/test source remained unchanged throughout this run.

Grok reached its 30-minute package deadline (1800.679s) while
`TestCreationRetainsOriginalClaimsAndNeverRepeatsUncertainty/setup-foreign` was
running. Its earlier reported failures are preserved below. They include native
initialization/operation timeouts and unexpected initialization classifications.
The log also reports three data races at the same fixture log-buffer boundary:
`probe_test.go:230` reads `bytes.Buffer.String` while the owned-process final
logger writes it from `process.Start`. These warnings are actual race evidence,
independent of the initialization failures and cumulative timeout.

Server reached its 30-minute package deadline (1801.728s) while
`TestAccountSwitchPairedClientReplayRechecksRevocation` had been running for two
seconds. It also reported `TestScheduleCoordinatorMissedHistoryVisitIsBounded`
(11.85s) with unavailable/operation-timed-out. Neither package completed its
entire test inventory; no full-suite success is claimed.

Reported failed cases (the timeout-interrupted cases above are separate):

| Test | Reported subcases |
| --- | --- |
| `TestTextClosureRequiresOriginalSummaryAcknowledgmentAndRemoval` | `closure-bind-failure` |
| `TestTextClosureClaimCancelsWithNativeLifetime` | No subcase reported |
| `TestOriginalFileReplyClaimsAndTerminalFaults` | `write-valid`, `write-reject`, `write-claim-failure`, `write-publication-failure`, `write-unclaimed-resolution` |
| `TestInputPublicationJoinsNativeClosure` | No subcase reported |
| `TestInstructionChangeBeforeNativeInputRefusesClaim` | No subcase reported |
| `TestInitialPlanRequiresOriginalClaimAckAndMode` | `mode-valid`, `mode-rpc-first`, `mode-command-prefix`, `mode-missing-event`, `mode-missing-rpc`, `mode-native-error`, `mode-result-extension`, `mode-result-null` |
| `TestProbeOwnsBoundedInspectedInitialization` | `valid`, `foreign`, `duplicate`, `trailing`, `request`, `notification`, `oversize`, `stderr`, `inventory-first`, `inventory-duplicate`, `inventory-nonempty`, `inventory-missing`, `inspect-managed` |
| `TestReadInputOwnsToolsResponsesAndNoPlainTextHistory` | `read-lost-rpc` |
| `TestScheduleCoordinatorMissedHistoryVisitIsBounded` | No subcase reported |

Isolated evidence, without source or product/test deadline changes:

- Exact schedule race rerun passed (5.765s).
- `-run '^Test(AccountSwitch|StoppedAccountSwitch)'` passed the stopped-account
  switch group (55.814s), including the case interrupted by the server deadline.
- The paired Grok selection
  `-run '^(TestTextClosureRequiresOriginalSummaryAcknowledgmentAndRemoval|TestCreationRetainsOriginalClaimsAndNeverRepeatsUncertainty)$/(closure-bind-failure|setup-foreign)$'`
  failed (35.959s) in `closure-bind-failure` with unavailable/operation-timed-out;
  no setup-foreign failure was reported in that selection.
- A source-only control archived from main
  `98df29c41ddf3c8b1274c51fe8f406b6dae6ca74` ran the exact
  `-run '^TestTextClosureRequiresOriginalSummaryAcknowledgmentAndRemoval$/^closure-bind-failure$'`
  race command and passed (10.989s).
- The same exact single-case command then passed on unchanged branch source
  (10.456s). Grok sources and the schedule test/implementation match that inspected
  main revision. The control does not establish full main success or reproduce a
  failed baseline. Causes of the other native initialization/cumulative failures
  remain unproven; isolated passes do not erase earlier failures.

## Follow-up source distinctions

The completed full run above predates these final changes:

- `1fc0601ef689c35b1d1348b7dd0ad6278e621d3b` charges an entry before Git
  recreates a missing local config. A new native Git-copy regression was first
  supplied through a temporary Go overlay against unchanged source-3530d3d9d;
  it failed (16.004s) because the copy succeeded outside the entry reservation.
  A proposed source overlay passed (42.470s) while the full run's real source
  stayed unchanged. After applying the guard to the worktree, the exact isolated
  race regression passed (24.066s). Final CLI build, vet and Windows workspace
  test cross-compilation passed with this guard.
- `f3c77032a46c3293a77a53319bb656729d21db10` synchronizes native Grok
  fixture diagnostic writes and reads, including shared API fixtures. This repairs
  the three observed log-buffer races without changing production process/native
  behavior or timeouts. The complete `TestProbeOwnsBoundedInspectedInitialization`
  group passed under the race detector (59.500s), retaining its native invalid-frame,
  cancellation, redaction and cleanup checks. Final root Go vet passed.

The full command was not rerun after these focused follow-ups. They do not turn
source-3530d3d9d's failed complete result into final-head success. Root contract
checks passed all 113 tests after the final source changes.

## Final pre-push inventory and deferred maintenance

The one-shot repair's final inventory still saw open/non-draft PR #1231 at remote
head `461cb6abab47dce4aaccd8668a461ee0d2761987`. Its 38-check inventory now
included [Windows Worker failure](https://github.com/delinoio/oss/actions/runs/36722791421/job/109912018425)
and [aggregate CI Result failure](https://github.com/delinoio/oss/actions/runs/36722791421/job/109917276116).
The aggregate log reports `go-test: expected success, got failure`; it is downstream
of the Go job rather than an additional independently diagnosed root cause.
The native Windows Worker package passed (182.426s); workspace failed (301.741s)
only in the added `TestSnapshotRestoredGitIdentityAtLongPrivatePath` at
`snapshot_restored_git_test.go:39` during initial `Prepare`, before restoration
or the long-path assertions. The code returned unavailable/native Git failure.
This is a different failure from the earlier Worker restoration case; no repeat
of that repair or native diagnosis is inferred from it.

Four Codex findings arrived during this pass and remain unhandled for the next
scheduled explicit repair, rather than extending this one-shot inventory:

- [Published restored Git-store identity](https://github.com/delinoio/oss/pull/1231#discussion_r4145427719), thread `PRRT_kwDORRAKg86nj9DD`.
- [Restored-byte revalidation before ownership publication](https://github.com/delinoio/oss/pull/1231#discussion_r4145427732), thread `PRRT_kwDORRAKg86nj9DO`.
- [Pinned claimed-namespace validation during unlink](https://github.com/delinoio/oss/pull/1231#discussion_r4145427742), thread `PRRT_kwDORRAKg86nj9DT`.
- [In-flight workspace-read serialization](https://github.com/delinoio/oss/pull/1231#discussion_r4145427749), thread `PRRT_kwDORRAKg86nj9DW`.


## Limits

A push requires new CI and Codex review evidence. The older source-46452512 full
race failed in CLI and Grok; the source-89cedbb7 run failed in an unborn Local diff
case whose unchanged isolated rerun passed. Those results and unknown causes
remain in their original independent records. No real accounts, credentials,
product Git pushes, native Windows/Linux acceptance or release publication are
established by this pass. Repository-owned generated dist output is removed from
the final worktree.
