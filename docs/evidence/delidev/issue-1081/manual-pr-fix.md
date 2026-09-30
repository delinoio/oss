# Manual PR fix implementation evidence

Recorded on 2026-09-30 for issue #1081. The implementation starts from
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7` and is retained in commits
`7508c2b92ad68b5bc830d070b3427596a6182d41`,
`557d6cbd4` and `d67b34ceb031064d010df37dd844275c76d43c8d`.
The owner explicitly selected the issue's rebase default; existing explicit
merge policies retain their meaning.

## Implemented boundary

Manual Fix now, authenticated Connect RPC and equivalent CLI accept exact
original retained evidence revisions/content versions with an explicit project.
Acceptance atomically binds one stable-PR owner, eligible linked session or
explicitly configured complete PR-head workspace, original input and job.
Dispatch independently refreshes source/gates. The Codex API profile requires
explicit write permission and exposes a typed capability; other harnesses fail
without fallback. The Worker retains its native Git identity separately from
provider credentials, and the harness invokes a bounded, assignment-bound Git
bridge to edit, commit and push. Independent original remote/local/cleanup proof
is required before exact evidence versions become handled. A native success
without a push leaves them unhandled; uncertainty retains ownership.

The extension adds a service and optional historical fields without a destructive
schema migration. Scoped AGENTS and integration, workspace, desktop, protocol,
client and normative default contracts changed with the implementation.

## Executed checks

All commands below ran in the isolated issue checkout unless stated otherwise.
Fixtures use temporary state, test-owned provider responses/native scripts and
local Git repositories. They do not use user GitHub/provider credentials.

| Command | Observed result |
| --- | --- |
| `go test -p 1 ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/store -run 'TestPRFix\|TestManualPRFix' -count=1` | Passed all three packages. Covers canonical revisions, atomic acceptance/receipt replay, initial concurrent identical requests, PR ownership, retained review and approved-review comments, conflict selection, paused-session exclusion, explicit missing configuration, complete companion/primary workspace selection, and verified/unchanged/uncertain or mismatched completion proof. |
| `go test -p 1 ./cmds/delidev-cli/internal/cli -run 'TestPRFix' -count=1` | Passed six CLI receipt, source, request and capability cases. |
| `go test -p 1 ./cmds/delidev-cli/internal/workspace -run '^TestPRGitToolForkPush' -count=1 -timeout=8m` | Passed (151.425 seconds). Actual Git push to the test-owned fork, original-source proof, missing write access, changed remote head, push-only destination rewrite, no-push success, exact lease argument restriction and replay rejection. Earlier runs hit the bounded preparation timeout; no production deadline was relaxed. |
| `go test -p 1 ./cmds/delidev-cli/internal/workspace -run '^TestPRGitPushOnlyRewrite' -count=1` | Passed. Real Git configuration read rejects a matching `pushInsteadOf` and permits an unrelated rewrite. |
| `go vet -p 1 ./cmds/delidev-cli/...` | Passed, including the final repeat with added CLI/domain fixtures. |
| `pnpm proto:check` | Passed formatting, lint, pointer-only baseline breaking check and forced generation without drift. |
| `pnpm exec vitest run tests/legacy-imports.test.ts` in the API-client package | Passed both compatibility/reflection cases. |
| `pnpm typecheck` in `apps/delidev` | Passed for the final frontend source. |
| `pnpm exec vitest run src/pr-fix.test.tsx src/pr-problems.test.tsx` | Passed 12 cases across the two files, including exact decimal revisions, typed capability gating, retained uncertain requests across navigation and malformed acknowledgment after unmount. |
| `pnpm exec vitest run src/pr-remediation-history.test.tsx --maxWorkers=1 --no-file-parallelism` | Passed all seven existing history cases. |
| `pnpm test:bundle-dry-run`, `pnpm test:desktop-launch`, `pnpm test:widget`, `pnpm build` in `apps/delidev` | Passed separately after the main test script stopped at Vitest: eight bundle fixtures, sixteen desktop/asset fixtures, widget checks and production build. |

## Full-suite qualifications

The required `pnpm test` ran in `apps/delidev`. API-client generation and
frontend typecheck passed. Vitest reported 957 passing and seven failing tests
across 85 files: three existing App five-second timeouts and four settings
integration DOM-query failures. The subsequent packaging/build commands did not
run through this failed script; their separate successful execution is recorded
above. A serial rerun, `pnpm exec vitest run --maxWorkers=1
--no-file-parallelism`, passed 961 of 964 tests (84 of 85 files); only three
existing App five-second timeouts remained. These runs are failures, not a green
full frontend suite.

A control run of `src/App.test.tsx` in the primary checkout, without applying
this implementation, passed 33 of 37 tests and reproduced four five-second App
timeouts. The observed primary revision after that run was
`545b40804a918505ef9387d6658ff36e850667b1`. Its existing AGENTS edit and deleted
icon were preserved. This control supports an existing timing limitation; it
does not prove every full-suite failure has the same cause.

The required Go command, `go test -race -p 1 -timeout=20m
./cmds/delidev-cli/...`, is still running at this record's initial publication.
It has already failed the existing CLI acceptance fixture and native Grok,
OpenCode and Claude probe/stream cleanup fixtures. No race detector finding has
been observed so far. A separate unchanged-primary-checkout control of
`go test -race -p 1 ./cmds/delidev-cli/internal/harness -run
'^TestDiscoveryVerifies(Grok|OpenCode)WithoutExecution$'` reproduced a Grok
probe timeout (OpenCode passed that control run). Remaining results must be
recorded when the original suite exits; this is not a full Go pass.

## Limits and generated output

No real provider account, actual inference-driven PR remediation, Windows/Linux
native execution, release signing or packaged platform acceptance was performed.
The fixture verifies actual local Git transport and closed conflict/lease
operands, not a live GitHub rebase conflict or every supported native harness.
Other harness fix profiles and automatic remediation remain unavailable.
Repository Rust source was not modified, so root Cargo testing was not required.

The required desktop icon LFS object was hydrated before consuming it. The
API-client and frontend `dist` output was explicitly generated for consumers and
removed after checks; generated output and private logs are not tracked.

## Current-main integration repair

The PR base advanced to `9d110ced702e66bb50974c5ec98e830b86adbe5b` after
publication. A normal merge preserved both new Activity contracts and manual-fix
contracts. Manual completion now retains the upstream dedicated Activity proof
in the same transaction as its exact handled versions, only after original
native, cleanup and push verification. Dismissal and unverified outcomes cannot
create this record. `go test -race -p 1 ./cmds/delidev-cli/internal/store
-run 'TestPRFix|TestPRActivity' -count=1` passed after the merge; new assertions
check the exact original proof references and absence for every unverified or
dismissed scenario. This controlled evidence is not real native/account
acceptance.

## Initial identical-request repair

A later focused race run reproduced two initial identical requests colliding in
the existing exclusive provider inspection before receipt publication. The
plain earlier fixture pass did not establish this concurrent boundary. Exact
actor/input request coalescing now occurs before provider reads; waiters re-read
the durable receipt. Ownership is cancellable and bounded to 64 original
requests, while accepted receipt replay bypasses that capacity. Different actors
or input cannot share an original request. This gate retains no native grant.

`go test -race -p 1 ./cmds/delidev-cli/internal/server
-run 'TestPRFix|TestManualPRFix' -count=5` passed (17.691 seconds), including all
three retained problem scenarios and the new exact/capacity/cancellation cases.
The fix is retained separately from the base-merge repair.
