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

The repository-wide race/vet, full protocol freshness, client suite/build and
controlled native fixture are in progress; their final results will be recorded
before PR publication. The first client suite attempt passed 41 tests but its
integration setup hit the 120-second Go build deadline. The first native fixture
attempt failed at protocol discovery (initialize/unavailable), before account
switching or provider requests. These attempts do not establish acceptance.

## Acceptance limits

Native opt-in uses explicitly selected Codex `0.151.0`, temporary isolated
server/Worker/runtime roots and a scripted keyless loopback provider. It cannot
access a user's cached credentials. A successful controlled run establishes
native full-history transfer and public CLI/Connect ownership; it does not
establish hosted-account inference, billing, subscription switching, desktop
selection UI, Windows/Linux native execution or release acceptance. Unknown
or required account-bound remote state remains unsupported. No product-wide
issue #964 completion or GA claim follows from this increment.
