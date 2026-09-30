# Issue #1094 full race-run limits, 2026-09-30

## Command and source

The stable merged-source attempt used:

```sh
GOMAXPROCS=4 go test -race -p 2 -parallel 2 ./cmds/delidev-cli/... -timeout=8m
```

It began against `dc46ec717` and continued through documentation-only commits,
including merge `3656f5d3d`. That merge adds compaction reservation documentation,
allocation-ledger entries and contract checks. There is no difference in Go,
frontend, source protocol or generated runtime bindings across those revisions.

## Observed results

The attempt is not a full backend pass. Before it was stopped, these packages
completed successfully: apiproxy, connections, credentials, domain, forwarding,
harness/codex, harness/nativewire and harness/opencode. Failed packages were:

- CLI: `TestCLISessionAcceptanceQueueAndArchive` failed while the workspace was
  waiting for verified dispatch readiness. The CLI package also exceeded the
  explicit eight-minute watchdog during the detached-Worker lifecycle test.
- Harness discovery: `TestDiscoveryVerifiesGrokWithoutExecution` could not confirm
  cleanup of its Grok Build probe.
- Claude harness: the package exceeded its eight-minute watchdog during the normal
  stream-finish test. That final subtest had run for only two seconds when the
  cumulative package budget expired.
- Grok harness: text-closure acknowledgment/removal and native-lifetime cancellation
  tests failed, and the package exceeded its eight-minute watchdog.

The command was stopped after these failures. Remaining packages were not fully
verified by this broad attempt. Only that recorded command, its current children
and its uniquely identified temporary fixture processes were stopped; unrelated
host jobs were preserved. The separate complete focused race command passed all
six feature/composition packages, including server and Worker, as recorded in the
merged-validation evidence. This does not substitute for an aggregate backend
pass or establish that every broad failure is pre-existing or environmental.

## Later checks and PR state

After the compaction-reservation merge, all 113 repository contract checks and the
complete repeated `pnpm proto:check` passed. No runtime schema drift was generated.
PR #1225 was confirmed non-draft, open and mergeable at head `3656f5d3d`; its GitHub
CI jobs were running and no review approval was claimed. Five-minute maintenance
is registered in the originating chat as `maintain-delidev-pr-1225` and never
merges or enables auto-merge.

Generated repository-owned dist output is removed after these checks and normal
commit hooks finish. Real-account, native-platform and release acceptance remain
unperformed. The existing historical evidence remains unchanged.
