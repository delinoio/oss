# PR #1225 frontend validation after main reconciliation

Runtime source: `b44c9f497aa7c21bc998ceb1bc6e3746cb0898dc`, including main
`98df29c41ddf3c8b1274c51fe8f406b6dae6ca74`. Executed on macOS arm64 on
2026-09-30. Required LFS assets and generated client/embed outputs were prepared
first. Source was unchanged throughout these checks.

## Required default command

`GOMAXPROCS=2 pnpm test` from `apps/delidev` completed with exit code 1.
The API-client build and TypeScript check passed. Vitest reported 82 passing and
19 failing files (101 total), with 1,216 passing and 63 failing tests (1,279
total), in 197.72 seconds. No Vitest worker-count or test-timeout override was
used. Full frontend validation remains failed.

| File in `apps/delidev/src` | Failed tests |
| --- | ---: |
| `App.test.tsx` | 12 |
| `settings.test.tsx` | 9 |
| `settings-lifetime.test.tsx` | 8 |
| `settings-models.test.tsx` | 7 |
| `settings-projects.test.tsx` | 6 |
| `agent-configuration.test.tsx` | 4 |
| `desktop.test.tsx` | 3 |
| `pull-requests.test.tsx` | 2 |
| `subscription-controller.test.tsx` | 2 |
| `settings-claude.integration.test.tsx` | 1 |
| `settings-workspace.integration.test.tsx` | 1 |
| `settings-preferences.integration.test.tsx` | 1 |
| `settings-github.integration.test.tsx` | 1 |
| `settings-configuration.integration.test.tsx` | 1 |
| `schedules-sidebar.test.tsx` | 1 |
| `tray-presentation.test.tsx` | 1 |
| `doctor.test.tsx` | 1 |
| `device-settings.test.tsx` | 1 |
| `notification-presentation.test.tsx` | 1 |

Diagnostics include test deadlines of 5 or 15 seconds, missing expected elements
and a pull-request pagination assertion. The cause of this aggregate failure is
unproved; concurrent full Go race validation is recorded as execution context,
not an established explanation. No assertion, deadline or product behavior was
changed to obtain a pass. Earlier passing evidence belongs to its original
revision and does not substitute for this result.

## Focused and skipped-stage checks

The following separate focused command passed all 27 tests in five files:

```sh
GOMAXPROCS=2 pnpm exec vitest run src/session-subagents.test.tsx src/subagents.test.tsx src/github-ci-profiles.test.tsx src/github-ci.test.tsx src/pr-problems.test.tsx
```

It used the existing deadlines and default Vitest worker selection. These tests
cover the PR's subagent hierarchy and main's changed CI/problem consumers; they
do not establish a passing complete desktop suite.

Because the required command stopped at Vitest, its later stages were executed
separately, each with `GOMAXPROCS=2`: `pnpm test:bundle-dry-run` passed 8/8,
`pnpm test:desktop-launch` passed 16/16, `pnpm test:widget` passed, and `pnpm build`
passed. They verify fixtures and build output, not real native installation,
account inference, cross-platform acceptance or release publication. Generated
repository-owned `dist` output is removed after all validations and normal hooks.
