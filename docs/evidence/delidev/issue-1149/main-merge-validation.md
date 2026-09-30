# Main-merge validation for home-only sidebar actions (issue #1149)

Date: 2026-09-30. This record covers the merge of main revision
`7f356266fc195b1880ffac66a93dadab5c5a2df7` into issue branch
`kdy1/delidev-home-actions-1149`, whose pre-merge head was
`381223a2b75b3d21bde3a0520180f1eccc669bcd`.
The original implementation and browser observations remain in
[home-sidebar-actions.md](home-sidebar-actions.md).

## Conflict resolution

Only `apps/delidev/AGENTS.md` conflicted. Preserve both the home-only sidebar
navigation rule and the incoming Diagnostics presentation ownership rule.
The scoped frontend instructions and desktop contract merged without conflict.
`App.tsx`, `sidebar.tsx` and their issue-owned regressions are unchanged from the
pre-merge head. The combined suite also covers the incoming AI API Keys and
Diagnostics presentation changes.

## Earlier local attempts

A focused five-file Vitest run with two workers and a 15-second test budget
passed 134 of 135 tests. The existing Settings optional-provider test exceeded
Testing Library's default one-second element wait for Continue to details.

The first complete merged package run used temporary two-worker, 15-second test
and five-second element-wait budgets. All 1,000 executed tests passed, but ten
Settings integration suites failed during Go fixture setup; eleven tests could
not run. Build errors identified disappearing shared Go cache objects and
missing pinned-toolchain source files. This was not a passing package run.

A retry with only a task-owned build cache also failed when the shared Go
compiler and cgo tools became unavailable. A fresh task-owned module/toolchain
cache plus build cache then successfully built the fixture CLI at the existing
pinned Go/dependency versions:

```sh
GOMODCACHE=/private/tmp/delidev-1149-go-modules \
GOCACHE=/private/tmp/delidev-1149-go-cache \
GOMAXPROCS=2 GOFLAGS=-p=2 \
go build -o /private/tmp/delidev-1149-cli ./cmds/delidev-cli
```

The retry does not change repository configuration, dependency pins, assertions
or test selection. Both temporary test runner/setup overrides are restored
byte-for-byte after the package command. The shared user caches are not repaired
or removed by this task.

## Completed verification

With the same task-owned Go caches and bounded Go build concurrency, the required
`pnpm test` command in `apps/delidev` passed: 85 Vitest files / 1,011 tests with
none skipped, eight package-verifier fixtures, sixteen launcher/asset fixtures,
native Swift widget fixtures, frontend type checking and the production build.
The Vitest phase took 177.51 seconds. The temporary local budgets were
`maxWorkers: 2`, `testTimeout: 15000` and Testing Library
`asyncUtilTimeout: 5000`; no assertions were changed or disabled.
This establishes success under those local budgets and isolated caches, not
success of the earlier failed attempts.

The runner/setup files were restored byte-for-byte. `git lfs fsck` and
`git diff --check` passed. Generated app/client `dist` directories were removed
before committing. No repository-owned Rust source was changed by this repair.

## Evidence limits

The original Chromium fixture evidence remains pinned to the original sidebar
implementation, which is unchanged by this merge. This repair did not repeat
browser interaction or launch a packaged Tauri/CEF host. Packaged macOS, Windows
and Ubuntu runtime behavior, actual 200% browser zoom, reduced-motion interaction
and native window acceptance remain unverified. Fixture/package success does not
establish native or real-account acceptance.
