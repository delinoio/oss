# Manual PR fixes: managed-restore main reconciliation

Date: 2026-10-01 (Asia/Seoul); 2026-09-30 UTC.
PR: [#1227](https://github.com/delinoio/oss/pull/1227).
Revision: the enclosing merge commit, with parents
`fc614326bd8437852c256928667ab0f009457560` and main
`9efb1917e0127a9223cee0969877238ab37c0e1d`.

GitHub reported new merge conflicts after managed backup restore (#1222) landed.
The six conflict regions were documentation/instruction additions at the same
insertion points. Their resolution retains both complete source-backed blocks:
manual-fix RPC/CLI ownership, exact-evidence and push/cleanup authority, Worker-only
Git credentials, generated service compatibility and the new restore receipt,
deletion, lifecycle and quarantine requirements. The project index retains both
cross-domain invariants. There was no source-code conflict to resolve.
An explicit comparison against both index conflict stages confirmed that each
resolved file retains every nonblank source line from both versions.

Main's restore implementation preserves current deletion/revocation authority,
pauses/quarantines historical work and binds original external receipts. The
composed Store.Mutate still returns a callback error before receipt insertion,
commit and watcher notification. Protocol outputs were regenerated from the
reconciled sources; no shared number or migration was invented or reassigned.

Historical records remain unchanged. The DeliDev icon consumed by bundle fixtures was
already hydrated. Pointer-only DevHud/Forge assets were not consumed by these
DeliDev checks. Required Go embed outputs were explicitly rebuilt before Go
validation and hooks.

## Executed validation

- `pnpm proto:generate` passed. `pnpm proto:check` passed formatting, lint,
  breaking compatibility against freshly fetched main `9efb1917` and generated
  freshness; regeneration introduced no unstaged binding drift.
- Required embed builds passed: `pnpm --filter @delinoio/devhud-api-client build`,
  `pnpm --filter devhud-admin build` and
  `pnpm --filter async-commit-hook build:embedded`.
- `pnpm test` in `packages/delidev-api-client` passed all 46 tests in five files
  (30.40 seconds), including the merged restore wire fixtures.
- Required `pnpm test` in `apps/delidev` failed. API-client build and TypeScript
  checking passed before Vitest; Vitest passed 1,188 cases and failed 93 across
  22 of 100 files (214.13 seconds). Failures included explicit deadlines and DOM
  waits. Its subsequent packaging/build steps did not run because the script
  stops on the first failed step.
- A diagnostic `GOMAXPROCS=2 pnpm exec vitest run --maxWorkers=2` also failed:
  1,251 cases passed and 30 failed across six of 100 files (363.04 seconds).
  The failing files were App, Settings, Models, Projects, Settings lifetime and
  Agent configuration; most failures were five-second deadlines. Other local
  test/build processes were observed concurrently. Resource contention is a
  possible explanation, not a verified baseline comparison or a dismissed
  failure. Neither attempt establishes a full frontend pass.
- The remaining frontend steps were executed separately and passed:
  `pnpm test:bundle-dry-run` (eight fixtures), `pnpm test:desktop-launch`
  (16 fixtures), `pnpm test:widget` and `pnpm build` (production Rsbuild output).
- `pnpm exec vitest run src/pr-fix.test.tsx --maxWorkers=1` passed all six
  manual-fix UI cases in isolation (1.35 seconds). This focused result does not
  replace either failed broad frontend run.
- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/... -count=1
  -timeout=20m` completed with exit 1 (3,762.819 seconds). The CLI package failed
  `TestCLISessionAcceptanceQueueAndArchive`: its review-create command returned
  an unavailable workspace reader with retained recovery-required diagnostics.
  Workspace failed `TestWorkspaceDiffUnbornAndBoundedResults` with a
  recovery-required preparation proof, then exhausted its aggregate 20-minute
  package budget while `TestPRFirstExecutionRechecksRemoteAndPreservesOriginalPreparation`
  was running. The timeout stack was in original process startup/Unix-socket
  acceptance during PR preparation. Remaining workspace cases are not proved
  by this incomplete package run. All other reported race packages passed,
  including server (610.964 seconds), store (234.331 seconds), Worker
  (158.832 seconds) and Grok (935.610 seconds). No complete Go-suite pass is
  established, and the workspace failure is not dismissed as a known baseline
  issue or a security fix.
- `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...` passed (70.360 seconds).
  `GOMAXPROCS=2 go build -p 1 -o /tmp/delidev-1227-sixth-cli
  ./cmds/delidev-cli` passed (24.400 seconds).
- After the broad run's CLI workspace-reader failure,
  `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/cli
  -run '^TestCLISessionAcceptanceQueueAndArchive$' -count=1 -timeout=3m`
  passed in isolation (package duration 89.658 seconds). The broad failure is
  retained; this diagnostic run does not convert it into a full-suite pass.
- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/server
  ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/cli
  -run 'Backup|Restore|PRFix|ManualPRFix|PRRemediation|SessionDeletion'
  -count=1 -timeout=20m` passed in 199.586 seconds. This selection exercises
  both restored authority/deletion/receipt gates and manual remediation behavior
  in the reconciled tree. It ran while the full race attempt remained active.

## Limits and review blockers

The original executable/configuration verification-to-authenticated-execution P1
and the companion-content security P1 remain unresolved. Their separate human
permission/execution-boundary and companion-access/publication decisions have
no answer. This reconciliation does not fix either finding, remove documented
full-access/companion support or establish native/account/platform/release
acceptance. Prior-head CI success does not approve this new merge head; fresh
CI/review observation belongs to the next heartbeat after the final push.
Repository-owned generated dist output is removed at completion; dependency
caches are preserved.
