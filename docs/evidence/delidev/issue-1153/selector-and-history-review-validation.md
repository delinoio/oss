# Schedules selector merge and history action review validation

Recorded on 2026-09-30 for the scheduled maintenance of
[PR #1189](https://github.com/delinoio/oss/pull/1189), addressing
[issue #1153](https://github.com/delinoio/oss/issues/1153).

## Source and repairs

Merge revision: `f66fcd6a274efce96c695298b8c578f5fde70ec2`, with main parent
`126641a6dcf274420c8c5800a6e88079f0e19990` and previous PR head
`2dbe41e92905ba561ac9e2995738f5f237b441cd`.

The selector conflict combines Schedules' optional `emptyLabel` with main's
Agent Worker `markRequired`, accessible required markers and read/problem
reporting. The exact Select lowercase-label fallback remains unchanged. Both
conflicted ownership notes are retained, alongside independently landed Activity,
Pull requests, Integrations and Agent Worker implementations/contracts.

The separate accessibility repair is
`e9d143c26aa9d61637849073576faaaeaecac6a9`. It handles
[the Codex finding](https://github.com/delinoio/oss/pull/1189#discussion_r4142236673)
that the expanded form exposed two buttons named Retained history. The disclosure
keeps that accessible name; the submit action exposes Open retained history and
retains the approved visible Retained history text. Submission, locks, draft,
focus, queries and drawer closure remain unchanged. The contract and scoped
frontend instructions now require those distinct names.

## Executed checks

- Generated API client build, then focused Vitest checks of
  `schedules-sidebar.test.tsx`, `schedules.test.tsx`,
  `agent-configuration.test.tsx`, `pull-requests.test.tsx` and
  `integrations.test.tsx`: 5 files / 75 tests passed on the merge revision.
- `pnpm test` from `apps/delidev` on the merge revision passed: 89 files / 1,065
  tests, generated client, typechecking, 8 packaging dry-run tests, 16
  launcher/asset tests, Swift widget fixtures and production Rsbuild build.
- After the accessible-name repair, package typechecking and the two focused
  Schedules files passed: 24 tests. The new regression selects the disclosure and
  submission by unique accessible names, checks the retained visible text,
  verifies that expansion makes no history request, and submits the exact ID
  with the existing 50-row request and drawer-close behavior.
- Final `pnpm test` on `e9d143c26aa9d61637849073576faaaeaecac6a9` passed: 89 files /
  1,066 tests, generated client, typechecking, 8 packaging dry-run tests, 16
  launcher/asset tests, Swift widget fixtures and production Rsbuild build.

The first full accessibility-check attempt caught unsupported Testing Library
`exact` options in the new role queries during TypeScript checking. Those options
were removed; string role-query names already match exactly. Typechecking and
focused checks were rerun before the successful final full package check.

Successful full runs used temporary `maxWorkers: 1`, `testTimeout: 60000` and
Testing Library `asyncUtilTimeout: 15000`. Both runner configuration files were
restored in `finally` and verified to have no diff. These are serial,
extended-timing passes, not evidence of default-timing acceptance.

## Cleanup and remaining limits

The generated client/app `dist` directories were removed after verification.
A scan excluding dependency `node_modules` and ignored native `target` found no
remaining repository-owned generated `dist`. No Rust source was changed.

The earlier browser matrix and macOS ad hoc smoke remain in
[the original record](schedules-context-validation.md); the prior ownership-only
merge check remains in [its separate record](main-merge-validation.md).
They were not repeated for this selector/accessibility repair. The accessible-name
regression is component evidence, not a native voice-control or screen-reader
acceptance run. Windows/X11, actual native 200% zoom, the full native viewport/state
matrix and signed release acceptance remain unverified.
