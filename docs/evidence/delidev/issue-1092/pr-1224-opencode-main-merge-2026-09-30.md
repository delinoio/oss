# PR #1224: OpenCode main reconciliation

## Revisions and scope

The 2026-09-30 13:15 UTC maintenance pass observed open PR #1224 as
conflicting on `4cd50834c95872d6a10fb3cf20fefd24b61cd471`. Its clean issue
checkout merges main `d1f83cecee4e0c50ea094335392cf68845f85739` without
rebasing. Main includes separate Windows OpenCode global roots (#1223) and
original OpenCode event reconciliation (#1229).

Four textual conflicts join independent additions in the server and Worker
AGENTS files and the harness and session contracts. Both original Codex fork
and Windows OpenCode additions are retained verbatim. Main's automatic code
merge removes the former Windows OpenCode General Chat exclusion while retaining
the existing Codex selection validation and fork dispatch. All 25 tracked fork
implementation and test files are byte-identical to the preceding PR head.
No new protocol allocation, migration, generated binding, frontend or Rust
change is introduced. The frozen historical evidence ledger is unchanged.

## Executed verification

The commands below ran from the repository root on the reconciled tree with
Go 1.26.8 on macOS arm64:

- `GOMAXPROCS=2 go test -race -p=1 ./cmds/delidev-cli/internal/harness/opencode -count=1 -timeout=4m`
  passed in 53.326 seconds.
- `GOMAXPROCS=2 go test -race -p=1 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/worker -run 'Fork|OpenCode' -count=1 -timeout=4m`
  passed: server 131.894 seconds and Worker 69.093 seconds.
- `GOMAXPROCS=2 go vet ./cmds/delidev-cli/...` passed.
- `DELIDEV_NATIVE_THREAD_EXECUTABLE=/private/tmp/delidev-1092-codex/codex GOMAXPROCS=2 go test -p=1 ./cmds/delidev-cli/internal/cli -run '^TestManualNativeCLISessionFork$' -count=1 -timeout=8m`
  passed in 76.315 seconds using pinned Codex 0.151.0, temporary state and a
  scripted keyless loopback provider. It covers General Chat and two dirty
  repository forks, independent files, exact retries, source queue isolation
  and child continuation across process replacement. Fork itself does not
  perform inference; no user credentials or hosted provider are used.
- Original addition and fork-file preservation comparisons, conflict-marker
  inspection and `git diff --check` passed.

## Evidence limits and maintenance

Ordinary OpenCode tests do not establish opted-in installed OpenCode or Windows
native acceptance. No unfiltered Go suite or frontend suite was repeated for
this Go/documentation-only reconciliation. The prior broad Go failures and
qualified frontend/platform results remain in
[the preceding repair record](pr-1224-repair-validation.md).

The initial PR inventory had no unhandled Codex threads or failing checks.
Only Cloudflare Pages had reported on the preceding head; Codex code/security
reviews were running for that head. Neither observation approves the merged
head. The next scheduled heartbeat assesses fresh CI, review and merge status.
