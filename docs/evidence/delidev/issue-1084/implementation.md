# Issue #1084: explicit server outbound proxies

## Inspected and implemented source

The replacement starts from freshly fetched main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` and implements the proxy boundary in `0beec65df1b0fc3ace5cfc36613c93f6893a0b61`. PR #1113 was closed without merging during the source-ownership refactor. Its repository-owned proxy implementation was retained and composed with the current split server/CLI/protocol layout; its unrelated CI, clibox and frontend-test changes were not imported. Existing main wire assignments remain unchanged, and the pre-reserved network resource 28/29 and capability 6 values are activated.

Profiles and immutable server/per-Worker selections use existing schema-24 entities/events/receipts. Protected credential generations remain in the server vault. All production provider inspection/catalog, inference relay and GitHub adapters receive only the server-selected route. The new service owns `network.proto`, generated clients and CLI operations. Compatibility generation discovers new independent service outputs without changing the historical declaration relocation map. Every network RPC is bounded to 30 seconds; the CLI waits 35 seconds, with a real delayed-response regression beyond its former 15-second header timeout.

## Executed verification on macOS arm64, 2026-09-30

- Focused Go race fixtures passed for explicit CONNECT/HTTPS/SOCKS5 routing, destination TLS, exact bypass, ambient isolation, no direct fallback, cancellation, credential reflection, provider/GitHub/inference integration and server profile/selection/deletion lifecycle. The first focused regex did not select the CLI-prefixed tests. A separate `GOMAXPROCS=4 go test -race -p 1 ./cmds/delidev-cli/internal/cli -run '^TestCLINetwork' -count=1` passed both lifecycle/exact-retry and delayed-response regressions.
- `GOMAXPROCS=4 go vet -p 1 ./cmds/delidev-cli/...` passed.
- `GOMAXPROCS=4 pnpm proto:check` passed lint, breaking and forced regeneration. `git diff --exit-code -- protos/gen packages/delidev-api-client/src/gen` confirmed no drift. Six structural/protocol fixtures and the Go aggregate-descriptor compatibility test also passed.
- Client typecheck and all 45 tests passed, including independent network exports, legacy reflection/query paths and real temporary Go-server synchronization. An earlier cold client fixture hit its 120-second Go-build limit; a direct CLI build passed and the uncached retry with bounded Go compiler parallelism passed.
- The initial frontend command passed 958 tests but two unrelated existing App/tray tests hit their five-second limits. The complete rerun, `GOMAXPROCS=4 GOFLAGS='-p=1' VITEST_MAX_WORKERS=2 pnpm test` from `apps/delidev`, passed all 960 tests without changing assertions or timeouts, eight bundle fixtures, sixteen native-launcher/asset fixtures, widget checks and the production build.
- The exact DeliDev icon LFS object was hydrated before frontend packaging checks; `git lfs fsck --objects` passed. Generated client, administrator and ach embed output was built explicitly for checks/hooks and will be removed from the final worktree.

## Pending verification

The complete `GOMAXPROCS=4 go test -race -p 1 ./cmds/delidev-cli/...` run is still running. It has reported `TestCLISessionAcceptanceQueueAndArchive` failing with an unavailable workspace file reader and three harness-discovery deadline/classification failures. The unchanged-main comparison is in progress. This record does not claim that the complete race suite passed or that the failures have been established as pre-existing.

## Evidence limits

These checks use temporary SQLite/state, scripted local HTTP/TLS/CONNECT/SOCKS5 endpoints and fixture-owned credentials. No real provider/GitHub accounts, enterprise proxy network, native proxy-credential lifecycle, Windows/Linux runtime or release publication was exercised. Worker bootstrap, credential transfer and native harness proxy application remain the separate #1085 boundary. No Rust or desktop presentation source was changed.
