# DeliDev Saved Client Connections

## Scope
`cmds/delidev-cli/internal/connections`, the local `connection` CLI commands and the existing Worker/device pairing primitive own named client-side server pairings. They provide the durable authority boundary for desktop remote selection. The desktop now opens a separately pinned native window per saved profile under the desktop contract; actual remote TLS/streaming/platform evidence remains distinct from loopback fixture acceptance.

## Runtime and Language
Go owns profile validation, exact pairing identity, private persistence and authenticated status verification. Native desktop infrastructure may call the same bounded commands and retrieve only the fixed profile's paired credential after their verification. Product traffic remains direct TypeScript/Go Connect RPC; a saved profile is not an agent-traffic proxy.

## Users and Operators
A user with an explicitly supplied short-lived client pairing grant may add a connection on this computer. Server owners or authorized clients create grants through the existing Device service. This local infrastructure does not read server owner credentials or change remote listeners/services. Explicit profile-owned Worker registration uses only that paired client's Device authority to register this computer.

## Interfaces and Contracts
- `connection list` reads at most 32 saved profile records without initializing absent state. A malformed/foreign/inaccessible entry fails the inventory; it is never silently omitted as if the remaining list were complete.
- `connection pair --id <uuid-v7> --name <name> --code-stdin` accepts one private grant of at most 32 KiB. The caller retains the explicit profile ID across retries. Name, exact grant/endpoint/server/pairing identity and creation time are pinned before any PairDevice request. Reusing an ID with different input conflicts.
- `connection inspect --id <id>` is offline metadata inspection. `pending` means local pairing completion has not been pinned; `paired` means this original device credential was durably retained, not that it is authorized or online now.
- `connection retry --id <id>` uses only the original private grant and Worker pairing journal/completed credential. It never generates a replacement request/token after original journal loss. A crash before journal creation can require explicit inspection/new scope; absence does not prove non-acceptance.
- `connection verify --id <id>` performs one authenticated System status read, bounded by ten seconds, against only the pinned endpoint. Require the original server/device identity, current client version/protocol and a non-stopping server, then recheck retained profile metadata. Normal TLS verification, no redirect following and no inherited environment proxy apply. It cannot start or replace any server.
- `connection worker-register --id <id>` explicitly registers a separate Worker on this computer for the paired server. It verifies the original client first, persists grant request/code ownership before CreatePairing, then pins the returned grant and commits the pairing-start barrier before PairDevice. Retries reuse both original requests, including after either acknowledgment is lost. A completed registration verifies its original Worker authorization; revocation cannot create a replacement.
- `connection worker-inspect|worker-status|worker-start --id <id>` and `connection worker-stop --id <id> --generation <uuid-v7>` address only that profile's fixed Worker scope. Inspection/status are offline and non-creating, startup is explicit and detached, and stop retains its original process generation. These reuse the existing controller/cleanup semantics. Neither status nor Local proof implies current remote authorization; product RPCs reauthenticate Worker proof at acceptance.

These commands use the normal versioned JSON/error/exit contract and reject `--server`/`--token-stdin` overrides. Ordinary output contains only profile/version/name/opaque identities/endpoint/state/timestamps; no pairing code or device token is emitted. Product commands may still use the original private paired client scope through the existing CLI authorization path.

Profile endpoints must be unambiguous HTTP loopback or HTTPS origins: no userinfo, path beyond `/`, query (including empty `?`), fragment, escaped path, zone, alternate numeric host, noncanonical IP/port or non-ASCII/noncanonical DNS hostname. Use lowercase ASCII/punycode DNS labels and a canonical IP representation. This stricter new-profile boundary lets the desktop derive one exact CSP origin without granting URL-normalization aliases. It does not widen ordinary endpoint rules or silently rewrite an existing grant.

## Storage
Under the explicitly selected local data root, `connections.lock` serializes profile pairing and each `connections/<profile-id>/connection.json` is an atomically written private intent. It retains the original single-use/expiring grant until coordinated client-credential removal is implemented. This file is private input state, never product metadata. Its paired device lives in that profile's `client` subdirectory using the existing `device.json`, pairing lock and pending journal.

Record the original device ID after PairDevice and credential persistence. If profile completion publication fails, retry reuses the already retained credential. Once completion is pinned, a missing/damaged/mismatched credential is recovery-required and cannot be replaced by replaying the grant. Existing unexplained profile directories or missing intent records are not adopted. Inspect/list/verify/retry cannot initialize missing profiles; ordinary pairing is the only creator. Neither profile removal nor server revocation is inferred from local absence. Renaming and explicit coordinated removal remain separate capabilities; native selection uses this existing exact identity.

An explicit Worker registration adds private `local-worker.json` intent and a separate `worker` directory inside the original profile. The intent binds profile/client/server/endpoint, original grant request/code, returned pairing ID, pairing-start barrier and completed device/machine identities. Hold the existing connection lock while registering. Lost/damaged intent, foreign credentials and missing original pairing journals cannot be adopted or reconstructed. A crash between the pairing-start barrier and first pairing journal deliberately requires recovery, because absence cannot prove that PairDevice was never accepted. Worker status/stop remain available offline with retained valid private evidence. App/window exit cannot stop this Worker; its server lease, process generation and session cleanup retain their existing independent authority.

## Security
Every path component owned by this boundary must be a private real directory and every record a private regular file. IDs are canonical UUID-v7, never path fragments supplied as arbitrary filesystem locations. Network input uses the existing durable pairing request/token identity and validates acknowledged server/device ownership. Retrying a pending Worker/device pairing now also checks its original request ID and server/endpoint/pairing ownership before any request.

Keep private grant/token documents out of normal output and logs. Clear temporary raw input/persistence buffers after use; Go strings and process memory are not promised as locked memory. Never use another saved profile, server owner token or system login as fallback. Revoked credentials remain retained but verification fails; status failure cannot create an alternate device. No server database, owner identity, default Worker scope or listener is initialized by these commands. Only explicit `worker-register` creates its profile-owned Worker state.

## Logging
The CLI emits structured `client_connection` observations to stderr with closed operation, opaque connection ID and stable result/error code. Names, endpoints, grants, tokens and raw native/network error strings are excluded. Stdout remains one versioned JSON envelope.

## Build and Test
Run package Go tests with race detection and `go vet ./cmds/delidev-cli/...`. Tests use private real servers and client data roots, a loopback proxy that drops successful CreatePairing/PairDevice responses, and no user credentials/inference. Verify identical replay request bytes, separate owner/client/profile-Worker scopes across servers, persisted completion, independent client/Worker revocation, missing/foreign credential refusal, damaged/lost pending journal refusal, strict origins, bounded inventories and secret-free CLI output. Native fixtures exercise detached profile Worker start, generation-bound stop and fixed Local proof. Windows/Linux cross-builds prove compilation only; remote desktop TLS/streaming and actual platform UX require their own evidence.

## Dependencies and Integrations
Uses the existing System/Device Connect services, private filesystem primitives and Worker/device pairing implementation. No new server RPC, schema migration, credential broker, OS service or remote supervisor is introduced.

## Change Triggers
Update this contract, scoped CLI/desktop AGENTS, project index and evidence whenever profile lifecycle, endpoint rules, credential persistence/removal, native selection or per-server cache ownership changes.

## References
- [DeliDev project](project-delidev.md)
- [Complete requirements](cmds-delidev-requirements.md)
- [CLI/server/Worker contract](cmds-delidev-contract.md)
- [Desktop client contract](apps-delidev-desktop-contract.md)
- [Protocol contract](protos-delidev-v1-contract.md)
- [Evidence ledger](cmds-delidev-evidence.md)
- [Repository defaults](repository-defaults.md)
