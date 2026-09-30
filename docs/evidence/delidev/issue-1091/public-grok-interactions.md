# Public Grok original tools, questions and Plan

Issue: [#1091](https://github.com/delinoio/oss/issues/1091). Inspected base: `74701b8948694e2bf8f8ba6d07e596c2d2f358a7`. Pinned native profile: Grok Build `1.0.41`.

## Implementation boundary

Original typed Read/Write/question/Plan events now share durable public Worker/server Resource publication, authenticated response claims, session/Inbox controls and existing CLI response documents. Native Read has no client permission request in the pinned profile; its automatic permission cycle is retained without invented denial. Write once/session/rejection and native question/Plan outcomes retain their original semantics. Original Plan revision/entry/Write/content ownership is preserved; no common synthetic Plan gate is introduced. Native response transport, original result acceptance, root terminal and process/workspace cleanup remain distinct. Lost publication acknowledgments repeat only exact receipts, never native replies.

Existing first-text successful history and original Stop remain separately tested. Repositories, continuation, unsupported tools, richer native Stop proof, real account/platform/release acceptance and the full issue #964 completion boundary are not established by this change. All ordinary regressions use temporary private state and controlled fixture processes without user credentials, provider inference or external publication.

## Executed validation

All checks below ran in the isolated issue worktree on macOS arm64. Focused native tests use temporary private state and controlled fixture processes. No real provider credential or account was used.

| Check | Result |
| --- | --- |
| Root `pnpm install --frozen-lockfile`, exact-path Git LFS hydration of the desktop icon | Passed; the consumed desktop icon is hydrated. |
| API client build, desktop `pnpm typecheck` | Passed after the final frontend changes. |
| Root `pnpm proto:check` | Passed lint, breaking and reproducible generated bindings. This change adds Resource JSON variants without changing the protobuf service/schema. |
| Original tool projection and public first-input fixtures | Mixed Read/Write/question and approved/cancelled/abandoned/revised Plan projection passed. Controlled Write allow/reject, native Plan and existing first-text closure passed in focused runs. A later combined race rerun failed three cases during original API initialization before public input; those deadline/uncertain-delivery failures are retained. |
| Transactional public Write response regressions, with Go race detection | Passed once/session/rejection, exact receipt replay, competing responses, wrong response kind, stale revisions, Stop and Worker-instance revocation. |
| Transactional original questions, Plan revisions/transitions and initial Plan completion, with Go race detection | Passed answer/notes, exact original revision ownership, approved/cancelled/abandoned transitions, and initial Plan terminal/report without a common approval gate. |
| Worker original-response boundary and extra-publication regressions, with Go race detection | Passed one native reply after a lost delivery receipt, competing control, lost claim acknowledgment, foreign claim rejection, Stop/revocation and uncertain-delivery retention. |
| CLI original question/approval response parity, with Go race detection | Passed original response documents and exact receipt replay after native closure. |
| Public tools terminal/report regressions, with Go race detection | Passed original aggregate validation, lost terminal acknowledgment and independently verified process/workspace cleanup reporting. |
| Root `go vet -p 1 ./cmds/delidev-cli/...` | Passed. |
| `go build -p 1 -o /private/tmp/delidev-1091-validation-cli ./cmds/delidev-cli` | Passed with task-private build/module caches. |
| Focused desktop original-interaction and existing native Grok tests | Passed all 101 tests after exact option and aggregate display validation. |
| Desktop bundle dry-run, desktop launch, widget and production build checks | Passed independently: 8 bundle tests, 16 launch tests, widget check and production build. |
| Required root `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/... -timeout=30m` | Still running at this evidence update; existing CLI workspace, discovery, Claude cleanup and Codex steer fixtures have failed their deadlines/reconciliation checks. This is not a passing full-suite result. |
| Required desktop `pnpm test` | Executed repeatedly. The run with a private build cache passed 969/970 unit tests and failed the existing tray test's five-second timeout; that test passed separately with a 20-second budget. The final run with both private Go caches passed 84 files/969 tests and failed the existing settings-preferences integration assertion waiting for its create button. That scenario passed in the earlier focused settings rerun. Packaging phases passed independently. Neither run is a passing full-script result. |

The first full frontend run found a question cancellation defect, which was corrected. A subsequent default run passed 80 files/961 tests and failed 5 files/8 tests. Serial unit validation passed 83 files/967 tests with two existing settings integration hook timeouts; both settings tests passed separately with a 60-second hook budget. No test source timeout was weakened. A later complete attempt passed 957 tests but lost fixture initialization when shared Go build-cache objects and the pinned toolchain compiler disappeared during concurrent work. Task-private `GOCACHE` and `GOMODCACHE` were then used for stable subsequent compilation. Process inspection showed multiple concurrent native suites and compilers; host contention is an inference for deadline failures, not proof that every failure is unrelated to this change.

The full-suite failures remain visible. No unperformed native/account/platform/release acceptance, unsupported richer Stop terminal, continuation, or broader issue #964 completion is inferred from parser, transactional or controlled-process results.
