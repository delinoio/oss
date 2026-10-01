# Final local validation for the 22:09 UTC PR #1225 maintenance pass

Runtime/test source: `f43bcf551` after the four separate repair commits. Commits `380557af9` and `079c14187` added evidence only while checks ran; no runtime, frontend or protocol source changed during verification. The scheduled 2026-09-30 pass completed local checks on 2026-10-01 UTC. Historical evidence remains unchanged.

## Full backend result

```sh
GOMAXPROCS=4 go test -race -p 2 -parallel 2 ./cmds/delidev-cli/... -timeout=20m
```

The command completed with exit 1: 17 packages passed, five failed and two had no tests. Cached results are explicit below; they are not fresh executions. No complete backend pass is claimed.

| Package below cmds/delidev-cli | Result | Reported result/time |
| --- | --- | --- |
| (command) | ? | [no test files] |
| internal/apiproxy | ok | (cached) |
| internal/cli | FAIL | 226.630s |
| internal/connections | ok | 34.905s |
| internal/credentials | ok | (cached) |
| internal/domain | ok | 5.067s |
| internal/forwarding | ok | (cached) |
| internal/harness | ok | 56.459s |
| internal/harness/claude | ok | 220.865s |
| internal/harness/codex | ok | 319.107s |
| internal/harness/grok | FAIL | 1201.531s |
| internal/harness/nativewire | ok | (cached) |
| internal/harness/opencode | ok | (cached) |
| internal/integrations/github | ok | (cached) |
| internal/presentation | ok | (cached) |
| internal/process | ok | (cached) |
| internal/providers | ok | (cached) |
| internal/rpc | ? | [no test files] |
| internal/security | ok | (cached) |
| internal/server | FAIL | 1201.633s |
| internal/store | ok | 856.294s |
| internal/userservice | ok | (cached) |
| internal/worker | FAIL | 494.824s |
| internal/workspace | FAIL | 1200.743s |

CLI acceptance failed `TestCLISessionAcceptanceQueueAndArchive` when its workspace file reader was unavailable. Worker failed `TestTargetedCancellationKeepsStreamAndNextOperationUsable/running` because cancellation ended its stream or did not prove stopped work (parent case 12.28s; subcase 12.15s). These broader failures remain unresolved; this run does not prove they are pre-existing, unrelated or environmental.

Grok, server and workspace each reached their cumulative 20-minute package limit. The active Grok creation-valid subcase had run seven seconds, server's pending-permanent-deletion account-switch case two seconds, and workspace's changed-remote partial-project subcase 13 seconds (its parent case 1m19s). Those suites provide partial coverage only; the watchdog cannot prove each active assertion failed or that later tests ran. No deadline or native cleanup assertion was relaxed, and no data race was reported by this run.

## Focused repairs and required checks

- The retained parent-tool real server/SQLite red/green selection passed server/domain under the race detector. See [ownership evidence](retained-parent-tool-2026-09-30.md).
- The incoming source-field selection passed domain/server/Worker/Codex under the race detector. The explicit verbose four-test native-adapter selection also passed. See [field-matrix evidence](source-field-matrix-2026-09-30.md).
- Actual Claude interrupt/Worker Stop boundary tests passed; a synthetic controller prototype was discarded and no Stop authority changed. See [maintenance inventory](heartbeat-2026-09-30-2209.md).
- The private history fixture and actual private reader selection passed Worker/Claude under the race detector. `GOMAXPROCS=4 GOOS=windows GOARCH=amd64 go test -p 2 -c -o /tmp/oss-1225-2209-worker.test.exe ./cmds/delidev-cli/internal/worker` passed. This proves Windows buildability only; the next Windows CI must exercise its ACL and receipt behavior. See [fixture evidence](windows-history-fixture-2026-09-30.md).
- All 43 closest child-page/hierarchy/streaming component tests passed at default Vitest deadlines, and fresh frontend typechecking passed. Complete default frontend validation failed with 79/103 files passing, 24 failing, 1,205 tests passing, 111 failing and seven skipped. Seven suite failures occurred during temporary Go fixture builds. Separate bundle (eight tests), launcher/assets (16 tests), widget and production build checks passed; they do not make the default command a pass. See [complete desktop evidence](heartbeat-frontend-2026-09-30-2209.md).
- `GOMAXPROCS=4 go vet ./cmds/delidev-cli/...`, all 113 `pnpm ci:contracts` checks and complete `pnpm proto:check` passed. Protocol format/lint/breaking/freshness introduced no tracked-source drift. No Rust source changed.

## Cleanup and maintenance boundary

Required embeds and API-client output were explicitly generated before compilation; the DeliDev exact LFS icon was verified ready. After tests finished, all seven repository-owned generated `dist` directories were removed. The ignored third-party Go toolchain's source directory named `dist` was preserved. No repository-owned generated output is tracked, and no remaining Go/CLI/test process with this checkout as its working directory was found. Original user configuration, identity material, credentials and historical evidence remain untouched.

The latest terminal guard still found the PR open at `b5c14557d0652f42e63f709bbfaae78c5f1dc591`; its original standalone `Closes #1094` remains required. The repair workflow performs one final helper inventory and one push after all local repairs/evidence are ready, then explains/resolves handled review threads. Push/thread outcomes are reported by the maintenance owner, not presumed by this pre-push record. A new push requires fresh CI/review; older success, skipped checks, a stale security summary or previous reactions cannot approve it. The existing five-minute heartbeat remains active while the PR is open. No merge or auto-merge is authorized.

These private fixtures, component/script checks and builds do not establish real native-account, supported-platform, packaging, signing or release acceptance. The wider issue #964 requirements and native evidence gaps remain explicit in their owning contracts.
