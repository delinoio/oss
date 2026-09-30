# Issue #1095: frontend validation after main reconciliation

This record covers source `20e9a04f2b2cde1b33cad880aaf3c2a5e5aa1822`
on macOS arm64 with Node.js 24.20.0 and pnpm 10.26.2. The frontend source has
an empty diff against incorporated main
`98df29c41ddf3c8b1274c51fe8f406b6dae6ca74`; the feature's generated protocol
bindings are present. Earlier evidence is preserved.

## Executed results

- `pnpm test` from `apps/delidev`: failed in the unit stage. Typechecking and
  the client build passed. The unit run had 84 passing and 15 failing files,
  with 1,224 passing and 51 failing tests in 147.40 seconds. This command did
  not reach its subsequent packaging, launch, widget or build steps.
- `pnpm test:unit --maxWorkers=2`: 95 passing and four failing files;
  1,271 passing and four failing tests in 419.58 seconds. All four failures
  were the existing 5,000 ms test deadlines: Schedules connection memory,
  device confirmation reopening, notification-permission reopening and
  configuration import review/retry. Assertions and deadlines were unchanged.
- `pnpm test:unit src/configuration-transfer.test.tsx
  src/device-settings.test.tsx src/notification-presentation.test.tsx
  src/schedules-sidebar.test.tsx --maxWorkers=1`: all four files and all 35
  tests passed in 23.80 seconds, including the four timed-out cases.
- Separately executed the remaining original pipeline steps:
  `pnpm test:bundle-dry-run` (eight tests), `pnpm test:desktop-launch`
  (16 tests), `pnpm test:widget` and `pnpm build`: all passed.

The exact LFS icon
`apps/delidev/src-tauri/icons/icon-source@2x.png` was hydrated before the asset
and packaging fixtures. Generated client/frontend distributions are ignored
outputs and are removed from the final worktree.

The smaller isolated run supports a timing distinction for those four cases,
not a complete frontend-suite pass or a proven single cause for all earlier
failures. No production timeout, fixture assertion, frontend configuration or
UI behavior was changed to obtain these results. These fixture/build checks do
not establish installed-native managed login, OAuth, inference, platform release
or desktop acceptance. Complete issue #964 remains separate.
