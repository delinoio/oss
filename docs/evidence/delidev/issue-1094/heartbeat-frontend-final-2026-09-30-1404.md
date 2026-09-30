# Final desktop validation for the 2026-09-30 14:04 UTC maintenance pass

Validated implementation: `79ff32d83400373e8647f97f65c2f539b168fd44`.
The default `GOMAXPROCS=2 pnpm test` command in `apps/delidev` was run again
after all four review fixes. API-client generation/build and TypeScript checking
passed. Vitest exited 1 after 226.12 seconds: 101 files (81 passed, 20 failed),
1,280 tests (1,190 passed, 85 failed, 5 skipped). This required pipeline did not
reach the packaging, widget or production-build stages.

The 85 failing tests are distributed across these 15 files:

| File | Failed tests | Total tests |
| --- | ---: | ---: |
| `src/subscription-controller.test.tsx` | 5 | 6 |
| `src/doctor.test.tsx` | 1 | 33 |
| `src/schedules-sidebar.test.tsx` | 1 | 17 |
| `src/pull-requests.test.tsx` | 2 | 11 |
| `src/device-settings.test.tsx` | 1 | 7 |
| `src/notification-presentation.test.tsx` | 1 | 3 |
| `src/desktop.test.tsx` | 3 | 5 |
| `src/agent-configuration.test.tsx` | 4 | 19 |
| `src/tray-presentation.test.tsx` | 1 | 1 |
| `src/settings-lifetime.test.tsx` | 12 | 15 |
| `src/settings-projects.test.tsx` | 11 | 16 |
| `src/settings-models.test.tsx` | 13 | 30 |
| `src/backups.test.tsx` | 1 | 8 |
| `src/settings.test.tsx` | 14 | 49 |
| `src/App.test.tsx` | 15 | 45 |

Five additional integration suites failed during their temporary Go binary
build: `settings-claude`, `settings-configuration`, `settings-github`,
`settings-preferences` and `settings-workspace`. The retained public summary
reports a failed `go build` command without sufficient cause detail; it does
not establish a compiler regression or a timeout cause. The 85 test failures
include unchanged default 5/15-second deadlines, missing UI and state/pagination
assertions. Their cause remains unproved. Another worktree's frontend and CLI
validation processes were observed on the host, but this concurrency is not
proof that these failures are environmental. No assertion, deadline or default
Vitest setting was changed to obtain a passing result.

The remaining required pipeline stages were run explicitly and passed:
`pnpm test:bundle-dry-run` (8/8), `pnpm test:desktop-launch` (16/16),
`pnpm test:widget` (all fixture checks) and `pnpm build` (production bundle),
each with `GOMAXPROCS=2`. Required repository-owned generated outputs were
built and LFS icons hydrated before validation; ignored generated `dist` is
removed after final validation and hooks.

The independently run diagnostics resource-kind regression passed 75/75 tests
at the same unchanged frontend source in `cca9b6c07`. Earlier isolated child/CI
frontend checks passed 27/27 tests. These focused results and successful
packaging/build stages do not substitute for the failed default desktop suite.
Real-account, native desktop-platform and release acceptance were not performed.
