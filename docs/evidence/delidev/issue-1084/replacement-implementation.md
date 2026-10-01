# Issue #1084 replacement implementation

## Inspected base and provenance

The clean replacement branch starts at freshly fetched main
`ad0e3e9a29cb3d8375ab5d168bb160c35a023250` on 2026-09-30. At that revision the
provider, native inference relay and GitHub adapters retain direct transports;
closed, unmerged PRs #1113 and #1170 do not establish issue completion.

The replacement reuses the source implementation from `0beec65df1b0fc3ace5cfc36613c93f6893a0b61`
and the short-credential guard repair from `204826a41b8f0252e3b134d76157101b8d9e82f0`.
It composes current permanent-session-deletion and Claude ownership rules rather
than restoring an earlier tree. All generated output is regenerated from the
combined canonical schema. No former branch's validation is claimed as newly
executed evidence here; the historical ledger is unchanged.

## Implemented boundary

Go owns closed Direct/HTTP/HTTPS/SOCKS5 definitions, exact bounded bypasses,
write-only vault credentials, immutable selected profile revisions, server and
independent Worker generations, authenticated NetworkService and CLI parity.
Existing schema-24 resource/event/receipt transactions retain metadata only;
pre-reserved EntityKind 28/29 and SystemCapability 6 are activated without
renumbering or migrating historical layouts. Worker metadata exports bind the
exact desired generation and expire after five minutes; they contain no secret
and do not prove bootstrap installation or native application.

All production provider catalog/validation, inference and GitHub clients use the
server route, retain destination/account authority and TLS verification, refuse
redirects, and have no ambient, direct or alternate-profile fallback or automatic
retry. CONNECT tunnels plaintext HTTP as well as HTTPS, preventing proxy
credentials from entering origin request headers or URLs. SOCKS5 authentication
and all handshakes are bounded and cancellable.

New handoff fixtures cover both CONNECT and SOCKS5 for each client, deny direct
access, return 302/307 to an independent recorder, and assert exactly one original
transmission and zero redirected/direct requests. Transmitted cancellation joins
origin and client/relay work without repetition. Relay revocation deliberately
aborts the downstream response; an initial test assertion incorrectly required a
synthetic error response and was corrected to accept the existing abort outcome.
The obsolete assertion run was stopped before starting full validation again.
Public snapshots, event history, SQLite and structured logs are inspected for
both original and replacement proxy credentials. Independent permanent-deletion
and existing system capabilities remain advertised.

## Newly executed validation

- `pnpm install`: passed, including linked-worktree Lefthook installation.
- `pnpm proto:generate`: passed after installing the workspace dependencies.
- `go test -race -p 2 ./cmds/delidev-cli/internal/outbound ./cmds/delidev-cli/internal/providers ./cmds/delidev-cli/internal/apiproxy ./cmds/delidev-cli/internal/integrations/github ./cmds/delidev-cli/internal/domain -run 'Network|Proxy|Credential' -count=1 -timeout 5m`: passed.
- `go test -race -p 2 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli ./cmds/delidev-cli/internal/apiproxy ./cmds/delidev-cli/internal/providers ./cmds/delidev-cli/internal/integrations/github -run 'Test(Network|CLINetwork)' -count=1 -timeout 3m`: passed with corrected cancellation assertions.
- `go test -race -p 2 ./cmds/delidev-cli/internal/server -run TestNetwork -count=1 -timeout 3m`: passed after adding snapshot/history/log isolation and permanent-deletion-capability assertions.
- `go vet -p 2 ./cmds/delidev-cli/...`: passed.
- API client `pnpm typecheck && pnpm test`: passed; all 45 tests pass.
- `pnpm proto:lint && pnpm proto:breaking`: passed. A subsequent attempted nonexistent `scripts/delidev/*.test.mjs` glob failed locally; the correct CI fixture paths below were then used.
- `node --test scripts/ci/delidev-structure.test.mjs scripts/ci/delidev-proto.test.mjs scripts/ci/proto-breaking.test.mjs`: all seven tests passed.
- `git lfs pull --include='apps/delidev/src-tauri/icons/icon-source@2x.png'`: passed; the asset is hydrated before frontend packaging tests.

Full Go race validation, complete desktop `pnpm test` and post-commit protocol
freshness remain pending at this initial implementation commit. The unrestricted
frontend run was stopped after timing failures; its retry temporarily caps Vitest
at two workers, restores the original config on exit, and changes no test bodies.
Completed results are recorded in a separate evidence file.

## Acceptance limits

Controlled loopback fixtures and injected temporary vaults do not establish real
provider/GitHub accounts, enterprise proxy networks, native credential-store
lifecycle, Windows/Linux runtime, release acceptance or Worker bootstrap. No user
credentials, real account inference or native proxy installation were exercised.
