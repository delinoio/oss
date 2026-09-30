# Issue #1136 execution-device terminology

Date: 2026-09-30. Base inspected: `ad0e3e9a29cb3d8375ab5d168bb160c35a023250` (`origin/main` fetched before implementation). Implementation revision: `8eaf3512c` (`fix(delidev): use Runs on and Runner Device presentation terms`).

## Implemented boundary

New session's machine selector is visibly and accessibly named `Runs on`. Its optional `ResourceChoice.resourceLabel` is `Runner Device`; placeholders and inventory status nouns use that resource label, while the existing visible/accessibility label and read-problem key retain `label`. The optional property defaults to `label`, and the existing `emptyLabel` override still takes precedence. The MACHINE display noun and unavailable-identity fallback use Runner Device.

Checkout/schedule/remediation selectors, schedule creation help, Settings navigation/back/help, diagnostics heading/help and the prerequisite step use Runner Device(s). The PR planning failure and schedule reconfiguration guidance use the same terminology. No execution condition, error classification, authorization, RPC/storage identifier, CLI command, structured log, user-assigned name, Agent Worker term or generic technical Worker term is changed. The `execution-workers` category value remains intact.

The desktop/diagnostics contracts, project naming invariant and desktop/CLI scoped instructions define the naming boundary. The historical ledger is preserved.

## Verification

- `pnpm install --frozen-lockfile` at root passed and installed the linked-worktree hooks. The DeliDev icon LFS object was hydrated; `git lfs fsck` passed.
- `go vet ./cmds/delidev-cli/...` at root passed.
- `GOMAXPROCS=2 go test ./cmds/delidev-cli/internal/server -run 'TestPRRemediationWorkspacePlanUsesCurrentExplicitSelectionWithoutDispatch|TestScheduleRPCReferencedDeletionDisablesAtomically' -count=1` passed. The original planning fixture now exercises a stale disconnected Worker and verifies the exact Runner Device message with Unavailable classification. The referenced-deletion fixture verifies the exact Runner Device guidance, Aborted RPC classification and unchanged retained disabling state after failed Resume.
- `go test ./cmds/delidev-cli/...` at root ran and failed. CLI session creation reached its existing operation timeout; Claude, Codex, Grok, server, Worker and workspace packages reached the default ten-minute package deadline, with additional timed fixture failures in OpenCode and workspace/interaction tests. The affected source outside the two presentation messages is unchanged. These broad failures remain visible and are not reported as passes.

Frontend validation runs on a shared macOS host with concurrent repository checks. The first type-check attempt caught an unsupported Testing Library query option in new tests; that test-only option was corrected. Parallel and one-worker attempts exposed existing short UI waits. Final validation temporarily uses one Vitest worker, 30-second test and 90-second hook defaults, a 15-second Testing Library asynchronous wait, and `GOMAXPROCS=2`. Assertions and production behavior are unchanged; both committed validation configuration files were restored before this evidence commit. Tests with explicit timeout overrides retain them.


### Frontend results and baseline comparison

- `pnpm test` from `apps/delidev` completed client preparation and TypeScript checking, then reported 1,238 passing tests and one failure across 95 files. Its single failure was the new pagination assertion loaded before its correction: a Connect router handler receives a request and context, while the original matcher expected only one argument. The corrected predicate examines only the request, waits for pagination to be enabled and retains the original selected machine ID. No production logic changed to correct this test.
- A fresh `GOMAXPROCS=2 pnpm exec vitest run src/session-tools.test.tsx` passed all ten tests after that correction, including exact Runs on naming, Runner Device placeholder/loading/empty/unavailable copy, machine ID retention through pagination, Local pinning and immutable uncertain creation retries.
- `GOMAXPROCS=2 pnpm exec vitest run src/settings.test.tsx -t 'complete grouped navigation|checkout|global routing and fetch'` passed all three selected tests (46 skipped). These verify the preserved `execution-workers` category values, checkout ownership and remediation selections/preferences using the renamed labels.
- The earlier five-file focused run (`session-tools`, `App`, `doctor`, `prerequisites`, `schedules`) reported 190 passes and two failures: the subsequently corrected pagination matcher and an existing App test's explicit 15-second deadline. This run passed first-message creation with the original `machine_id`, diagnostics heading/guidance, prerequisites and schedule/Local/retry checks. Neither aggregate run is reported as completely green.
- A final `pnpm --filter delidev-desktop typecheck` passed after restoring the committed test setup and Vitest configuration.
- Because the broad test command stops at the failed assertion, the remaining package checks were run explicitly: `pnpm test:bundle-dry-run`, `pnpm test:desktop-launch`, `pnpm test:widget` and `pnpm build`. All passed: eight bundle tests, sixteen asset/launch tests, widget fixtures and the production Rsbuild bundle.
- The initial broad Go run's CLI failure was rechecked separately and reproduced at session creation. A temporary `git archive` of untouched base `ad0e3e9a29cb3d8375ab5d168bb160c35a023250` also failed `GOMAXPROCS=2 go test ./cmds/delidev-cli/internal/cli -run '^TestCLISessionAcceptanceQueueAndArchive$' -count=1`, this time on an Unavailable session-file read after creation. This demonstrates baseline timing/availability failure in the same fixture, not an identical failure step or proof that every broad Go failure has the same cause. The shared host's observed load average exceeded 600. Broader local Go acceptance remains unverified.

The implementation commit passed the installed pre-commit Go formatting hook without bypasses. Root administrator and async-commit-hook embedded assets were explicitly generated for that hook's repository-wide Go package discovery; they are validation prerequisites only.

## Evidence limits

Component fixtures and copy/server regression checks do not establish native CEF/platform acceptance, packaged deployment, harness/account readiness or new execution behavior. This change does not alter geometry, dependencies, protocols, migration versions or native adapters. Generated repository-owned `dist` outputs are validation inputs only and are removed from the final worktree.
