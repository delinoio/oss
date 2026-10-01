# Eighth maintenance validation

The 2026-09-30T23:08:36Z heartbeat inspected open, non-draft PR #1227 at
`caaa5dd44a8123c50220bcac69824298cf3b1e42`. GitHub reported MERGEABLE/BLOCKED,
with no review approval. The recorded checkout was clean and matched the exact
repository and branch; other observed validations belonged to other worktrees.
This pass explicitly used the installed repair-pr skill and did not overlap the
completed seventh pass.

Two independent review repairs were committed separately:

- `cb0174e0c642f3dc9a63d7a25dc0513f8d84187e` displays the original handled-push
  attempt, execution, pushed commit and server handling time. See
  [the audit record](handled-push-audit-2026-10-01.md).
- `e8130258b251a84a7ff7db1314bf78c494980246` preserves case-sensitive original
  Git environment names on POSIX and case-insensitive normalization on Windows.
  See [the environment record](git-environment-case-2026-10-01.md).

Source remained stable at the second commit throughout the required workspace
package race command. The frontend source remained unchanged after the first
commit throughout its complete run. No Rust, protocol schema, generated binding,
dependency, migration or permission-profile change was made. Documentation and
scoped instructions clarify the existing audit and native-environment contracts.

## Frontend results

From `apps/delidev`, required `pnpm test` exited 1 in 345.255 measured elapsed
seconds. API-client build and TypeScript checking passed before Vitest. Vitest
reported 1,199 passing and 87 failing cases across 101 files (81 passed and 20
failed); the total was 1,286 cases. Failures included 61 reported test deadlines
and unavailable DOM query results in App, Settings, desktop, navigation and
configuration integration suites. There were no failed `pr-problems.test.tsx`
or `pr-fix.test.tsx` cases in its failure inventory. This is not a complete
frontend pass, and no resource cause or independently verified baseline is
claimed. Log: `/tmp/delidev-1227-eighth-frontend.log`.

The separate focused PR-problem/manual-fix selection passed all 16 cases in two
files (19.81 seconds reported by Vitest), including the new original audit
regression. Its negative control failed against the previous presentation.

The remaining explicit steps, skipped by the failed `&&` chain, each passed:

| Command from `apps/delidev` | Result | Measured elapsed seconds |
| --- | --- | --- |
| `pnpm test:bundle-dry-run` | 8 fixtures passed | 2.312 |
| `pnpm test:desktop-launch` | 16 fixtures passed | 103.814 |
| `pnpm test:widget` | Widget fixture checks passed | 27.892 |
| `pnpm build` | Production build passed | 8.387 |

Logs use `/tmp/delidev-1227-eighth-<step>.log`; the exact command/results are in
`/tmp/delidev-1227-eighth-remaining-results.json`. Builds and component fixtures
do not establish native desktop, account, platform or release acceptance.

## Git environment and build results

The native POSIX negative control failed before the repair. The environment-only
race regression passed afterward (package 2.019 seconds), including the modeled
Windows branch, exact POSIX name/value semantics and stable retention.

The broader PR Git race selection exited 1 in 306.099 measured elapsed seconds
(package 283.034 seconds). Native preparation/descendant cleanup failed the
bridge and original fork-push fixtures; the conflict merge/rebase fixture failed
cleanup and a bounded operation deadline. These failures remain qualified in the
environment record. They are not converted into a pass by the environment-only
result.

`GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/internal/workspace` and
`GOMAXPROCS=2 go build -p 1 -o /tmp/delidev-1227-eighth-cli ./cmds/delidev-cli`
passed. DevHud API-client, administrator and async-hook embed prerequisites
were built by their owning packages before Go compilation and the commit hook.

## Required workspace package race result

From the repository root:

```sh
GOMAXPROCS=2 go test -race -json -p 1 ./cmds/delidev-cli/internal/workspace -count=1 -timeout=20m
```

This command exited 1 in 1,205.392 measured elapsed seconds. The package reported
1,200.805 seconds and exhausted its aggregate 20-minute budget. There were 133
completed passing test/subtest results and one reported failing result:
`TestWorkspaceDiffUnbornAndBoundedResults` (28.28 seconds), whose unborn working-tree
read returned `recovery_required` because the workspace result did not prove its
accepted preparation.

`TestPRFirstExecutionRechecksRemoteAndPreservesOriginalPreparation/untracked`
and its parent remained unfinished at timeout. Subsequent workspace cases are
unproved; this is neither a complete workspace nor a complete Go-suite pass.
No complete CLI/server/harness/Worker Go sweep was performed in this eighth pass.
The historical broad failures remain preserved separately.

Within this failed package run, all reported manual Git/environment regressions
passed: bridge command ownership, POSIX/Windows-model snapshots, original POSIX
preflight spelling, original local launcher ownership, original-bound once-only
fork push, push-destination rewrite rejection, default merge and explicit rebase,
and external local operand rejection. The same-source later passes do not erase
the preceding focused selection's cleanup/deadline failures or establish their
cause. No security, real GitHub account or supported-platform acceptance is
inferred.

Log: `/tmp/delidev-1227-eighth-workspace-race.log`; parsed exact outcomes and
unfinished cases: `/tmp/delidev-1227-eighth-workspace-summary.json`. The frozen
source revision was `e8130258b251a84a7ff7db1314bf78c494980246`.

## CI, reviews and publication limits

All 38 reported checks on the previous pushed head
`caaa5dd44a8123c50220bcac69824298cf3b1e42` passed or were skipped (13 pass,
25 skipped). Its [CI run](https://github.com/delinoio/oss/actions/runs/36788698093)
was independently read as completed/successful with that exact head. There was
no failing CI root cause to repair. This evidence becomes historical after the
eighth pass's single final push; it does not approve the new local commits.

The two original unresolved security P1s remain:

- [Git executable/configuration verification-to-execution](https://github.com/delinoio/oss/pull/1227#discussion_r4144215820): the initial permission-profile/execution-boundary decision is still pending.
- [Companion-content publication from attacker-controlled PR evidence](https://github.com/delinoio/oss/pull/1227#discussion_r4145749300): the companion-access/reviewed-publication boundary decision is still pending.

This pass repairs neither finding and silently narrows no documented permission
or companion support. Both decisions were already requested; the heartbeat is
not an answer. The two corrected P2 threads are resolved only after the final
push is confirmed. Exact publication, thread resolution and cleanup are verified
in the chat and heartbeat checkpoint afterward. Preserve all original PR body
content and `Closes #1081`. No newly pushed-head CI result, review approval,
live-account, native-platform or release acceptance is established here.
