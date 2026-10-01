# Combined local validation

## Revision and scope

Validated merge `bcf96a51e` combines the outbound-proxy implementation with main
`574c1a92c957fc741a723ff8123888dad32a2194`. Runs below took place on 2026-09-30.
This record supplements the earlier implementation, independent-TLS and merge
records; it does not replace their historical results or qualifications.

## Passing checks

- Focused Go race tests for network routing, CLI parity, native accounting and
  Grok accounting passed across the selected server, CLI, domain, outbound,
  provider, inference and GitHub packages. The command used `GOMAXPROCS=2`,
  `-race -p 2 -count=1 -timeout 5m` and filter
  `Network|CLINetwork|NativeAccounting|GrokClosed|GrokAccounting|AccountingProfile`.
  The selected store package had no matching tests; this filter is not evidence
  of a complete store-suite pass.
- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` passed.
- `pnpm proto:check` passed after the merge commit: formatting/lint, breaking
  compatibility against main and regenerated Go/TypeScript freshness. Generated
  bindings have no tracked or untracked drift.
- The DeliDev API client passed `pnpm typecheck && pnpm test` (45 tests).
- `node --test scripts/ci/delidev-structure.test.mjs
  scripts/ci/delidev-proto.test.mjs scripts/ci/proto-breaking.test.mjs` passed
  all seven fixtures.
- After the complete frontend run below, `GOMAXPROCS=2 pnpm exec vitest run
  src/App.test.tsx src/settings-configuration.integration.test.tsx
  src/settings-usage.integration.test.tsx
  src/settings-preferences.integration.test.tsx --maxWorkers=1` passed all
  four files and 47 tests. This is a focused retry, not a green complete suite.
- From `apps/delidev`, `pnpm test:bundle-dry-run` passed eight tests and
  `pnpm test:desktop-launch` passed 16 tests. `pnpm test:widget` and `pnpm build`
  passed. These are fixture/build results, not native launch acceptance.

## Complete frontend command

From `apps/delidev`, `GOMAXPROCS=2 pnpm test` completed with a temporary
two-worker Vitest cap: 92 files passed and four failed; 1,238 tests passed and
five failed. The temporary config was restored on exit.

Four App tests exceeded their existing five-second limits. The preferences
fixture could not find the New Server preferences button. Configuration and
usage fixtures each exceeded their existing 15-second cleanup-hook limit.
The subsequent four-file retry passed without relaxing assertions or deadlines.
Shared-machine contention was observed, but these observations do not establish
an identical root cause for every failure.

## Complete Go race command

From the repository root,
`GOMAXPROCS=2 go test -race -p 8 ./cmds/delidev-cli/... -timeout 20m`
completed with exit 1: 16 packages passed and seven failed. The root executable
package has no tests. The complete outbound, provider, inference-proxy and GitHub
packages passed, in addition to the earlier focused network-server/CLI checks.

| Failing package | Observed result |
| --- | --- |
| `internal/cli` | Recovery fixture startup was canceled; session acceptance reached an unavailable workspace reader during the creation diff. |
| `internal/harness/grok` | Native initialization/mode/instruction fixtures failed; the package reached its 20-minute limit during original-plan uncertainty handling. |
| `internal/harness/nativewire` | Two race reports describe asynchronous process-exit logging writing a `bytes.Buffer` while `wire_test.go:186` reads it. Explicit JSON-RPC and late-acknowledgment cases failed. |
| `internal/server` | Account startup timed out and approval fixtures reported state-storage failure; the package reached its limit during provider catalog discovery. |
| `internal/store` | Backup deletion/inspection and budget fixtures reported state-storage or integrity failure; historical-schema convergence reached the package limit. |
| `internal/worker` | The package reached its limit during OpenCode builtin handling. |
| `internal/workspace` | Diff/recovery/PR-match fixtures reported proof, Git-launch or timeout failures; the package reached its limit during the first-execution head-moved case. |

This is a failed complete race command, not a green result reconstructed from
focused tests. Native harness, process, Worker and workspace source files have
no PR diff against merged main. Store additions are confined to network record
helpers and scoped instructions. That inspection does not prove a common cause
or excuse the race reports. The unchanged-main comparison in the merge record
reproduced an earlier `session create --wait` timeout; this run's session-diff
failure is different and has not been reproduced on unchanged merged main.

Shared-machine contention and 99% filesystem capacity (approximately 8.9 GiB
available when inspected) were observed. Neither observation establishes the
cause of all failures. No assertions, integrity checks or operation deadlines
were weakened. The run was allowed to finish through its configured package
limits; the corresponding Go test processes are no longer running.

## Acceptance limits

The earlier `c1f4de21` head passed selected GitHub checks in run 36700558670.
Those results do not certify the merged head or constitute Codex approval.
The next push requires fresh CI and review evidence.

Required LFS assets were hydrated before consumption. Generated application and
API-client dist directories were removed after validation. No generated binaries,
native user state, raw native content or credentials are included in this record.

Real provider/GitHub accounts, enterprise proxy networks, native credential
lifecycle, Windows/Linux runtime, release and Worker bootstrap acceptance were
not exercised. Passing controlled fixtures cannot establish those outcomes.
