# Manual PR fixes: same-account Codex fork reconciliation

Date: 2026-10-01 (Asia/Seoul); 2026-09-30 UTC.
PR: [#1227](https://github.com/delinoio/oss/pull/1227).
Revision: the enclosing merge commit, with parents
`52e7c6866907ad0181cc70720e0eaf30d40007ab` and main
`6c749670727b30679e722821846bc8dc00f5ac32`.

GitHub reported new conflicts after same-account Codex forks (#1224) landed.
The twelve conflict regions include two Go sources. Assignment validation keeps
the independent manual-fix Execute/Codex/explicit-write requirement and the fork
version-3 boundary, mutually exclusive with ordinary v1 and continuation v2.
Worker initialization verifies the child checkpoint and selects its original
Codex home before adding the independently owned PR Git environment. Neither
operation replaces the other. Original cleanup, command ownership and push
verification remain in place.

Instruction and session/workspace conflict additions retain both complete
ownership blocks. The desktop Activity paragraph retains the implemented
manual-fix verifier statement; the complete Fork presentation section follows it.
The stale sentence saying verification creation remained unimplemented is not
reintroduced. Other non-conflicting main changes remain, including child-owned
fork lifetime and the already allocated fork protocol capability/assignment.
Protocol outputs were regenerated from reconciled schemas without allocating
new shared numbers or migrations. Historical evidence remains unchanged.

## Executed verification before the merge commit

Required Go embed outputs were explicitly rebuilt: DevHud API client,
administrator assets and async-commit-hook embedded assets. Protocol generation
and `pnpm proto:check` passed formatting, lint, breaking compatibility and
generated freshness without unstaged binding drift.

The focused race selection is
`GOMAXPROCS=2 go test -race -p 1 ./internal/domain ./internal/worker
./internal/server ./internal/workspace ./internal/harness/codex
-run 'Fork|PRFix|ManualPRFix|PRRemediation|Continuation' -count=1 -timeout=15m`
from `cmds/delidev-cli`. The two directly reconciled Go owners passed: domain
2.334 seconds and Worker 9.162 seconds. Server failed
`TestPRRemediationWorkspaceReadBindsOriginalCandidateAndExclusiveProof` during
workspace fixture preparation: the matches case timed out, and different/pause/
unlink returned invalid local-branch diagnostics. The server package finished
with failure in 421.840 seconds. Remaining workspace/Codex results and the
required complete-suite attempt belong to a separate final validation record;
this record does not claim the combined selection passed.

Required frontend `pnpm test` failed: 1,150 passed, 125 failed and 10 skipped
cases across 101 files, with API-client build and type checking passing first.
Vitest duration was 279.15 seconds; the complete command took 314.809 seconds.
Failures include deadlines and DOM waits. Several other local suites were
observed running concurrently, which is a possible explanation rather than a
verified cause or baseline dismissal. Follow-up client, focused UI and explicit
packaging/build outcomes are retained separately. No full frontend or Go pass,
native/account/platform/release acceptance or current-head CI approval is claimed.

## Separate review work

The new preflight retry finding is evaluated separately after this merge commit.
The original executable/configuration P1 and companion-content P1 remain
unresolved, with their human decisions unanswered. This reconciliation does not
alter full-access/companion support or fix either security boundary. Fresh CI and
review observation belongs to the next heartbeat after the final repair push.
Repository-owned generated dist output is removed at completion; dependencies
and caches are preserved.
