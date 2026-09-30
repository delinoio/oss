# Schedule and Projects main merge validation

## Source and revision

- Date: 2026-09-30.
- Issue: https://github.com/delinoio/oss/issues/1136.
- PR: https://github.com/delinoio/oss/pull/1172.
- First-parent revision: `94e2568c24c37179a80c93c7092d8fd8598a586d`.
- Merged main: `6441b84813007eb1f3fca9c3ac82d6963cf44bd6`.

The final repair inventory reported a new conflict after the preferences CI fix was committed. Main added four Windows Go shards (#1194), Home-only sidebar actions (#1185), direct API provider actions (#1183), schedule frequency presets (#1196) and grouped Projects settings (#1191). The sole textual conflict was the workspace integration fixture's new schedule controls.

The resolution retains main's Local computer radio, explicit Custom frequency selection and Create schedule action, with the existing Runner Device label. The new schedule creation component's selector and required-field guidance use Runner Device while Agent Worker, machine IDs, Local proof, definition bytes and immutable retries retain their meanings. The new schedule tests retain every main assertion with matching labels. The desktop contract identifies the new selector's visible/accessibility name without changing generic execution-machine references or other source-backed requirements.

Inspection confirmed that main's application navigation, account/provider workflow, sidebar, independent Settings fixture and CI workflow/sharder source are preserved exactly. Existing Runs on, Runner Devices Settings category and presentation-only contracts remain intact.

## Focused verification

- `pnpm exec vitest run src/schedules.test.tsx src/settings-projects.test.tsx --maxWorkers=1`: all 47 tests in two files passed.
- `node --test scripts/ci/go-test.test.mjs scripts/ci/ci-contract.test.mjs scripts/ci/plan.test.mjs`: all 56 tests passed.
- `git lfs fsck`: passed; newly inherited evidence PNGs are each below 512 KiB.

Required administrator/async-commit-hook embeds and the generated DeliDev client were prepared before compilation. `GOCACHE=<private temporary cache> GOMAXPROCS=2 go build -p 2 -o <temporary validation binary> ./cmds/delidev-cli` passed.

## Complete frontend gate and limits

`GOCACHE=<private temporary cache> GOMAXPROCS=2 pnpm test` from `apps/delidev` passed: all 1,124 tests in 89 files, eight packaging fixtures, sixteen asset/desktop-launch fixtures, widget checks and the production build. This includes the resolved real-Worker workspace/schedule fixture and the repaired preferences test. The complete gate used the original tracked Vitest configuration and Testing Library setup, with no global wait increase, worker limit or runner timeout override.

`git diff --check` passed. Generated repository-owned `dist` output and this pass's temporary Go cache/binary are removed before the final clean-worktree check. This merge does not establish native/account/platform acceptance or replace the earlier incomplete broad local Go gate. Fresh CI and review for the pushed head remain separate evidence.
