# Issue #1206: native model discovery prerequisites

## Inspected baseline and scope

On 2026-09-30, fetched and inspected `origin/main` at
`ad0e3e9a29cb3d8375ab5d168bb160c35a023250`. The checkout initially had no changes
and no attached PR. The issue was open and the open-PR search for #1206 returned
no matching PR.

Source inspection found no `model/list` invocation or native model discovery
capability in the Go component, active DeliDev proto schemas or desktop source.
The existing catalog contract explicitly keeps native discovery pending. Issue
#1095 was also open; this change does not establish its managed-subscription lease
or credential lifecycle. These are source/history observations, not native tests.

The root AGENTS rule and `docs/cmds-delidev-structure-contract.md` require shared
protocol reservations on main before dependent implementation. This prerequisite
reserves server capability 15 and Worker capability 5, records the complete pending
native observation/explicit-registration boundary, and leaves active schemas,
capability advertisements and the executable migration registry unchanged. It does
not complete or close #1206. No migration version is allocated by this change.

The official pinned `model.rs` was retrieved through `gh api` at
`78c290807ce710180111df227df3b7a4fe845452`; its separate picker `id`, executable
`model`, pagination and advisory metadata agree with the issue's boundary.

## Executed validation

- `pnpm install --frozen-lockfile`: passed; installed the linked-worktree hook and
  required workspace binaries without changing the lockfile.
- `node --test scripts/ci/delidev-proto.test.mjs scripts/ci/delidev-structure.test.mjs`:
  passed all six checks, including numeric assignment collision protection and
  preservation of existing migration reservations.
- `pnpm proto:check`: passed formatting, lint, breaking compatibility against the
  fetched baseline and forced Go/TypeScript generation freshness. Generated
  bindings had no diff.
- An ephemeral Python check verified the pending domain contract's required
  section order and all its local links: passed.
- `git diff --check`: passed.

## Remaining acceptance

No Go, desktop or Rust runtime source was changed. No native process, provider
account, subscription login, model collection, registration flow or real-platform
acceptance was exercised. The five feature scenarios in the pending native model
contract remain unperformed. Main must establish the reservations before dependent
implementation; managed-subscription discovery additionally needs #1095's full
exclusive account lease, protected transfer/writeback and cleanup boundary.
