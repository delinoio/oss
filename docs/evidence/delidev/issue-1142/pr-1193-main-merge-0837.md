# PR #1193 main-merge maintenance at 08:37 UTC

Recorded on 2026-09-30 on macOS arm64 after explicitly invoking the repair-pr workflow for [PR #1193](https://github.com/delinoio/oss/pull/1193). The starting head was `56c1828f5f8160d0af2ccf93914b152dfb0ac3af`. The [implementation](repository-folder-registration.md), [allocation repair](pr-1193-first-repair.md) and [picker/primary-checkout repair](pr-1193-review-repair.md) records retain their historical outcomes.

## Conflict resolution

Main advanced to `b1b3e9e7c55511086a284021850426d48484b127`. Merge commit `3d7b1a4ee870874fde4c92ba7c0fed138fdeac32` retains the new repository-registration routing branch while adopting main's direct API-provider wizard change, which no longer seeds provider-search text for Add entry. The automatic merge preserves Projects grouping, new Schedule presentation, Home-only sidebar actions and the existing Settings opening lifecycle.

The domain, server and Worker AGENTS.md conflicts contained independent appended rules. Both the repository metadata negotiation/map-validation rules and main's verified settled Claude-failure continuation/recovery rules are retained without omission or reinterpretation. No protocol allocation, schema, generated binding or native Rust source was changed by the resolution.

At the initial maintenance inventory there were no unresolved, non-outdated Codex review threads and no failing checks. GitHub exposed only a successful Cloudflare Pages check; complete Actions and approval evidence was unavailable. The previous two repaired threads remain resolved. No CI or review repair was invented from missing evidence.

## Executed verification

- Generated client build passed: `pnpm --filter @delinoio/delidev-api-client build`.
- Focused frontend from `apps/delidev`: registration, Projects, Settings and account suites passed 4 files / 103 tests with `pnpm exec vitest run src/repository-registration.test.tsx src/settings-projects.test.tsx src/settings.test.tsx src/account-settings.test.tsx --maxWorkers=1 --testTimeout=15000`; `pnpm typecheck` passed before the merge commit.
- Focused Go from `cmds/delidev-cli`: `go test ./internal/domain ./internal/server ./internal/worker ./internal/harness/claude -run 'Test(RepositoryMetadataNegotiation|InspectionMetadataCapability|InspectionGitHub|InspectionMetadataUsesEffectiveURLs|ClaudeFailedResumeClaimsNextFIFOInputOnce|ClaudeLostFailedReportRecoveryPreservesOriginalFailure|ClaudeFailedCheckpointStillRequiresOriginalNativeEOFProof|FailedCheckpointRejectsChangedSettingsWithoutNativeReplay)' -count=1 -timeout=4m` passed. Domain compiled with no matching tests in this command; the independent `go test ./internal/domain -run 'Test(Claude|RepositoryAcceptsRemoteCheckoutPaths)' -count=1 -timeout=2m` passed its domain regressions. Both commands used disposable `GOMODCACHE=/private/tmp/issue-1142-go-mod GOCACHE=/private/tmp/issue-1142-go-build GOMAXPROCS=2 GOFLAGS=-p=2`.
- Required full frontend against merge revision `3d7b1a4ee870874fde4c92ba7c0fed138fdeac32`: `GOMODCACHE=/private/tmp/issue-1142-go-mod GOCACHE=/private/tmp/issue-1142-go-build GOMAXPROCS=2 GOFLAGS=-p=2 VITEST_MAX_WORKERS=2 pnpm test` passed from `apps/delidev`: 90 files / 1,147 tests, 8 package dry-run tests, 16 launch/asset tests, widget fixture checks and production build. The normal unit-test timeout was retained.
- `pnpm ci:contracts` passed 113 tests, including the newly merged Windows test-precompile runner; `pnpm ci:workflows` passed. The normal commit hooks passed Go formatting. `git diff --check` passed, and repository-owned generated dist directories were removed after validation.

## Remaining limits and publication

Earlier root Rust failures on unchanged binpm temporary-path and clibox DNS-classification assertions remain recorded. This resolution changes no Rust source and claims no new root Rust success. The earlier failed broad frontend runs remain historical failed runs even though the last review repair and this merged run pass completely.

Native picker/trusted-main/saved/foreign-window authorization, keyboard/focus and 960×640/200% zoom acceptance remain unverified independently on macOS, Windows and Linux. Component, compile, package, widget and disposable Go fixtures do not establish native desktop or real-account acceptance.

All local changes are committed before one normal push; no force push, merge or auto-merge is used. Closes #1142 and the existing PR description's historical limits are preserved. The push invalidates prior CI/review evidence; current mergeability, Actions checks and Codex activity are assessed in subsequent five-minute maintenance rather than a polling loop.
