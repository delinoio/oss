# Stable ALLGREEN merge-queue CI: replacement implementation

## Revision and provenance

Issue: https://github.com/delinoio/oss/issues/1105. Freshly fetched main and starting
revision: `ad0e3e9a29cb3d8375ab5d168bb160c35a023250`, inspected on 2026-09-30.
The two previous PRs, #1110 and #1168, were closed without merging; main still
classified every queued PR as Unknown. This replacement starts directly from
current main and preserves its scoped ownership and original PR activity.

Reused the scoped implementation from `ea7683f64daec5e19e98962496c122b45dff5551`
and the final-read/history-identity corrections from
`da82e06c859045be7c674121333ce7a326a26677` and
`a1d486adcfa5ee5af27cd814c704218e8c3e8134`. Earlier validation records are not
claimed as checks executed for this replacement. Added independent unequal-page
coverage for queue membership and entry-head checks.

## Implemented boundary

The existing authenticated read-only CI operation collects complete original PR,
queue/entry, ordered membership, entry base/head, strategy, active-rule and check
proof. Repeated complete inventories and an unconditional final inventory after
the final rule read must agree. Only ALLGREEN can select the exact entry head.
Actions checks require their original `merge_group` workflow and independently
matching workflow suite/commit. Queue state alone cannot establish failure.

HEADGREEN and absent attribution stay Unknown. Changed, partial or inaccessible
inventories return no successful assessment. Non-queue evaluation and legacy
history remain supported. Replacement queue/entry identities retain separate
proofs even when they reuse a native result; removal clears current authority
while preserving historical proof and local decisions. Desktop validation and
inert display use the existing generated operation. No schema, migration or
protocol allocation changes were made.

## Executed validation

- `go test -race ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/integrations/github ./cmds/delidev-cli/internal/store -run 'ALLGREEN|QueueCI|CIQuery|RequiredCI|HistoricalQueue|CIProblem|PRCI|Remediation'`: passed all three packages (1.813s, 2.101s and 18.036s).
- `go test -race ./cmds/delidev-cli/internal/integrations/github`: passed the full adapter suite, including unequal entry/check pagination (3.302s).
- `go vet ./cmds/delidev-cli/...`: passed.
- `pnpm test` in `apps/delidev`: passed, including API-client generation, type checking, 95 files / 1,240 component tests, eight package fixtures, sixteen launcher/LFS fixtures, Swift widget fixtures and the production build.
- `node scripts/delidev/verify-independent-changes.mjs`: passed with reproducible generated bindings and no shared changed files in its structural fixture.
- `git diff --check`: passed; `go fmt ./...` passed. The DeliDev icon was explicitly hydrated with Git LFS before consuming packaging output. Generated repository-owned `dist` output is removed from the final worktree.
- Executed the exact fixed GraphQL document through `gh api graphql` on public PR #1110: GitHub returned no schema errors, PR 1110, `isInMergeQueue: false`, and no entry. This checks query compatibility only.

The complete Go race run and an untouched-main native CLI control are recorded
separately after completion. No real queued-account, native product/platform or
release acceptance is claimed. Queue behavior uses controlled provider fixtures;
native integration tests use independent temporary state.

## Provider references

- [GitHub merge-queue semantics](https://docs.github.com/en/enterprise-cloud%40latest/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue)
- [GitHub GraphQL queue configuration and strategy](https://docs.github.com/en/enterprise-cloud%40latest/graphql/reference/pulls)
