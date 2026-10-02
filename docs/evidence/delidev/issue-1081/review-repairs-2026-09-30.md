# Manual PR fixes: review repairs and completed local runs

PR: [#1227](https://github.com/delinoio/oss/pull/1227). Source revision:
`cd6089e063f160420a025bcd39e287165864dd82`, including main `65eca3341`.
Date: 2026-09-30. This independent record completes the pending-run observations
in the earlier evidence files without changing those historical snapshots.

## Handled review findings

- `PRRT_kwDORRAKg86ng9Gv`: commit `cd5f86619` corrects the desktop contract to
  merge by default, retaining rebase only for an explicit effective policy.
- `PRRT_kwDORRAKg86ng9G1`: commit `cd6089e06` focuses the project selector each
  time the manual-fix form opens. Native select autofocus does not reclaim focus
  after later capability refreshes. The scoped app instructions, desktop
  contract and regression fixture describe that interaction.
- `PRRT_kwDORRAKg86ng9HE`: commit `bf4ce794b` removes the separate local Git
  supervisor and unrecorded temporary process journal. Ordinary child execution
  retains the original launcher/native owner, whose recovery and deletion
  inventory already exists. The Worker joins that owner before accepting proof.
  See `local-launcher-containment-2026-09-30.md` for the actual containment
  evidence and its live-sandbox/platform limits.

Only these handled threads are eligible for resolution after the repair push.

## Unresolved native Git boundary

The P1 thread `PRRT_kwDORRAKg86ng9G8` remains actionable and unresolved:
[verification-to-execution race](https://github.com/delinoio/oss/pull/1227#discussion_r4144215820).
The bridge hashes the executable and configuration before authenticated native
execution, but mutable files can change before the separate process launch.
Existing hashes, exact command operands and workspace identity checks do not
eliminate that race. A full-access Agent can mutate same-user private files;
copying files into a private directory alone is not evidence of isolation.

A user decision is pending on narrowing the initial profile to workspace-write
or retaining full-access and redesigning authenticated execution. Root
`AGENTS.md` requires clarification before removing or reinterpreting ambiguous
source-backed documentation scope. No scope reduction or race fix is claimed.
Either choice still requires a concrete execution/configuration boundary and
regression evidence before this P1 can be resolved. This PR is not merge-ready.

## Completed verification

- `pnpm typecheck` from `apps/delidev`: passed after the autofocus repair.
- `pnpm test:unit src/pr-fix.test.tsx --maxWorkers=1` from `apps/delidev`:
  passed all six cases, including reopen focus and capability-refresh behavior.
- `GOMAXPROCS=2 pnpm test` from `apps/delidev`: failed one existing App fixture
  after 5,223 ms exceeded its 5,000 ms limit; 98 files and 1,269 tests passed.
  The failing case was `discards notification and import drafts on close without
  saving`. The composite command stopped before its later bundle/build steps.
- `pnpm test:unit src/App.test.tsx -t 'discards notification and import drafts on close without saving' --maxWorkers=1`:
  passed the isolated case, with 43 other cases intentionally skipped. This
  retry does not convert the failed full command into a full-suite pass.
- The explicit remaining frontend steps passed: `pnpm test:bundle-dry-run`
  (eight fixtures), `pnpm test:desktop-launch` (16 fixtures), `pnpm test:widget`
  and `pnpm build`. Packaging fixtures and the frontend build do not establish
  a signed native release or live-account acceptance.
- The earlier serial current-main unit run completed with 1,242 passing and six
  skipped tests. Nine suites failed build preparation, teardown or temporary
  server readiness; preserve that result alongside the later run.
- `GOMAXPROCS=4 go test -race -p 2 ./cmds/delidev-cli/... -timeout=20m` completed
  unsuccessfully. CLI recovery/forwarding fixtures, Claude interrupt and Codex
  late-steer fixtures failed deadlines. Grok reported initialization failure
  followed by the package timeout; server reached its package timeout. Worker,
  workspace and store passed in that run (379.102, 862.913 and 385.886 seconds).
  It started at `0ce9f8e06`; main reconciliation and later repairs occurred while
  it ran, so its package results do not prove one immutable current snapshot.
- The independently completed focused native Git race suite and Go vet after
  `bf4ce794b`, main-reconciliation focused server/domain race checks, protocol
  freshness checks and focused frontend checks remain recorded in the prior
  independent records. No further Go source changed in the autofocus repair.

All background validation runs from this maintenance pass have finished.
Removed the final two generated repository-owned `dist` directories
(`apps/delidev/dist` and `packages/delidev-api-client/dist`), preserving
dependency directories. No Rust source changed. No full-suite pass, current-head
CI approval, live Codex sandbox denial, real GitHub publication, real-account
fix execution or cross-platform/release acceptance is claimed.
