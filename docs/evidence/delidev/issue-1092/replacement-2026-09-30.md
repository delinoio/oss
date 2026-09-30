# Issue #1092 replacement: current ownership and independent child lifetime

## Reconciled source

This replacement starts from main `ad0e3e9a29cb3d8375ab5d168bb160c35a023250`
and preserves the structural ownership contract, permanent session deletion,
existing capabilities and split service schemas. It reuses the preserved,
unmerged fork implementation through `e39db9cbfb9df3688659534afa59249a72891b63`;
closed PRs #1126 and #1176 were not completion evidence on main. Commit
`78ec46cd5` reconciles those sources. Commit `97627d007` makes child continuation
and deletion independent of parent records. Merge `77c6fd863` includes main's
Grok accounting and shared compaction reservations without activating compaction.

Capability 13 was already reserved on main. This feature adds no SQL migration;
the reconciled schema 25 belongs to the existing Grok accounting implementation.
Bindings were generated from the reconciled service schemas. The historical
issue-1092 README and frozen DeliDev evidence ledger remain preserved.

## New lifecycle composition

A published child's immutable seed retains the original Worker cleanup device
and digest-bound native runtime. Its first new input can continue after permanent
parent deletion; it does not read the source-owned fork job. Deleting a child
before its first input still creates a Worker cleanup obligation. Parent cleanup
retains published child history, and a pending unpublished fork prevents accepting
irreversible parent deletion. Definite failed forks retain source-owned cleanup.

Project copies retain the parent's actual snapshot path separately from the
stable original Git registration source. Both common directories must match
before publication. Removing the parent's managed worktree does not remove the
child's registration authority. Local sharing retains its existing no-checkout-
deletion boundary. Deletion logs contain only session identity, closed stage and
stable outcome code. Success responses retain correlation metadata.

## Executed acceptance

All native fixtures use generated private homes, temporary repositories and a
keyless scripted loopback provider. They do not use user Codex credentials,
provider accounts, seeded readiness or ambient native history.

The macOS arm64 Codex executable reports `codex-cli 0.151.0`; the retained
`codex-aarch64-apple-darwin.tar.gz` SHA-256 was independently recomputed as
`6409e2c65994d294a92bc7330148ebc447ab23908bd7f5c71e718425a622c965`.

- The installed native adapter fork smoke passed in 40.96 seconds. It verified
  exact terminal inherited history, separate cwd/runtime, one child continuation
  and unchanged original rollout bytes.
- The reconciled `TestManualNativeCLISessionFork` passed in 182.78 seconds with
  both General Chat and a project containing two dirty repositories. It exercised
  authenticated public CLI/Connect/Worker creation, exact receipt retries, an
  empty child queue despite queued source input, independent files and two child
  turns across process replacement. The original profile/account/model remains
  selected. Both subcases emitted their explicit acceptance log.
- Race-enabled server fork tests passed in 76.439 seconds; Worker fork cleanup
  passed in 3.552 seconds. These include root-only rejection, failed-copy/no-child
  publication, unchanged source, immutable continuation, parent deletion,
  unstarted-child cleanup ownership and direct-handler correlation retention.
- Real Git child cleanup after parent removal passed under the race detector in
  55.793 seconds, preserving the original checkout. The two-dirty-repository copy,
  second-repository failure and pre-native drift tests remain part of the suite.
- `pnpm proto:check` passed after reconciling current main. `go vet -p 1
  ./cmds/delidev-cli/...` passed. Protocol/structure CI-contract tests passed (6).
- The API-client suite passed all 44 tests. Frontend typechecking passed. The
  serial full frontend run passed 1,244 tests; one GitHub settings fixture setup
  hook exceeded its 150-second deadline. That exact fixture then passed on retry
  (12.68 seconds overall). Bundle dry-run (8), desktop launch/assets (16), and
  Widget fixture checks passed independently. Full suite failure is retained and
  is not described as a successful `pnpm test` invocation.

Native commands:

```sh
GOMAXPROCS=2 DELIDEV_NATIVE_THREAD_EXECUTABLE=/private/tmp/delidev-1092-codex/codex \
  go test -race -p 1 ./cmds/delidev-cli/internal/harness/codex \
  ./cmds/delidev-cli/internal/cli \
  -run '^(TestManualNativeForkSmoke|TestManualNativeCLISessionFork)$' -count=1 -v -timeout=12m
GOMAXPROCS=2 DELIDEV_NATIVE_THREAD_EXECUTABLE=/private/tmp/delidev-1092-codex/codex \
  go test -race -p 1 ./cmds/delidev-cli/internal/cli \
  -run '^TestManualNativeCLISessionFork$' -count=1 -v -timeout=12m
```

The first combined attempt passed the adapter smoke but failed CLI discovery
(General Chat) and the existing bounded fork wait (two repositories) under shared
host contention. The second command, against reconciled sources, passed both CLI
subcases without changing production or test deadlines. An initial broad App
run had 23 timeouts; its two fork UI tests passed. The later serial run passed all
frontend test assertions apart from the independent fixture setup timeout above.
The pre-reconciliation full Go run recorded an existing
`TestCLISessionAcceptanceQueueAndArchive` workspace-wait timeout. Full Go result
and CI readiness are tracked separately from these focused/native passes.

## Limits

This is the bounded same-account, same-machine, legacy-root Codex profile. It
rejects active, child, goal, paginated and unsupported auxiliary histories.
Sidechat, account/provider/model switching and other native fork profiles remain
outside this implementation. These local checks do not prove Windows/Linux
installed-native behavior, real hosted-account behavior, distribution or release
acceptance. The desktop action's deterministic tests do not replace packaged
native desktop acceptance. Unknown native/publication/cleanup outcomes remain
uncertain and never authorize creation replay.
