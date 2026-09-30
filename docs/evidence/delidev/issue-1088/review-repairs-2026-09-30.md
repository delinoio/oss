# Issue #1088: PR #1226 review and CI repair

## Revision and scope

PR: https://github.com/delinoio/oss/pull/1226. This is one repair pass on the
existing `kdy1/issue-1088-worker-terminals` branch. The main integration commit
`bd8d86929e68cd51572a13432cb5ba7df4c264e2` merges
`d1f83cecee4e0c50ea094335392cf68845f85739`, preserving both terminal deletion
and current native-profile instructions. Focused server race tests for the
native-accounting capability and joined deletion boundary passed (6.391 seconds).
No protocol schemas or generated bindings changed in this repair.

Final implementation source: `6f463ddcf96a35fc905a308715802874a39f5654`.
The desktop source is identical to `919ef80574d85b20b0f5e5932e02fa9e97e8a5a5`;
the following commit changes only server eviction and its contract/instructions.
Subsequent documentation-only commits reconcile the desktop/project capability
boundary, accepted selection and protocol diagnostic contract without changing
that implementation source.

## Separate repairs

- `ffc73c76999c894cb3e74c7ff8288f27c447a2de`: failed creation copies the joined
  publisher's output-loss fact into its native result. The controlled missing
  original-directory fixture exercises publisher cancellation and conservative
  uncertainty before launch; it is not a post-Resume path-race simulation.
  Existing server loss/gap fixtures cover the corresponding client notification.
- `04c403b45586d6c74c72179bec8c1bb88d617236`: retain one accepted creation
  resource so it remains selected and attachable beyond the first 50 history
  records. The fixture watches and controls that exact returned terminal while
  every refreshed history page still excludes it.
- `5bd785ab231744922a8169b159b163553bf6732e`: share output flag parsing with
  deadline selection. Parsed follow streams retain caller lifetime; non-follow
  reads retain the ordinary 30-second bound. Actual Connect request headers
  cover both actions, default/true/false/repeated flags and caller deadlines.
  This checks propagated deadlines without a 30-second wall-clock wait.
- `3991a8f0248441c8f00761ad1a6fbf3f2516b4c1`: validate the closed native failure
  classification and replace all Worker diagnostic text with server-owned text
  before persistence. Adversarial reports cover unknown-code rejection without
  consuming the request, stripped long message/guidance/cause/correlation data,
  public persisted reads, exact read-only replay and changed-original-byte
  conflicts. Running results cannot carry contradictory failure problems.
- `919ef80574d85b20b0f5e5932e02fa9e97e8a5a5`: gate history reads, polling,
  refresh and selection on advertised terminal support. The fixture covers
  pending status, unsupported status, support becoming available and subsequent
  support loss, including manual refresh and polling intervals.
- `6f463ddcf96a35fc905a308715802874a39f5654`: replace timestamp-based ring
  eviction with exact bounded access order. The original Windows server CI job
  failed `TestTerminalOutputDiscardedRingExposesFreshAttachmentGap/evicted`
  because equal timestamps could select an arbitrary ring. A separate fixture
  verifies that touching an old ring preserves it and evicts the next oldest,
  with both the map and order metadata remaining bounded to 128 entries.

Original failing CI: [Windows server job](https://github.com/delinoio/oss/actions/runs/36710201977/job/109869983585),
on pre-repair head `e9dd5862d18ff679d949c3fd0b576f0b60dfa122`. Ubuntu and macOS
Go CI, Windows core/Worker/harness and the other executed checks passed on that
head. Those results do not approve the repaired source.

## Completed validation

- Root `GOMAXPROCS=2 go vet ./cmds/delidev-cli/...` passed after all source repairs.
- Root `pnpm proto:check` passed lint, baseline breaking checks, regeneration
  and generated-source freshness after all source repairs.
- Root `pnpm ci:contracts` passed all 113 tests after the main merge and review
  repairs, before the independent eviction repair.
- `GOMAXPROCS=2 go test -race ./cmds/delidev-cli/internal/worker -run
  'TestTerminalFailedCreateRetainsAbandonedPublisherOutput|TestTerminalNativeCreateAndInputReceiptLossNeverReplay'
  -count=1` passed (2.786 seconds).
- `GOMAXPROCS=2 go test -race ./cmds/delidev-cli/internal/cli -run
  'TestTerminalCLI' -count=1` passed (1.775 seconds).
- `GOMAXPROCS=2 go test -race ./cmds/delidev-cli/internal/terminal
  ./cmds/delidev-cli/internal/server -run
  'TestResultRejectsUntrustedFailureClassifications|TestTerminalReportSanitizesWorkerProblemsAndPreservesExactReceipt|TestTerminalReceiptsOutputReattachAndArchiveBarrier'
  -count=1` passed (terminal 1.397 seconds; server 4.480 seconds).
- `GOMAXPROCS=2 go test -race ./cmds/delidev-cli/internal/server -run
  'TestTerminalOutput' -count=10` passed (64.084 seconds).
- The terminal UI file passed all five tests and desktop typecheck passed.
- `git lfs pull --include='apps/delidev/**'` completed and the required icon was
  hydrated before the full desktop consumer run.
- `GOMAXPROCS=2 VITEST_MAX_WORKERS=1 pnpm test` in `apps/delidev` passed: explicit
  client build, typecheck, 99 files / 1,269 tests, eight packaging tests, 16
  launcher/asset tests, native Swift widget fixtures and production build.
  Vitest took 178.77 seconds.
- `GOMAXPROCS=2 GOOS=windows GOARCH=amd64 go test -c -o <temporary-output>
  ./cmds/delidev-cli/internal/server` passed on the final source. This is
  cross-compilation, not native Windows execution.
- `git diff --check` passed. Generated desktop/client `dist` directories were
  removed after their consumers finished; no repository-owned `dist` remains.

## Failed, interrupted and pending validation

The earlier complete Go attempt referenced by `replacement-final-validation-2026-09-30.md`
failed `TestCLISessionAcceptanceQueueAndArchive` and was interrupted before this
repair. A new `GOMAXPROCS=2 go test -race ./cmds/delidev-cli/...` at `919ef805`
also failed that CLI test during `session diff` and was interrupted before the
eviction repair to avoid mixing source revisions. No passing complete race
suite is inferred from either attempt.

An isolated `GOMAXPROCS=2 go test -race ./cmds/delidev-cli/internal/cli -run
'^TestCLISessionAcceptanceQueueAndArchive$' -count=1` at the final source failed
again during `session review-context` (63.696 seconds). Its private structured
log records 23 native Git starts during the original 15-second observation,
followed by workspace-read recovery/unavailable classification. The exact
underlying performance or cleanup cause has not been established. This is not
classified as a baseline failure; no observation or cleanup deadline was relaxed.

A final root `GOMAXPROCS=2 go test -race ./cmds/delidev-cli/...` at `6f463ddc`
completed with exit code 1. It reproduced the CLI review-context deadline failure
(CLI package 250.758 seconds), and the
Grok package reached the Go runner's default ten-minute aggregate test limit
(601.703 seconds), during `TestInitialPlanRequiresOriginalClaimAckAndMode`.
The running parent test was 1 minute 38 seconds old and its current subcase
7 seconds old. CI's `scripts/ci/go-test.mjs` uses a 20-minute package runner
limit; native observation/cleanup deadlines are separate and remain unchanged.
The server package also reached that ten-minute aggregate runner limit
(600.792 seconds), during the one-second-old
`TestForwardExactBytesReceiptReplayAndStopVersusArchive`. The full store race
package passed (312.180 seconds), Worker passed (296.858 seconds), workspace
passed (554.953 seconds) and terminal passed (1.407 seconds). All other packages
completed successfully, including cached results where reported by Go. No
passing Grok/server race package or passing complete root race suite is claimed.
The runner timeouts leave unexecuted cases in those two packages; focused
terminal checks passed separately as listed above.

## Remaining acceptance limits

Earlier protocol freshness, client integration and platform compilation evidence
remains qualified in the independent replacement records. Real-account,
physical remote-Worker, native Windows/Linux, native desktop visual and release
acceptance remain unperformed. The text/control view still has no full-screen VT
emulation. Fresh head-specific CI and Codex reviews remain required after the
single repair push. The existing five-minute heartbeat remains the maintenance
owner; no merge or auto-merge is performed by this workflow.
