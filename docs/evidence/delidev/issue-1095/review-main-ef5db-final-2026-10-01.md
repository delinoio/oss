# Issue #1095: explicit-network main merge and final focused checks

The final inventory found new main conflicts after the earlier complete Go run.
Merge `8211985c167741805399f7b608595c46fea543fb` combines repair/evidence head
`db631d252a482a77dd93b49d5cfb1e2d2c7f03cf` with freshly fetched main
`ef5dbf8974ee69fb262cf66fc174f91e97a0e86f` (explicit server outbound proxies,
PR #1216). No rebase, force-push or intermediate push was used.

The twelve conflicted files were composed by their owning contracts. Both sets
of domain/server/store/protocol/client instructions remain. Restore retains the
current network profiles, immutable selections and required machine metadata;
pending private network credential intents and unsettled subscription ownership
both block replacement. Historical subscription generations remain fenced.
The API-only Fork restriction and every network invariant remain in the project
index. The three repaired subscription runtime files are byte-identical to their
pre-merge versions.

The combined `delidev.proto` imports both NetworkService and SubscriptionService.
The compatibility generator retains discovery from compiled public imports,
keeps historical relocation order separate and preserves main's generated query
facades. Generated Go/TypeScript outputs were recreated with `pnpm proto:generate`,
including the legacy SubscriptionService query facade; no generated conflict was
resolved by selecting one side. No new allocation or migration is introduced.

All following checks ran on that composed source on macOS arm64 with synthetic
credentials and temporary test state:

```sh
GOMAXPROCS=2 go test -race -p 1 -timeout 20m -count 1   ./cmds/delidev-cli/internal/outbound   ./cmds/delidev-cli/internal/domain   ./cmds/delidev-cli/internal/providers   ./cmds/delidev-cli/internal/integrations/github   ./cmds/delidev-cli/internal/apiproxy   ./cmds/delidev-cli/internal/cli   ./cmds/delidev-cli/internal/server   ./cmds/delidev-cli/internal/store   ./cmds/delidev-cli/internal/worker   -run 'Network|Proxy|Subscription|ManagedExecutionPreNative|ManagedUnused|ManagedLifecycle|ManagedSubscription|BackupRestore|Restore|StatusPreserves'
GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...
GOMAXPROCS=2 go test -p 1 ./protos/...
DEVHUD_PROTO_BASELINE=ef5dbf8974ee69fb262cf66fc174f91e97a0e86f pnpm proto:check
node --test scripts/ci/delidev-structure.test.mjs scripts/ci/delidev-proto.test.mjs
```

The selected Go regressions passed in all nine packages (exit 0): outbound
1.725s, domain 1.425s, providers 1.500s, GitHub integration 1.500s, API proxy
1.760s, CLI 21.138s, server 42.307s, store 60.114s and Worker 6.683s. This is a
focused selection, not a complete Go suite. Vet and protocol bindings/reflection
passed. Protocol lint/breaking checks passed against fetched main; forced
regeneration left no tracked or untracked binding drift. All six structural and
allocation tests passed. The compatibility tests independently cover aggregate
reflection round-trip and canonical registry identity; client network tests
cover legacy NetworkService and query facade exports.

From `packages/delidev-api-client`, `pnpm typecheck` passed and `pnpm test` passed
all 47 tests across six files (6.85s). Windows amd64 Worker and server test
packages also cross-compiled successfully with `GOMAXPROCS=2 GOOS=windows
GOARCH=amd64 go test -p 1 -c -o <external-binary> <package>`. Compilation does not
run Windows tests or prove installed-native/platform acceptance. Command logs
and binaries remain in this maintenance run's external temporary directory.

The [earlier complete run and controls](review-complete-a629703b8-2026-10-01.md)
remain preserved: exact source `63fecacdd` completed with eighteen passing
packages, five failing packages and two without tests. CLI, discovery/harness,
Claude, Grok and server failed, including two cumulative 20-minute package
deadlines. Worker, store, subscription and Workspace passed. That complete run
predates the lifecycle revision follow-up and this main merge; no complete final
source suite pass is claimed.

The prior published-head Windows Workspace diff timeouts remain unresolved.
No operation deadline, native proof or ownership cleanup check was weakened.
Real OAuth/inference, installed-native/platform/release acceptance, desktop
lifecycle controls and complete uncertain-lease recovery remain unverified.
Fresh CI and Codex review on the newly published head are checked by the next
scheduled heartbeat; previous-head evidence does not establish readiness.
