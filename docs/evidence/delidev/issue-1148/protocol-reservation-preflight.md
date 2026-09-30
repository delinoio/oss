# Issue #1148 protocol reservation preflight

## Inspected state

Inspected freshly fetched `origin/main` at
`ad0e3e9a29cb3d8375ab5d168bb160c35a023250` on 2026-09-30.
Issue #1148 was open, with no comments or matching implementation PR. Its timeline
linked closed issue #1160 (Models presentation), not a provider expansion PR.

The issue is not already resolved:

- `providers.Presets()` returns the existing six hosted and three local presets.
- `ProviderPresetId` declares values 0–9 only, and the closed domain enum retains
  the same nine identities.
- Provider inventory still queries at most ten managed rows.
- The executable migration sequence ends at schema 24. The storage ledger already
  reserves 25 for #1108, 26 for #1115 and 27 for #1117. The issue's investigated
  migration-25 proposal therefore needs an explicit sequence decision.

## Prepared prerequisite

Reserve the 26 issue-owned `ProviderPresetId` members at values 10–35 in the issue's
table order, retaining every existing assignment. Record the originating `issue`
rather than labeling it as a PR. The protocol contract includes the complete
stable-ID/value/name mapping; scoped instructions describe activation ownership.

This change does not activate schema declarations, presets, adapters, migration
SQL, account behavior or generated-client support. Those dependent changes require
the protocol and migration reservations to be established on main first under the
structure contract. The historical evidence ledger remains intact.

## Executed validation

- `pnpm install --frozen-lockfile`: passed; installed the worktree dependencies
  and repository hooks without changing the lockfile.
- `node --test scripts/ci/delidev-proto.test.mjs scripts/ci/delidev-structure.test.mjs`:
  all six tests passed, including allocation collision checks, immutable baseline
  checks, semantic FILE compatibility checks and the existing migration ledger.
- Root `pnpm proto:check`: passed formatting/lint, breaking comparison and forced
  generation/freshness checks. Generated sources had no drift.
- `git diff --check`: passed.

No frontend or runtime source changed, and no live-provider requests, valid-key
checks, inference or native-harness acceptance were performed. The full issue's
adapter fixtures, upgrade/inventory/lifecycle checks, Go race/vet checks, frontend
tests and evidence requirements remain implementation work after the prerequisite.

## Pending coordination

The owner was asked whether the provider migration should precede the three
reserved changes at 25 (moving them to 26–28), or follow them at 28. No storage
reservation or migration sequence is changed before that decision. The fix-issue
workflow prohibits agent merging, so landing the eventual prerequisite also needs
a human merge before dependent implementation can begin. Issue #1148 remains open.
