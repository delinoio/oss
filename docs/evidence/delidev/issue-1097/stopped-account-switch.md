# Explicit stopped-session API account switching

## Revision and source

Issue: https://github.com/delinoio/oss/issues/1097. This implementation starts
from main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` and adapts the scoped
implementation from the closed, unmerged PR #1112 at
`34544c0082b4aa11e18db3e00f4d555caab53f01`. The earlier validation record is
historical evidence; the commands below are fresh checks of this change.
The final implementation and validation commits are discoverable in this file's
Git history on `kdy1/delidev-1097-account-switch`.

## Implemented boundary

Authenticated owner/client Connect and CLI selection require a paused, settled
Codex API session, verified cleanup and full native history, an unchanged
provider/model, and an eligible account in the original immutable candidate
snapshot. Selection appends a revisioned receipt-bound change and preserves
all original assignments, transcripts and usage. It remains paused; only an
explicit Resume creates a fresh account-scoped execution credential.

The server records sticky account-bound history before provider work and
rejects conversation, previous-response and item references after switching.
The Worker reads the original predecessor checkpoint under its original
account/connection and verifies native history/settings before new input.
Clients use typed capability number 5, preserving existing capabilities 1–3
and the separate native-accounting allocation at 4. Generated compatibility
facades expose the same canonical SessionQuery method and request descriptors.

## Fresh validation

- `CI=1 pnpm install --frozen-lockfile`: passed, including linked-worktree
  Lefthook installation; app preparation was intentionally skipped by CI mode.
- `pnpm proto:generate`: passed; generated Go, TypeScript and compatibility
  outputs were regenerated from the service-owned schemas.
- `pnpm proto:lint`: passed.
- `DEVHUD_PROTO_BASELINE=74701b8948694e2bf8f8ba6d07e596c2d2f358a7 pnpm proto:breaking`:
  passed against the inspected base.
- `GOMAXPROCS=4 go test -race -p 2 -timeout 5m ./cmds/delidev-cli/internal/server
  ./cmds/delidev-cli/internal/cli ./cmds/delidev-cli/internal/apiproxy -run
  'Test(AccountSwitch|StoppedAccountSwitch|CLISwitchAccount|Switched|FullNativeHistory)'
  -count=1`: passed. Coverage includes immutable original snapshots/jobs,
  exact receipt replay, revoked A grants, fresh B grants, unchanged original
  usage attribution, no automatic dispatch, and atomic refusal of active,
  unknown/account-bound history, incomplete cleanup, stale revision, disabled
  or foreign candidate, disabled provider, Archive, unsettled interactions,
  Worker access, project restrictions and contradictory terminal state.

- `DEVHUD_PROTO_BASELINE=74701b8948694e2bf8f8ba6d07e596c2d2f358a7 pnpm proto:check`:
  passed after committing generated output, including formatting/lint, breaking
  comparison and forced reproducibility without generated-source drift.
- `GOMAXPROCS=4 GOFLAGS='-p=2' pnpm test` in
  `packages/delidev-api-client`: passed all 44 tests. `pnpm typecheck` and
  `pnpm build` also passed. The first suite attempt passed 41 tests but its
  integration setup hit the 120-second Go build deadline; the bounded-concurrency
  retry passed, including the real authenticated Go server integration.
- After appending the new RPC to preserve all existing method order,
  `pnpm exec vitest run tests/legacy-imports.test.ts` and `pnpm typecheck`:
  passed (2 compatibility tests).
- `node --test scripts/ci/delidev-proto.test.mjs
  scripts/ci/delidev-structure.test.mjs`: passed all 6 tests, including immutable
  numeric assignments, relocation and semantic breaking enforcement.
- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/apiproxy
  -run 'TestSwitchedHistoryGuard|TestFullNativeHistory' -count=1`: passed after
  independently scanning mixed input-array elements. Every remote-reference
  refusal precedes credential access and upstream transmission.
- `GOMAXPROCS=4 go vet -p 2 ./cmds/delidev-cli/...`: passed from the repository
  root. A final `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...` also passed.

## Broader validation failures and native observations

`GOMAXPROCS=4 go test -race -p 2 -timeout 20m ./cmds/delidev-cli/...` was
started from the repository root. It has observed failures in the existing CLI
workspace/preparation checks and Claude/Grok/OpenCode/Codex discovery checks,
including version-probe, operation and cleanup deadlines. It remains running
at this evidence snapshot; remaining packages are unverified. These failures
do not invoke the new account-switch operation. They occurred on a host with
many concurrent native/process test suites; that observation does not prove
their cause or turn the required full race command into a passing check.

An earlier broader `go test -race -p 2 -timeout 20m ./...` also reported
missing ignored embedded bundles for unrelated async-commit-hook and DevHud
packages, plus CLI workspace-read and native-discovery failures. It was
interrupted after those failures in favor of the issue-required scoped command.
`pnpm --filter devhud-admin build:embedded` subsequently generated and validated
the real administrator bundle. This preparation is not a repository-wide Go
pass. Generated repository `dist` output is removed before publication.

The controlled native command used explicitly selected Codex `0.151.0`:
`DELIDEV_NATIVE_THREAD_EXECUTABLE=/private/tmp/delidev-1092-codex/codex
GOMAXPROCS=2 go test -race -p 1 -parallel 1 -timeout 5m
./cmds/delidev-cli/internal/cli -run '^TestManualNativeCLIAccountSwitch$' -count=1`.
The final attempt failed at the public CLI discovery wait deadline, before
account selection. Other attempts failed the initialization/version probe.
A temporary wrapper disabling optional plugin synchronization also failed
discovery and is not an acceptance result or a product configuration change.

One earlier run of the longer native fixture completed A's original turn,
committed and replayed one A-to-B selection, and completed B's first native
continuation. Its per-request assertions checked ordered prior native content
and absence of A's remote response/conversation identifiers; the current
execution used B while the immutable initial selection retained A. That run
then timed out on its third turn and did not pass overall. This is partial
native observation, separate from the passing server usage/scope tests above.
The new fixture is limited to two turns with explicit paused-queue verification,
post-completion exact receipt replay and preserved transcript checks; the
original longer FIFO/interrupt fixture remains intact. Its overall deadline is
three minutes; product probe and operation bounds remain unchanged.

No complete native acceptance pass is claimed for the final revision. Rerun
the explicit two-turn fixture and complete the full scoped race suite on an
available host before treating those gates as satisfied.

## Acceptance limits

Native opt-in uses explicitly selected Codex `0.151.0`, temporary isolated
server/Worker/runtime roots and a scripted keyless loopback provider. It cannot
access a user's cached credentials. A successful controlled run establishes
native full-history transfer and public CLI/Connect ownership; it does not
establish hosted-account inference, billing, subscription switching, desktop
selection UI, Windows/Linux native execution or release acceptance. Unknown
or required account-bound remote state remains unsupported. No product-wide
issue #964 completion or GA claim follows from this increment.
