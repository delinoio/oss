# Local validation after repository-inspection reservation merge

## Revision

Runs on 2026-09-30 validate merge `805f098ac`, which composes main `65eca3341`
with the proxy implementation. The merge record describes the two protocol
documentation conflicts and preserves both independent contracts. This record
supplements, rather than replaces, the earlier failed broad runs and focused
validation evidence.

## Passing commands

- `GOMAXPROCS=2 go test -race -p 2` for server, CLI, domain, outbound,
  provider, inference-proxy and GitHub packages with filter
  `Network|CLINetwork|Proxy|Credential|Workflow`, `-count=1 -timeout 5m` passed.
- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` passed.
- Post-commit `pnpm proto:check` passed formatting/lint, breaking compatibility
  and regenerated Go/TypeScript freshness without tracked or untracked drift.
- The structure/protocol/breaking CI fixtures passed all seven tests.
- API client `pnpm typecheck && pnpm test` passed all 45 tests.
- Complete desktop `GOMAXPROCS=2 pnpm test` passed: 98 files and 1,264 tests,
  followed by eight bundle fixtures, 16 desktop-launch/asset fixtures, widget
  checks and the production build. A temporary two-worker Vitest cap was
  restored on exit; assertions and deadlines were unchanged. Required LFS
  assets were hydrated before consumption, and generated application/client
  dist directories were removed after validation.

## Complete Go race command

From the repository root,
`GOMAXPROCS=2 go test -race -p 8 ./cmds/delidev-cli/... -timeout 20m`
completed with exit 1: 19 packages passed and four failed. The root executable
package has no tests; the security package used its unchanged cached result.
Complete outbound, provider, inference-proxy, GitHub, store, Worker and native-wire
packages passed.

| Failing package | Observed result |
| --- | --- |
| `internal/cli` | The session creation-diff fixture reported an unavailable workspace reader; comparison details follow below. |
| `internal/harness/grok` | `TestOwnedInputRetainsClaimsAndRejectsUncertainReplay/input-text-method` reported uncertain native request delivery. The package completed in 1,168.825 seconds. |
| `internal/server` | The package reached its 20-minute limit during `TestContinuationPreservesSameTurnInputsAndEarlierQueuedInput/unaccepted`; not every server case completed. |
| `internal/workspace` | `TestWorkspaceDiffUnbornAndBoundedResults` reported a recovery-required preparation proof for the staged unborn diff. The package completed in 937.590 seconds. |

This remains a failed complete race command. Its passing native-wire rerun does
not explain the earlier race reports, and no native-wire fix was made. Historical
failures and their qualifications remain in the preceding records. The configured
package bound was retained; the corresponding Go test processes are no longer
running. No failed native outcome is reclassified as a successful operation.

## Session fixture comparison

The complete CLI package failed `TestCLISessionAcceptanceQueueAndArchive` at
`sessions_test.go:239`, reporting an unavailable workspace reader for the
creation diff. The case took 46.37 seconds; the package took 274.879 seconds.

An untouched `git archive 65eca3341` of the Go module, DeliDev command sources
and generated Go protocol sources passed the isolated case in 100.225 seconds.
Its private temporary source archive was removed after the command. The PR
branch's isolated case failed at the same diff in 30.69 seconds (package
31.406 seconds). Both isolated commands used `GOMAXPROCS=2`, `go test -race -p 1`,
`-run '^TestCLISessionAcceptanceQueueAndArchive$'`, `-count=1 -timeout 3m`.

This comparison did not reproduce the failure on untouched merged main and
does not establish its cause. Workspace implementation and this test have no
PR diff against merged main, but that inspection cannot dismiss the observed
branch-only failure. The older unchanged-main comparison reproduced a different
session-create timeout. The session-diff failure remains an explicit unresolved
validation limit; no assertion or native/observation deadline was weakened.

## Acceptance limits

Earlier-head CI does not certify this merged head. Fresh CI and Codex review
evidence remain required after publication. No real provider/GitHub account,
enterprise proxy, native credential lifecycle, Windows/Linux runtime, release
or Worker bootstrap acceptance was exercised. Controlled fixture, browser and
build outcomes remain separate from those acceptance requirements.
