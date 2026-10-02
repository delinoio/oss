# Seventh maintenance validation

Date: 2026-10-01 (Asia/Seoul); 2026-09-30 UTC.
PR: [#1227](https://github.com/delinoio/oss/pull/1227).
Stable Go source revision: `c26cb7ba0ae1c6f7f30a240adf83c33abf750465`,
following main reconciliation `089bb124a53499b5bbccda1e715c5c9bf00e9bb7`.
This record adds evidence only. Historical records remain unchanged.

## Reconciliation and focused repair

The [merge record](seventh-maintenance-main-6c749670-2026-10-01.md) retains the
composed manual-fix and native-fork boundaries. Its focused race command completed
with exit 1 in 1,281.768 seconds. Domain (2.334 seconds), Worker (9.162), workspace
(547.998) and Codex (138.269) passed. Server failed its PR workspace-read fixture
group in 421.840 seconds, as recorded independently. That server phase finished
before subsequent server repair edits; the remaining workspace/Codex source was
unchanged during their checks. The later complete stable-source server run passed,
without changing this earlier failed selection into a pass or proving its cause.

The [preflight backoff record](preflight-retry-backoff-2026-10-01.md) retains the
separate quota fix and its race regression pass (8.451 seconds), including actual
coordinator/provider call counts, independent ordinary dispatch, original queued
input, explicit Stop/Resume and cancellation/join of a stalled read. The same
deadline and authenticated manual-fix RPC tests also passed in the complete
stable-source server run below. Backoff supplies no native or push replay grant.

## Required Go checks on stable source

`GOMAXPROCS=2 go test -race -json -p 1 ./cmds/delidev-cli/... -count=1
-timeout=20m` finished with exit 1 in 4,533.655 measured elapsed seconds. Durations
here use command/Go elapsed measurements, not subtraction of wall-clock timestamps.
The source stayed unchanged throughout this complete attempt.

Four packages failed:

- CLI (185.344 seconds): `TestCLISessionAcceptanceQueueAndArchive` failed during
  `session files read` of `tracked.txt` with an unavailable original workspace
  reader; Worker logs retained a recovery-required read result.
- Harness discovery (89.353 seconds):
  `TestDiscoveryVerifiesOpenCodeWithoutExecution` and
  `TestDiscoveryUsesIsolatedEnvironmentAndOwnedProcesses` failed their bounded
  initialization/version observations. OpenCode remained failed/unverified;
  Codex reported an unavailable version probe rather than verified discovery.
- Claude harness (623.739 seconds):
  `TestProbeOwnsOnlyBoundedPrivateInitialization` failed account/trailing timeout
  cases and request/oversize/stderr cleanup-confirmation cases.
  `TestStreamPreservesLateAcknowledgmentsAndCanceledReads`,
  `TestStreamRejectsProtocolFailuresAndStopsOwnedScope` (malformed and duplicate-key
  cases), and `TestStreamBoundsBlockedInputAndJoinsWriter` also failed.
- Grok harness (1,201.485 seconds):
  `TestOriginalPlanReplyClaimJoinsNativeLifetimeLoss` failed native initialization;
  `TestProbeOwnsBoundedInspectedInitialization/inventory-missing` failed. The
  package then exhausted its aggregate 20-minute budget with
  `TestCreationRetainsOriginalClaimsAndNeverRepeatsUncertainty/session-timeout`
  unfinished. That group and remaining Grok cases are not proved by this run.

All other reported race packages passed, including server (677.244 seconds),
store (232.900), Worker (146.953), workspace (640.694), Codex (499.598), API proxy,
connections, credentials, domain, forwarding, native wire, OpenCode, GitHub
integration, presentation, process, providers, security and user service.
This does not establish a complete Go-suite pass. No failed case was dismissed
as a known baseline issue and no unperformed isolation comparison is claimed.

`GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...` passed (83.266 seconds).
`GOMAXPROCS=2 go build -p 1 -o /tmp/delidev-1227-seventh-cli
./cmds/delidev-cli` passed (6.095 seconds).

## Protocol, frontend and packaging

Required embed builds and reconciled protocol generation/checks passed as recorded
in the merge evidence. Regeneration introduced no unstaged binding drift. Shared
fork allocations already existed on main; this repair adds no number or migration.

`pnpm test` in `packages/delidev-api-client` passed all 46 cases in five files
(9.767 command seconds). Required `pnpm test` in `apps/delidev` failed: API-client
build and type checking passed first, then Vitest reported 1,150 passed, 125 failed
and 10 skipped cases in 101 files (27 failed, 74 passed). Vitest duration was
279.15 seconds and command duration 314.809 seconds. The following script steps
did not run after that failure. Deadlines and DOM waits remain failed observations;
concurrent local test processes were observed, but resource contention was not
proved as their cause and no baseline dismissal is made.

The explicit remaining steps passed:

- `pnpm exec vitest run src/pr-fix.test.tsx src/session-fork.test.tsx
  --maxWorkers=1`: ten cases in two files (6.193 command seconds).
- `pnpm test:bundle-dry-run`: eight fixtures (0.822 seconds).
- `pnpm test:desktop-launch`: sixteen fixtures (39.774 seconds).
- `pnpm test:widget` (6.411 seconds).
- `pnpm build`: production output (1.878 seconds).

These focused/explicit passes do not replace the failed complete frontend run.
Consumed DeliDev assets were hydrated; no unrelated pointer-only assets were
needed. Repository-owned generated dist output is removed at completion while
dependency caches remain.

## CI and unresolved boundaries

The in-pass CI inventory on pushed predecessor `52e7c6866` contained one successful
Cloudflare Pages check and no failed check. Earlier Actions success on
`fc614326b` remains historical. No current repair-head Actions result, review
approval or native/account/platform/release acceptance is established here.
Newly pushed-head observation belongs to the next heartbeat.

The original executable/configuration verification-to-authenticated-execution P1
and companion-content P1 remain unresolved, and both human decisions remain
unanswered. This pass preserves documented full-access/companion support and does
not assert immutable same-UID copies, independent read isolation or reviewed
publication authority. Only the separately fixed preflight retry thread is
eligible for resolution after the final single push succeeds.
