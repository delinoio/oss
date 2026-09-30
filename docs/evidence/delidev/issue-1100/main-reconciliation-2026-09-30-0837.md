# PR #1184: main reconciliation for the 08:37 UTC maintenance pass

## Scope and revisions

Started from the clean existing issue branch at
`aea128b440a068f8eb1bbb1b18b0cb90155935cd`. The PR remained open and
GitHub reported conflicts after main advanced. Explicitly invoked the installed
repair-pr skill and merged fetched main
`16b1aff543c7518dfb678609acf04e0379d990d7` without rebasing.

The only textual conflicts were independent appended rules in the domain and
server AGENTS files. Retained the complete Grok accounting rules and main's
Claude settled-failure continuation/recovery rules. Moved the unchanged Grok
rules immediately after each owner's introductory instructions, avoiding
another shared-file-end conflict. The preserved rule SHA-256 values are:

- Domain: `ee71a7996a579c92bb28dd042120ae8c9c45330ba9f027e41fa97652bd1e44a9`.
- Server: `0225453902d0cc19b3114d95023b7df3cd57780cd9e7657073aa13f998f59be9`.

Main's provider-picker, schedule-creation and Projects presentation changes
merged without textual conflicts. Claude's explicit failed Resume remains
separate from the Grok successful-completion accounting boundary. Preserved
Grok original input/source/cleanup proof, exactly-once retention, unpriced
native units and schema 25 layout identity. No protocol or Rust source changed
in this main reconciliation; generated schema outputs needed no repair.

## Executed validation

Executed on the merged tree in the existing issue worktree, on macOS arm64
with Go 1.26.8, Node 24.11.0 and pnpm 10.26.2. No user credentials or installed
harness inference were used by the ordinary fixtures.

- `GOMAXPROCS=2 go test -race -p 1 -timeout=10m ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/harness/claude -run 'Accounting|UnmergedVersion25|EveryHistoricalSchema|MigrationDefinitions|Claude.*(Failed|Recovery|Continuation)|FailedCheckpoint' -count=1`
  passed domain (1.644s), store (100.319s), server (155.751s), Worker (8.727s)
  and Claude harness (1.994s). It verifies accounting/replay/history rejection,
  historical migration convergence and the imported failed-Claude
  continuation, explicit Resume and original-failure recovery invariants.
- `go vet ./cmds/delidev-cli/...` passed.
- `VITEST_MAX_WORKERS=1 pnpm test` from `apps/delidev` passed the generated
  client build, frontend typecheck, all 90 Vitest files / 1,129 tests (188.55s
  Vitest stage), 8 bundle dry-run fixtures, 16 desktop-launch/asset fixtures,
  native Swift widget fixtures and production web build. The tracked icon
  source was confirmed to be a hydrated PNG.
- Both required Go embeds were explicitly built before the normal commit
  hook. Repository-owned generated dist directories were removed after
  validation; none are tracked or retained in the final worktree.

## CI, review and remaining limits

The initial current-head CI inventory had no failures: its plan, contracts,
Go Quality and Pages checks passed; platform Go and other selected checks were
still running. All relevant Codex thread/review/comment pages were inspected.
At the CI-repair step, the same pre-push head had 11 passing checks and no
failures. Linux and macOS Go, Windows harness/server/Worker, protocol/client,
Go Quality, CI contracts, affected-job planning, async-commit-hook and Pages
passed. Only Windows core remained pending; this is not complete CI success.
The harness-shard pass is fresh Windows evidence beyond the earlier failed
monolithic job, including the detached Worker fixture. It does not prove the
later merged head. There were no unresolved non-outdated bot threads. The activity summary bound
the running Code Review and completed Security Review to `aea128b4`; it does
not approve the later merge commit. The last submitted inline review was on
`babbabb653`, whose two findings had already been fixed and resolved.

This focused pass is not a complete broad Go race pass. The previously
reported broad CLI workspace-reader failure and earlier Windows detached
Worker failure remain recorded in [the previous maintenance evidence](main-merge-maintenance-2026-09-30.md).
No repeat of those unchanged blocked local attempts was made in this pass.
Fresh CI and Codex results on the pushed head are assessed by the next
scheduled run. Fixtures, builds and a macOS test cannot establish hosted Grok,
installed-provider, native visual, Windows/Linux product or release acceptance.
