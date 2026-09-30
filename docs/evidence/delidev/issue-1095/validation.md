# Issue #1095: managed Codex subscription validation

The replacement implementation starts from main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` and adapts the unmerged implementation from [closed PR #1124](https://github.com/delinoio/oss/pull/1124) to the current service-specific schemas, CLI/server file ownership, Worker capability allocation and independent evidence structure. The previous branch observations are preserved separately in [historical evidence](previous-pr-1124.md).

All authentication fixtures use synthetic JWT/token material, temporary private runtime state and controlled native subprocesses. No user login or real provider account is imported.

## Checks executed on the replacement

Original implementation revision: `39ce1b5014a5f219a1c7904340ffc1d8a4e777f9`. The later protocol compatibility repair is recorded separately below.

- `go test ./cmds/delidev-cli/... -run 'Subscription|Managed|Bundle' -count=1`: passed, including protected Connect/SQLite lifecycle, concurrent leases, cancellation/revocation, bundle rotation, lost write-back, native process fixtures and execution authentication cleanup.
- `go vet ./cmds/delidev-cli/...`: passed; the final implementation including capability coexistence was also checked with `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` and passed.
- API client `pnpm test`: 44 tests passed. The first run hit the integration fixture's two-minute Go build deadline during concurrent machine compilation; the warmed-cache retry passed all four test files.
- API client `pnpm typecheck`: passed.
- `pnpm proto:check`: passed, including Buf formatting/lint, FILE/package breaking compatibility against the fetched baseline, allocation-compatible capability value 3, and generated-source freshness.
- `go fmt ./...`: passed after explicitly generating both Go embedded app asset prerequisites.
- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/harness/codex ./cmds/delidev-cli/internal/subscription ./cmds/delidev-cli/internal/cli -run 'Subscription|Managed|Bundle' -count=1`: all five packages passed, including capability coexistence and duplicate rejection.
- Required full `go test -race ./cmds/delidev-cli/...`: failed during concurrent machine load. CLI/workspace/process/native discovery failures and ten-minute package timeouts were reported across Claude, Codex, Grok, server, Worker and workspace tests. No full-suite pass is claimed.
- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/cli ./cmds/delidev-cli/internal/harness -run '^(TestCLISessionAcceptanceQueueAndArchive|TestDiscoveryVerifiesGrokWithoutExecution|TestDiscoveryVerifiesOpenCodeWithoutExecution)$' -count=1`: both discovery tests passed in the isolated package rerun; the CLI scenario still timed out at `sessions_test.go:202` (`session create --wait`, workspace preparation remained claimed).
- The same isolated CLI scenario also failed at `sessions_test.go:202` on a disposable archive of unchanged main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` (73.26-second test, 76.081-second package), reproducing the existing preparation deadline without the subscription implementation. This establishes only that specific baseline failure, not that every full-run failure is unrelated.
- The complete adapter/process rerun, `GOMAXPROCS=2 go test -race -p 1 -timeout=20m ./cmds/delidev-cli/internal/harness/codex ./cmds/delidev-cli/internal/process -count=1`, failed. Codex reported native handshake/delivery/approval failures and timed out after twenty minutes (1201.269-second package), while `TestContinuationInvalidHistoryNeverAuthorizesResend/duplicate-item` was active. The process package failed `TestCancellationAndForeignOwnerRecovery` with "canceled child did not stop" (76.555-second package). These complete-package checks are not reported as passing; no unchanged-main comparison was executed for these additional failures.

- `node --test scripts/ci/delidev-proto.test.mjs scripts/ci/delidev-structure.test.mjs`: all six allocation, descriptor relocation and structural compatibility checks passed.

## Acceptance limits

These fixtures establish deterministic protocol and ownership behavior, not real subscription OAuth, hosted inference, installed-Codex subscription execution, desktop login controls, full uncertain-lease recovery, native Windows/Linux runtime acceptance or release readiness. Complete issue #964 remains independent and unfinished.

## First PR maintenance repair

Main `7f356266f` was merged in `959c4070`, retaining its AI API Keys terminology and Diagnostics presentation together with the managed subscription contract. No Go implementation or schema change came from that base merge.

PR #1174's protocol job and macOS/Ubuntu Go jobs all reported the same aggregate-reflection omission: the compatibility generator took its service inventory from the historical declaration relocation map, excluding the new subscription schema. The repair derives both Go and TypeScript aggregate files from compiled `delidev.proto` public imports, preserves historical declaration order, and leaves the relocation map unchanged. TypeScript coverage now verifies canonical subscription declaration identity and reconstructed service reflection.

Executed against the repaired generated output:

- `GOMAXPROCS=2 go test -p 2 ./protos/...`: passed, including the previously failing aggregate reflection test.
- API client `pnpm test` and `pnpm typecheck`: all 44 tests and typecheck passed.
- `node --test scripts/ci/delidev-proto.test.mjs scripts/ci/delidev-structure.test.mjs`: all six checks passed.
- `pnpm proto:lint` and `pnpm proto:breaking`: passed.

Repair source revision: `979f8b44fe7f320be2972aff8c1ca4752a40df14`. The same five-package focused subscription race command and `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` passed again after regeneration. The repository pre-commit Go formatting hook also passed.

The post-repair `pnpm proto:check` completed format/lint and breaking checks, then its generation step failed because a referenced `container/heap` archive was missing from the shared Go build cache. A second generation through `GOCACHE=<temporary task cache> GOMAXPROCS=2 pnpm proto:generate` passed. The normal freshness assertions (`git diff --exit-code` over all four generated-source roots and no untracked generated files) then passed with no output changes. This isolated retry did not change repository configuration or the user's shared cache.

These focused repair checks do not supersede the complete native-suite failures or real-account acceptance limits above. New-head CI and review remain separate evidence after publication.

## Initiator revocation review repair

The non-outdated Codex finding [on queued initiator revocation](https://github.com/delinoio/oss/pull/1174#discussion_r4141950170) was reproduced: queued login, refresh and logout all retained their pending operation after the initiating paired client was revoked. The new race regression failed all three queued cases before the fix.

Client revocation now cancels its still-queued operations in the same transaction as credential revocation. It changes neither protected generations nor accepted logout health and preserves claimed/native leases. New authorized requests can proceed, while the original operation cannot grant authority and receipt replay cannot cancel a replacement operation from another initiator. Six queued/claimed cases pass under the race detector.

Validation base: `631b9d5d02ea5f3e35133943bb979598b6586db6`. Tested source blobs: `devices.go` `bb870b4a78863d45cddf37371dc6401694aeed81`, `subscriptions.go` `50fbe00b6125991b7fbab0e71a37c17d7acf7008`, and `subscriptions_test.go` `40e5ad7f967305fb321a1950664f7a580b4e898e`. The same five-package `Subscription|Managed|Bundle` race command passed after this fix, followed by `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...`.

The first new regression attempt could not build because the shared Go 1.26.8 compiler/vet files disappeared. Both the reproducing run and passing repair checks used an isolated temporary extraction of the already downloaded exact Go 1.26.8 archive (`GOTOOLCHAIN=local`), whose version was confirmed. No repository runtime pin or user credentials changed. The previously recorded complete native-suite failures remain unresolved and are not replaced by these focused passes.

## Logout completion race follow-up

A separate repair based on `12a4ef0a14c25828a736034cfa40c4e1970bda65` covers native execution completion racing cancellation of its initiator's queued logout. The protected Connect regression initially failed because successful bundle write-back restored Ready after revocation had removed the pending logout. The completion path now preserves Revoked health while retaining the newest generation; a fresh authorized logout still acquires the account and completes protected cleanup.

Tested source blobs: `subscriptions.go` `b7a7c5c964141d717e36f268bba2cb2a4e3d5c28` and `subscriptions_revocation_test.go` `86b32aaff045a33b90d204e613c79b0028be44ea`. The five-package focused `Subscription|Managed|Bundle` race command and `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` passed using the same isolated exact Go 1.26.8 toolchain. The regression verifies original execution-lease retention, completion write-back remaining revoked, and fresh logout reaching Disconnected without a retained generation. Complete native-suite and real-account limits above remain unchanged.

## Frontend CI acknowledgment wait repair

The [new-head protocol/client CI job](https://github.com/delinoio/oss/actions/runs/36686041320/job/109792311748) passed protocol reflection and API-client checks, then failed the singleton server-preferences integration test at its default one-second DOM wait for the save acknowledgment. The page still displayed the disabled Saving action when that wait expired; 1,012 other frontend tests passed on that runner.

The regression now delays only the temporary fixture's successful ConfigurationService save replies by 1,100 milliseconds. With the original one-second waits, that controlled delay reproduced the same missing Edit Server preferences assertion. Five-second waits after both saves then passed while retaining every real-server singleton, exact-default, revision and edit assertion. The overall fifteen-second test deadline remains bounded. No production UI behavior or configuration changed.

Validation base: `0f6e33829fdd6b854d18e677c1e9f93d875f94dd`. Tested `settings-preferences.integration.test.tsx` blob: `4b9bf44db1910fd0d5e51a8e5e7632a870b2f6d9`.

- `pnpm exec vitest run src/settings-preferences.integration.test.tsx`: passed with the controlled delayed acknowledgment (9.49-second test).
- Required `pnpm test` from `apps/delidev` on macOS arm64, using the isolated exact Go 1.26.8 toolchain for owned Go fixtures: passed. This includes API-client generation, frontend typecheck, 85 Vitest files / 998 tests, eight bundle dry-run checks, sixteen desktop-launch/asset checks, widget fixture checks and the production build.
- The exact-path LFS icon was hydrated through `pnpm prepare:assets` before the frontend command. Generated app/client `dist` outputs are removed after validation.

The successful frontend command does not establish real subscription authentication, packaged native desktop acceptance or a full DeliDev native-suite pass. The earlier native-suite failures and acceptance limits remain visible above; new-head CI and review after this repair are separate pending evidence.
