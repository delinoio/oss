# Issue #1094 replacement implementation, 2026-09-30

## Source and scope

The branch starts from freshly fetched main `ad0e3e9a29cb3d8375ab5d168bb160c35a023250`.
Issue #1094 is open. Prior PRs #1122 and #1169 are closed without merging;
this change reuses #1169 head `05bab0c09274e0aa830fe45b2c17496e64eee459`
while preserving current main, including permanent session deletion. Historical
issue evidence is retained unchanged. Main already reserves EntityKind 30 and
SystemCapability 12; no allocation or migration is introduced.

The implementation exposes authenticated, read-only Codex 0.151.0 and Claude
2.1.236 API child observations through resource reads, CLI pagination and the
capability-gated desktop. Children retain independent cleanup obligations;
observed children never acquire a continuation checkpoint. Nullable output,
requested/observed models and non-additive usage remain distinct. Complete
ownership batches and receipts preserve atomic publication and replay.

The replacement also addresses the outstanding prior review findings:

- Inspect Codex descendants on every parent completion, even without a spawn
  notification. Inspection uses state-DB-only inventory and read-only history.
- Bind Claude sidecars to retained description and depth, and project only the
  verified selected ancestry. Abandoned sibling records cannot supply output,
  model or usage. Missing binding metadata leaves stored history unavailable.
- Acknowledge late Claude task flags and description patches before changing
  local metadata. A retained true skip-transcript flag cannot be cleared.
- Keep task/content receipts separate from locally retained last-available
  fields; forwarded child user/context and tool-result messages are history
  proof rather than assistant output.
- Reject replacement of an original explicit requested model, and reject
  Claude-only ownership fields in Codex observations.
- Check retained native/product/execution ownership once per bounded batch,
  before writes, instead of rescanning source-coverage JSON per child.

## Executed verification before the implementation commit

Required assets were hydrated with `git lfs pull`; root `pnpm install` installed
workspace binaries and hooks. `pnpm proto:generate` regenerated tool-owned split
bindings and compatibility exports. The ach and DevHud administrator embedded
assets were explicitly built before Go compilation.

- `pnpm proto:lint` and `pnpm proto:breaking`: passed.
- `GOMAXPROCS=4 go vet ./cmds/delidev-cli/...`: passed.
- The initial six-package focused race command passed domain, Claude history,
  server publication and CLI tests, but failed a Codex fixture lifetime and a
  newly added Worker test's model setup. The Codex multi-read fixture now owns a
  bounded one-minute lifetime; its isolated recheck passed. The Worker fixture
  now assigns its child model after the common helper, which otherwise replaces
  that field. The complete focused command is being rerun on the corrected source.
- `pnpm exec vitest run src/subagents.test.tsx src/session-subagents.test.tsx`:
  passed, two files/four tests, including hierarchy, exact nullable counters,
  read-only paging and preservation of the root stream under large child output.
- Required `pnpm test` in `apps/delidev`: failed in Vitest, 77 files passed and
  20 failed; 1,176 tests passed and 64 failed. The output includes Settings,
  sidebar and tray timing/readiness failures. No full frontend pass is claimed.
  An equivalent test/build sequence with Vitest limited to two workers is running.
- The complete DeliDev backend race suite is running with `GOMAXPROCS=2`, `-p 1`,
  `-parallel 2` and a 20-minute per-package watchdog. No aggregate result or
  revision-pinned green result is claimed here.

A later independent evidence file will record completed checks and any repairs.
Concurrent native/Go/frontend suites were observed on this host, but contention
is not proof that every failure is environmental or pre-existing. Ordinary
fixtures use isolated temporary state and no user credentials. Real-account,
native-platform installation, release and perceptual acceptance remain unperformed.
Generated dist output will be removed after validation and commit hooks finish.
