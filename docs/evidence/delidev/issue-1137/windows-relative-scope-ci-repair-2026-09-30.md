# Issue #1137 Windows relative-scope CI repair — 2026-09-30

The repair starts from PR #1228 head
`ca0ba6eaa85db7bee41ad97035f297367fbcd09d` with a clean checkout. Codex code and
security reviews completed for that revision, with no unresolved review threads.

## Failure and repair

CI run [36713168848](https://github.com/delinoio/oss/actions/runs/36713168848) fails
the Windows harness and Worker shards in two relative-scope fixtures:

- `TestDesktopLaunchAdmissionPreservesRelativeEnsureScope` in the CLI package.
- `TestLaunchAdmissionCanonicalizesRelativeScope` in the user-service package.

Both call `filepath.Rel` between the checkout on drive D: and a temporary scope on
drive C:, which cannot have a relative path. The other packages in both Windows
shards pass. Ubuntu and macOS Go tests and the Windows core/server shards pass.
The aggregate `CI Result` failure reports the same failed Go-test dependency.

Each fixture now uses `t.Chdir` to enter its temporary scope's parent and passes
the relative basename. Test cleanup restores the original working directory before
removing temporary state. The ensure fixture still verifies that absent lifecycle
intent does not initialize an owner or authorize startup. The service fixture still
verifies canonical scope identity, retained stopped registration, and exclusion of
an absolute-path admission while the relative-path admission holds its lock.
Product behavior, native service definitions, protocol and storage are unchanged.

## Validation

- Focused race tests pass: CLI 1.897 seconds and user-service 1.687 seconds, using
  `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/cli
  ./cmds/delidev-cli/internal/userservice -run
  'TestDesktopLaunchAdmissionPreservesRelativeEnsureScope|TestLaunchAdmissionCanonicalizesRelativeScope'
  -count=1` from the repository root.
- `GOMAXPROCS=2 go vet ./cmds/delidev-cli/...` passes.
- `pnpm proto:check` passes lint, breaking checks and fresh generation with no
  generated-source drift.
- DeliDev API-client lint, all 44 tests in four files, and build pass through the
  package's existing scripts.
- Both affected Go test packages cross-compile for Windows x64 using
  `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 GOMAXPROCS=2 go test -c -p 1` with
  separate test executables outside the checkout. This establishes compilation,
  not native Windows execution.
- The broader `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/...` run reports
  the previously recorded `TestCLISessionAcceptanceQueueAndArchive` workspace-read
  failure (`unavailable`, CLI suite 307.992 seconds). The Grok harness separately
  reports native initialization failure in
  `TestInitialPlanRequiresOriginalClaimAckAndMode/mode-valid`, then reaches its
  ten-minute package timeout while
  `TestOriginalPlanReplyClaimJoinsNativeLifetimeLoss` is running. Its package result
  is failure in 600.560 seconds; that running test's one-second age does not prove
  that it caused the package timeout. These failures are outside the changed
  fixture setup and do not establish a passing full race suite.
- The complete server (505.634 seconds), store (172.181 seconds), user-service
  (4.433 seconds), Worker (126.083 seconds) and workspace (387.791 seconds) race
  suites pass, as do the Claude, Codex, OpenCode and native-wire harness suites.
  The full command finishes with exit code 1; only the CLI and Grok packages fail.

Existing rendered CEF, real service-manager, packaging, remote TLS and other
native acceptance limits remain unchanged. DevHud administrator and
async-commit-hook embedded assets are regenerated successfully for root Go hooks.
Generated bindings retain no drift, and repository-owned generated `dist`
directories are removed after the commit hook completes.
