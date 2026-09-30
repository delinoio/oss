# Issue #1092: same-account native Codex session forks

## Implementation

Replacement of the unmerged PR #1126 on inspected main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7`. The implementation retains native `thread/fork`, immutable account/configuration, complete independent workspace snapshots, exact request receipts, source reservations, atomic child publication and paused child continuation. Source-backed fork contracts are retained; historical evidence remains frozen.

The replacement uses the current session/system proto owners, allocation-ledger capability number 13, CLI session dispatch and server status ownership. Generated service/client bindings preserve compatibility exports. No migration or credential-bearing user state is consumed.

## Validation

Executed on macOS arm64 on 2026-09-30. Runtime implementation was tested at
`6b2d9f8a834408d926e16006f2cc4fb4b25bc79a`; the follow-up native fixture at
`e3c1d0a664f07766cf9d6dea69f7c1f0f71b3529` uses ordinary asynchronous source
creation and its existing bounded public completion observation. It does not
change production or fork deadlines.

### Passing checks

- Root `go vet ./cmds/delidev-cli/...` passed.
- Root `pnpm proto:check` passed format, lint, breaking checks and fresh generated
  binding comparison. The split source owners and compatibility exports reproduced
  without drift.
- `node --test scripts/ci/delidev-proto.test.mjs scripts/ci/delidev-structure.test.mjs`
  passed all six ownership/generation checks.
- `pnpm --filter @delinoio/delidev-api-client test` passed 44 tests in four files.
- In `apps/delidev`, the new `src/session-fork.test.tsx` passed in isolation.
  `pnpm typecheck`, `pnpm test:bundle-dry-run` (eight tests),
  `pnpm test:desktop-launch` (16 tests), `pnpm test:widget` and `pnpm build` passed
  when run separately after the full frontend suite stopped at Vitest failures.
- The selected race-enabled Codex, workspace and server run used
  `go test -race -p 1 ./cmds/delidev-cli/internal/harness/codex ./cmds/delidev-cli/internal/workspace ./cmds/delidev-cli/internal/server -run Fork -count=1 -v`.
  All nine new deterministic fork tests passed: exact rollout ownership and byte
  proof; active/foreign/child refusal; immutable policy; source mutation checks;
  linked-destination refusal; two dirty repositories with unpushed HEAD,
  staged/unstaged/untracked/ignored data and permissions; second-copy cleanup;
  one atomic paused child with empty queue and exact receipt replay; and invalid
  source/completion rejection. The broad selection also matched the existing
  `TestPRPreparationCreatesExactForkWorktreeAndPreservesOriginalWork`, which failed
  with PR-startup rejection. The overall command therefore exited nonzero; its
  Codex and server packages passed, and the new workspace tests passed individually.

### Installed native evidence

The explicitly selected official Codex `rust-v0.151.0` macOS arm64 asset was
verified against its published SHA-256
`6409e2c65994d294a92bc7330148ebc447ab23908bd7f5c71e718425a622c965`
and its reported version before use. Every native run used temporary generated
homes, private Worker/server state and a controlled keyless loopback provider,
without an existing login or user credentials.

With `GOMAXPROCS=4` and `DELIDEV_NATIVE_THREAD_EXECUTABLE` set to that binary,
`go test -race -p 1 ./cmds/delidev-cli/internal/harness/codex ./cmds/delidev-cli/internal/cli -run '^(TestManualNativeForkSmoke|TestManualNativeCLISessionFork)$' -count=1 -v`
produced these distinct results:

- `TestManualNativeForkSmoke` passed in 24.15 seconds: the actual native child
  inherited the exact completed history, used its own cwd/runtime roots, continued
  once and left the source rollout bytes unchanged.
- `TestManualNativeCLISessionFork/general-chat/execute` passed in 97.78 seconds:
  the public CLI/Connect/Worker flow retained source queued input, created one
  request-replayed child with an empty queue, copied independent owned files,
  completed two child turns across native process replacement and preserved the
  source session state.
- The two-repository subtest timed out during ordinary source-workspace
  preparation, before Fork. Its retry after the asynchronous fixture adjustment
  used `GOMAXPROCS=2 ... go test -race -p 1 ./cmds/delidev-cli/internal/cli -run '^TestManualNativeCLISessionFork/worktree/execute$' -count=1 -v`
  and failed during native harness discovery, before source creation or Fork.
  Installed-native two-repository acceptance remains unverified. The deterministic
  two-repository copy/cleanup and server publication evidence above is separate.

An earlier native attempt also timed out during child `StartTurn` and source
harness discovery. These failed attempts are retained as limits, not passing
native evidence.

### Full-suite failures and comparisons

- Required root `go test -race ./cmds/delidev-cli/...` completed with exit 1.
  Failures included ordinary CLI workspace acceptance, Grok discovery, native
  approval/handshake and JSON-RPC fixture timeouts, workspace-diff fixtures, and
  10-minute package timeouts in Claude, Codex, Grok, server, store, Worker and
  workspace suites. This is not a passing full Go validation.
- A bounded repeat,
  `GOMAXPROCS=4 go test -race -p 1 -parallel 1 -timeout=30m ./cmds/delidev-cli/...`,
  again failed `TestCLISessionAcceptanceQueueAndArchive` during ordinary source
  preparation. It was stopped after that established failure; remaining packages
  in this repeat are unverified.
- On the unchanged main checkout at
  `74701b8948694e2bf8f8ba6d07e596c2d2f358a7`,
  `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/cli -run '^TestCLISessionAcceptanceQueueAndArchive$' -count=1 -v`
  also failed during ordinary source-workspace preparation (76.17 seconds). This
  reproduces that one CLI failure on main; it does not establish a full baseline
  result or explain every other Go failure.
- Required `pnpm test` in `apps/delidev` completed with 941 passing and 20 failing
  tests. A one-worker Vitest repeat had 944 passing and 17 failing tests. A full
  `VITEST_MAX_WORKERS=1 pnpm test` repeat completed Vitest with 959 passing and two
  5-second timeout failures out of 961 tests: Settings draft discard and tray
  navigation. Later commands in that chain were not reached; their independent
  successful runs are listed above.
- A focused retry kept the original deadlines: tray navigation passed; Settings
  draft discard still timed out. Against unchanged main frontend source at
  `74701b8948694e2bf8f8ba6d07e596c2d2f358a7`, the same Settings test also timed out.
  That comparison reused the installed pinned frontend dependencies and the
  implementation's additive generated API-client package through the dependency
  links; it is a frontend-source comparison, not a complete baseline suite.

Host load averages exceeded 200 during these runs and were observed above 700.
This is evidence of contention, not proof that every failure is environmental.
No committed test deadline or production timeout was relaxed, and the full suites
remain explicitly non-green. Native account inference, Windows/Linux runtime,
release and packaging installation acceptance were not performed.

### Generated inputs and cleanup

The exact consumed desktop icon LFS path was hydrated before desktop packaging
checks. Required API-client and embedded asset outputs were generated explicitly
before compilation and hooks. No migration or destructive backup was needed.
Repository-owned generated `dist` directories are removed before publication;
logs, fixture state, downloaded native binaries and generated output are not
committed. No Rust source changed, so root Cargo tests were not required.
