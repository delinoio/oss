# Ninth manual PR-fix maintenance: explicit server outbound routing

## Revisions and restoration

PR #1227 entered `DIRTY` / `CONFLICTING` on pushed head
`b787b03ea252ac7ce12a92df34af70516f035a88`. The ninth explicit repair-pr pass
merges main `ef5dbf8974ee69fb262cf66fc174f91e97a0e86f` (issue #1084 outbound
proxy configuration) into the existing `kdy1/fix-1081-manual-pr-fixes` branch.
There is no rebase, new branch, new PR or merge of the GitHub PR.

The recorded checkout had only `.gomodcache` and was absent from Git's worktree
registry. Its existing local branch still matched the pushed head. The cache-only
directory was renamed atomically to a sibling, the same existing branch was
restored at the original path with LFS smudging initially disabled, and the cache
was returned intact. `gh pr checkout` then verified that branch/head; the checkout
was clean before the merge. Required LFS assets were subsequently hydrated and
all fourteen tracked LFS paths were independently observed with full objects.
`git lfs pull` reported an index-update error while merge stages were present;
that warning is not erased by the separate full-object observation. Root frozen
pnpm installation restored dependencies and hooks without changing the lockfile.

## Conflict composition

Thirteen files conflicted: ten source/instruction files and three generated
facades. Both authenticated service registrations and public forwarding schema
imports remain. Scoped CLI/server/store/protocol/client instructions retain
manual-fix authority alongside explicit network-profile authority. Manual
preflight backoff, once-only push/native ownership, Activity verification,
account switching, ALLGREEN evidence and managed-restore safety remain present.
The new server route does not grant Worker Git authentication or publication.
Network profile/route/capability values were already reserved on main; no new
allocation, SQL migration or dependency was introduced by this reconciliation.

The integration contract retains the implemented original-assignment manual-fix
caller and adds explicit outbound routing. Main's earlier fixture-only paragraph
is recorded verbatim below as historical baseline evidence, rather than asserting
that the existing manual-fix caller has disappeared:

> The retained Activity contract now publishes metadata-only original problem observations, local dismissals and semantic remediation attempt transitions atomically with their sources. Stable remote PR ownership, exact receipts and unchanged alias collections preserve one history. Failed/uncertain/successful attempts remain distinct from verified handling. The private `RetainPRHandlingVerification` boundary accepts independently verified exact versions and a private proof commitment, preserving the first record for an identical proof. It is fixture-tested but has no production/public caller; implementing the independent verifier, handled/resolved mutations and controller still requires their separate composition and acceptance. There is no retrospective backfill or inferred success. See [retained activity](cmds-delidev-activity-contract.md) for identity, actor, pagination, session cleanup and content boundaries.

The current production caller is `internal/store/pr_fix.go`, which invokes
`RetainPRHandlingVerification` only after the existing exact handled-version and
original completion/push proof gates. Other verifier/controller composition and
native acceptance remain separate. The historical paragraph is not current
absence-of-caller evidence. All other nonblank conflicting source content was
retained, with blank separation for independent Markdown rules. Buf and the
compatibility generator rebuilt all tool-owned outputs from the reconciled
schemas; neither parent's generated conflict was selected by hand.

## Focused validation before the merge commit

All commands ran in the restored checkout with its retained `.gomodcache`.

- `pnpm proto:generate` succeeded; formatting/lint and main-relative breaking
  checks passed (1.932 and 3.392 measured seconds respectively).
- DeliDev API-client lint, all 47 tests in six files, and build passed. Vitest
  reported 21.37 seconds; the measured test command took 22.953 seconds.
- Required administrator and async-commit-hook embed builds passed.
- `go test -race -count=1 -timeout=20m` over domain, store, server, outbound,
  providers, GitHub integration, API proxy and CLI, selecting
  `Network|ManualPRFix|PRFix|PRRemediation|ALLGREEN|AccountSwitch|ProxyJoinsCancellation`,
  passed all eight packages in 80.617 measured seconds. Server reported 39.760
  seconds; store 17.947 and CLI 19.791. This is a focused selection, not a
  complete-package or complete Go-suite pass.
- `go vet ./cmds/delidev-cli/...` and the CLI build passed (2.414 and 0.837
  measured seconds). The executable was written only under `/tmp`.
- No conflict marker remains in the thirteen original conflicting files.
  `git diff --check HEAD` reports the generator-owned new blank line at EOF in
  `network_pb.ts`, inherited from main and reproduced by the pinned generator.
  Generated code was not hand-edited to remove it.

The required complete DeliDev race attempt and post-commit generation freshness
are recorded separately once completed. No new Rust or app frontend source was
changed in this merge, and no native/account/platform/release acceptance follows
from these fixtures or builds. Earlier failed broad validations remain preserved.

## Review and CI boundary

Initial inventory showed 38 checks on the prior pushed head (13 passed,
25 skipped), no failed check and no review approval. That CI is prior-head evidence
once this merge is pushed. The original Git executable/configuration P1
`PRRT_kwDORRAKg86ng9G8` remains unresolved and non-outdated with no new reply or
human profile decision. The companion-data P1 `PRRT_kwDORRAKg86nkvJ_` was
independently observed resolved by `kdy1`, non-outdated, with its one original
comment and no new reply. This repair did not resolve it, implement a reviewed
publication boundary, narrow companion/full-access support, or establish a
security fix. Neither previously requested human decision was supplied by this
heartbeat. No unchanged blocked security repair was retried.
