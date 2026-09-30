# Account-switch PR maintenance on main 65eca334

## Revisions and preceding observations

This pass started from published PR #1218 head
`9446a96aff22e490f2422cd7f015a7b7240568f3` and merged freshly fetched main
`65eca3341e2180f676fc81c24ebccbc82b67f344` on 2026-09-30.
Main includes the repository-inspection allocation prerequisite (#1214),
Activity sidebar presentation (#1217), pinned required-workflow evaluation
(#1219), and Runner Device terminology (#1220).

Before this repair, all 13 non-skipped reported checks passed on the preceding
head, including the complete CI Result, Go Quality, macOS/Linux Go tests, all
four Windows Go partitions, protocol/client and async-commit-hook checks.
The other 25 checks were skipped. CI run:
https://github.com/delinoio/oss/actions/runs/36707518698.
Both Codex code/security summaries reported completion on that exact head and
there were no unresolved or outdated inline findings. No formal review or human
approval was present. These observations do not validate the following push.

## Conflict composition

Only `docs/protos-delidev-v1-contract.md` and `protos/delidev/AGENTS.md` conflicted.
Retain the complete stopped-account selection rule and the complete main
repository-inspection prerequisite. Worker capability 6 and attachment-response
field 3 remain reservations; they cannot advertise implemented support. Account
selection retains System capability 5, independently of Worker capability values,
permanent deletion and native accounting. No new reservation or migration is added.

Canonical schemas and generated bindings had no merge conflict. The normal
`pnpm proto:generate` pass reproduced them without any source delta. Imported
main behavior is retained; the authored conflict resolution changes no product
source or frontend behavior relative to that main.

## Executed verification

- `GOMAXPROCS=2 go test -race -p 1 -timeout 5m
  ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/apiproxy
  ./cmds/delidev-cli/internal/cli
  -run 'Test(AccountSwitch|StoppedAccountSwitch|HistoryObservation|SwitchedHistory|FullNativeHistory|CLISwitch)'
  -count=1` passed: server 177.239 seconds, apiproxy 1.480 seconds,
  CLI 1.730 seconds. It retains explicit Resume, historical attribution,
  original checkpoint connections, principal/revocation checks and pending deletion.
- `GOMAXPROCS=2 go vet ./cmds/delidev-cli/...` passed.
- `node --test scripts/ci/delidev-structure.test.mjs
  scripts/ci/delidev-proto.test.mjs scripts/ci/proto-breaking.test.mjs` passed
  all seven checks, including reservation declaration kinds and immutable numbers.
- Protocol generation, `pnpm proto:lint` and `pnpm proto:breaking` passed.
- `pnpm --filter @delinoio/delidev-api-client test` passed all 44 tests in four
  files; its explicit build passed.
- Desktop typecheck and the six targeted sidebar/workflow/Runner Device test files
  passed all 140 tests after the client build completed. Initial checks started
  before that dependency finished and failed import resolution; they are not
  counted as passing checks and no product code changed to address that mistake.
- From `apps/delidev`, `VITEST_MAX_WORKERS=2 GOMAXPROCS=2 pnpm test` passed the
  complete required chain: client build, typecheck, all 1,264 tests in 98 files,
  eight packaging dry-run tests, 16 desktop-launch/asset tests, widget fixture
  checks and the frontend production build. The installed Vitest version supports
  the worker-count environment selector; no source, test or product deadline changed.
- The required icon was hydrated and verified against its tracked LFS SHA-256
  and exact size before asset checks/build. `git lfs pull` printed an index warning
  for the still-unmerged documentation, but the icon's verified native PNG bytes
  were present and no asset source changed.
- `pnpm --filter devhud-admin build:embedded` passed real embedded-asset preparation
  for the root Go formatting hook. This does not establish native runtime acceptance.

## Continuing limits

The complete frontend pass supersedes the earlier failed frontend attempt for
this merged source. Prior failed attempts remain in their independent records.
The preceding published head's cross-platform CI passes likewise do not erase
local historical full-Go failures or validate this new head before CI completes.

Installed Codex A-to-B acceptance remains unverified after the two recorded
initialization failures. No installed-native, hosted-account, subscription,
platform-release or billing evidence is added by these checks. Repository-owned
`dist` outputs are removed after validation and commits.

## Committed-source result

Merge commit `1666d9250453a901c4bd64a1d2a28b8e569f03da` has parents equal to
the preceding published head and inspected main above. Root Lefthook Go formatting
passed. A complete post-commit `pnpm proto:check` passed formatting/lint, breaking
comparison and forced Turbo regeneration with no tracked or untracked generated
source drift. The final repair inventory had no Codex threads and no failing
reported checks; it still described the preceding published head. This repair
is pushed once, and new-head CI/review evidence remains pending after the push.
