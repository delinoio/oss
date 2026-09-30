# Runner Device presentation terminology

## Source and scope

- Date: 2026-09-30.
- Issue: https://github.com/delinoio/oss/issues/1136.
- Inspected main: `74701b8948694e2bf8f8ba6d07e596c2d2f358a7`.
- Implementation: `f65e1a8d4efb4b22673fff3c3e3f2420bd5ab0b1`.
- Reuses the focused source changes from closed, unmerged PR #1141 and adapts its Settings regression coverage to the independent fixture files introduced on main. This record does not rewrite the frozen historical ledger.

New session exposes its machine selector as exactly **Runs on**. Its placeholder, loading/empty/cached/unavailable inventory wording and unavailable-selection option use **Runner Device**. Existing execution-device labels across checkout and schedule selectors, remediation, Settings, prerequisites and diagnostics use **Runner Device(s)**. PR planning and schedule reconfiguration guidance use the same terminology.

The optional internal `ResourceChoice.resourceLabel` defaults to `label`; only presentation nouns change. Agent Worker, technical Worker references, user-assigned names, CLI commands, logs/error codes, authorization, schema, RPC/storage fields and the `execution-workers` Settings category remain unchanged. There is no migration or dependency change.

## Focused verification

- `pnpm exec vitest run src/session-tools.test.tsx src/App.test.tsx src/schedules.test.tsx src/prerequisites.test.tsx --maxWorkers=1 --testTimeout=30000`: 127 tests in four files passed.
- `GOMAXPROCS=2 go test -p 1 -timeout 5m ./cmds/delidev-cli/internal/server -run 'TestPRRemediationWorkspacePlanUsesCurrentExplicitSelectionWithoutDispatch|TestScheduleRPCReferencedDeletionDisablesAtomically' -count=1`: passed.
- `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...`: passed.
- `node --test scripts/ci/delidev-structure.test.mjs`: three checks passed.
- `git lfs fsck` and `git diff --check`: passed. The exact DeliDev icon source was hydrated before its asset-consuming fixtures; administrator and ach UI embeds were generated for Go validation.

Fixtures retain the original machine ID through submission, unavailable selections, later pages, Local proof/pinning and exact uncertain-creation retries. Settings navigation retains `execution-workers`; checkout, schedule and remediation fixtures retain their saved IDs and Local origin. The disconnected PR planning fixture retains `Unavailable`; schedule Resume retains `Aborted`, the original revision and disabling document bytes.

## Broader validation

The initial `pnpm test` run was interrupted after multiple short UI and integration waits failed while many independent repository test runs were active. A full serial Vitest run then passed 960 of 962 tests; the two failures were unrelated Settings waits for **First model** and **Delete Saved instructions**. The observed shared-host load average exceeded 700. These runs do not establish a complete passing frontend gate.

The complete `pnpm test` rerun passed: all 962 tests in 84 files, eight packaging fixtures, sixteen asset/desktop-launch fixtures, widget fixture checks and the frontend build. Only local validation temporarily set Vitest `maxWorkers: 1`, `testTimeout: 30000` and `hookTimeout: 300000`; the wrapper restored the original tracked configuration, confirmed by an empty diff. No timing change is shipped.

The broad `GOMAXPROCS=2 go test -p 1 -timeout 20m ./cmds/delidev-cli/...` run reported two failures outside the changed execution-copy conditions:

- `TestCLISessionAcceptanceQueueAndArchive`: the creation-comparison workspace read returned `Unavailable`.
- `TestDiscoveryVerifiesClaudeWithoutGrantingExecution`: the fixture could not confirm Claude probe cleanup and returned `recovery_required`.

Several preceding packages passed, but the remaining native package run was interrupted after sustained host contention. This broad Go gate is **incomplete and not passing**. Host contention is a plausible cause, not proof that either failure is harmless. The naming-specific server fixtures and full Go vet passed separately; CI must independently establish the broad matrix result.

The exact source change preserves native execution/read/cleanup logic. No unrelated workaround or test timeout is committed. Generated repository-owned `dist` output is removed after validation; dependency-owned outputs remain installed.

## Evidence limits

This change is copy maintenance. Deterministic fixtures and builds do not establish real harness/account/platform or production desktop interaction acceptance. Existing DeliDev acceptance gaps remain governed by their owning contracts and evidence.
