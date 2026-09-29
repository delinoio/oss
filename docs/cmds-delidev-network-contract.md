# DeliDev explicit outbound network contract

## Scope
Issue #1084 owns server routing in `cmds/delidev-cli/internal/outbound`, revisioned domain/store records, authenticated NetworkService and equivalent CLI operations. Worker bootstrap installation and native harness proxy application belong to separate work.

## Runtime and Language
Go, the existing server SQLite authority and Connect RPC. No ambient proxy discovery is permitted.

## Users and Operators
Authenticated owners and paired clients configure named profiles and explicitly select a revision for the server or one registered Worker machine. Workers cannot invoke these owner operations.

## Interfaces and Contracts
NetworkService exposes SaveNetworkProfile, DeleteNetworkProfile, SelectNetworkProfile, GetNetworkRoute and ExportWorkerNetworkMetadata. System status advertises `SERVER_OUTBOUND_PROXY_V1`. Resource kinds `network_profile` and `network_route` participate in normal authenticated reads, snapshots and metadata events; generic configuration writes cannot create them.

Profiles use closed `direct`, `http`, `https`, `socks5` modes, a 128-byte name, canonical ASCII host/IP and nonzero port for proxies. Direct has no endpoint, bypass or credential. At most 128 profiles and 128 bypass entries per profile are permitted. Bypass entries contain exact host, literal IP or canonical CIDR plus optional port; no suffix, wildcard, environment or DNS-answer interpretation is allowed. DNS names are lowercase with no trailing dot; ambiguous numeric IPv4 spellings are rejected.

Save uses schema 1 definition JSON plus separate write-only credential JSON with visible ASCII username/password of 1–255 bytes; usernames cannot contain colon. Omission preserves an existing credential, explicit clearing removes its active association. Creation uses the request UUID as the stable profile ID. Every mutation binds the actor, original request UUID, exact expected revision and full canonical input; credential input is bound by a domain-separated keyed commitment, never an unkeyed digest. Selected profile revisions are immutable snapshots. Editing a profile requires a later explicit selection to change effective routing. Empty profile selection means built-in Direct. The server target is empty machine ID; each Worker has a separate target and monotonically revisioned desired generation. A selected profile cannot be deleted. In-flight requests retain their original pinned route; later selections affect new requests and never replay accepted work. Profile deletion proves native credential-store cleanup separately from completion of those bounded requests.

Metadata export is an authenticated, read-only operation for an exact Worker desired generation. Its signed non-secret metadata binds format version, server ID, Worker machine ID, selection identity/generation, immutable profile configuration, issue/expiry times and a random export UUID. It expires after five minutes. Preserve the exact returned metadata bytes and signature; numeric parsing/reserialization is not verification authority. It is not an encrypted credential bundle, Worker installation receipt, recipient binding or proof of native application; those belong to #1085.

CLI: `network profile save|delete|list|get`, `network select`, `network status` and `network export-metadata`. Mutations use the common request ID and exact decimal revisions; secret input enters stdin independently of public definition files. Ordinary output contains only public resources and signed non-secret metadata.

## Storage
Existing schema-24 entities, events and actor-bound receipts hold public profile/configuration and opaque credential generation references. No SQL layout migration is required. Credentials use the protected server vault's `network-proxy` purpose and immutable profile/request UUID references. At most 256 unresolved credential generations per profile are retained, including pinned selections and exact uncertain retries; profile deletion commits denial before retryable native cleanup. Save/deletion work has a 30-second cancellation bound, with exact receipts retaining cleanup retries. SQLite/configuration backups do not back up these credentials. Configuration import/export does not acquire network authority.

## Security
Only server selection affects catalog, credential validation, inference relay and every GitHub adapter. Worker selections never affect server routing or listener/account selection. Each HTTP attempt resolves one immutable route and obtains its exact protected credential. Requests have no direct or alternate-profile fallback, redirects or proxy-owned retries. Direct/exact-bypass routing pins localhost to actual loopback. Explicit proxy routing retains the destination authority, so server-local providers require an exact localhost/IP bypass. HTTP/HTTPS proxies tunnel destinations with CONNECT, including plaintext HTTP, keeping proxy authorization out of URLs and origin request headers. HTTPS proxy and destination TLS use normal certificate/hostname verification. SOCKS5 uses bounded context-aware authentication/dialing. CONNECT headers are bounded to 32 KiB; handshakes are bounded to ten seconds and cancellation closes owned connections. Existing client body/header/stream/deadline bounds remain in force. A bounded response guard withholds unresolved protected prefixes and rejects literal, JSON-escaped and Base64 credential reflections in headers/bodies; it does not claim arbitrary transformation detection.

## Logging
Structured `network_profile_saved`, `network_profile_deleted` and `network_route_selected` metadata includes opaque profile/route/machine IDs, generation, closed mode and typed result. No credentials, endpoint host/URLs, bypass entries, raw network errors or export authentication values enter logs.

## Build and Test
Run `go test -race ./cmds/delidev-cli/...`, `go vet ./cmds/delidev-cli/...`, and `pnpm proto:check`. Controlled CONNECT/SOCKS5/TLS fixtures must deny direct paths, prove exact bypass and no fallback, preserve cancellation and TLS verification, and cover all outbound client integrations plus Worker/server separation. Use temporary state and injected test vaults only. Fixture success does not prove enterprise proxy, real provider/GitHub accounts, native OS credential or Worker bootstrap acceptance.

## Dependencies and Integrations
Uses the existing server vault, resource authority and generated Go/TypeScript clients. Protocol implementation follows [Go HTTP transport](https://pkg.go.dev/net/http) and [context-aware SOCKS5](https://pkg.go.dev/golang.org/x/net/proxy).

## Change Triggers
Update scoped command/protocol/package AGENTS, the project index, CLI and evidence contracts when routing, secret ownership, generations or export semantics change.

## References
- [Project](project-delidev.md)
- [Protected credentials](cmds-delidev-credentials-contract.md)
- [Native relay](cmds-delidev-proxy-contract.md)
- [Repository defaults](repository-defaults.md)
