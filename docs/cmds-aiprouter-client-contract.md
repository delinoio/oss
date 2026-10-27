# AIPRouter client configuration contract

## Scope

Owns `cmds/aiprouter`: the Go CLI, device authorization client, client settings integrations, profiles, and user background HTTP relay.

## Runtime and Language

Go in root module `github.com/delinoio/oss`; platform targets and distribution are defined below.

## Users and Operators

End users connecting clients, including users approving a connection through a separate browser while using SSH.

## Interfaces and Contracts

### CLI configuration, profiles, and loopback relay

#### Distribution

- Executable: **`aiprouter`**.
- npm package: **`@delino/aiprouter`**.
- Go standalone binaries with checksums, plus an npm package that installs the matching platform binary.
- CLI targets: Linux x86_64/ARM64, macOS x86_64/ARM64, Windows x86_64.
- Support the pinned reference's automatic client configuration targets and document their actual path/format/platform constraints. Do not claim that an external client supports an OS that it does not support.
- Verify npm scope/publishing access before publication.

The inspected file-export registry includes qoder, qoder-cn, workbuddy, workbuddy-ai, opencode, pi, omp, hermes, openclaw, kimi, gajae, dsh, mcode, zcode, prime, aside, raycast, omo, cline, commandcode, kilo, droid, and gentle-shell. Dedicated native integration paths such as Codex/Claude/Grok must also be inventoried; the file-export registry alone is not the entire integration surface.

JSON, JSON5, YAML, TOML, active profile stores, source-preserving merges, and client-specific ownership checks must follow the relevant reference paths. Preserve their supported capabilities rather than assuming every client uses the same file or remote-key mechanism.

#### Safe settings changes and backups

- Show proposed changes and require confirmation before applying them.
- Back up original settings and offer restoration on disconnect.
- If the user changed settings after application, do not overwrite conflicts; explain recovery.
- Preserve original backup contents with owner-only permissions/Windows ACLs, including where original settings contain secrets.
- Delete backups after successful disconnect/restoration.
- If restoration conflicts, still attempt server-key revocation and stop the relay, but preserve conflicting settings and recovery backups for manual restoration.
- Preserve unrelated settings, profiles, secret values, and user edits.
- Remote-key placement into client settings requires the separate explicit confirmation described above.

#### Named connection profiles

- Support named server/organization connection profiles with separate credentials.
- Select an active profile per client.
- Switching profiles changes settings but retains the prior profile's key.
- Revoke a profile's key when disconnecting that profile.
- Run multiple profile relays concurrently on distinct loopback ports.
- Port conflicts cause an actionable startup failure; never silently choose another port.
- If the user changes a port, preview affected client settings and obtain confirmation before applying them.

#### HTTP relay lifecycle and trust boundary

- Provide a local HTTP relay only for clients that need it to connect to the remote service.
- Install as a user background service that starts at login, with status, stop, and removal support.
- Bind only to loopback.
- Do not require separate local authentication: other processes on the same computer can call it under the explicitly selected policy.
- Do not turn this relay into a local provider execution agent.

#### Optional native TLS compatibility

Claude first-party Desktop mode and its model picker, plus the pinned experimental Codex Desktop compatibility runtime, are included as explicitly selected client modes. They are separate from ordinary gateway configuration and do not introduce rollout feature flags or local executable provider backends.

- Before activation, preview the exact application mode, supported build, intercepted destinations/routes, loopback listeners, certificate fingerprint, trust-store changes, and settings changes. Require separate explicit runtime consent for certificate trust and application mutation; ordinary connection consent does not authorize these changes.
- Bind listeners to loopback only. Restrict interception to the pinned client destinations and protocol paths. Preserve upstream TLS verification; never install a general-purpose traffic interceptor or forward native account credentials to the router. Provider selection and usage authority remain on the server.
- Codex PAC routing selects the fixed `https://chatgpt.com` origin. Preserve native HTTP and WebSocket traffic to that origin; apply usage-response rewrites only to the pinned usage routes. A host-wide PAC must not make unrelated native routes fail merely because they are outside the rewrite set. Do not permit arbitrary-host forwarding.
- Authenticate local control requests and replies without sending a reusable control secret to an unverified listener. Authenticate a fresh, nonce-bound listener-generation challenge before sending an actionable control proof. Bind requests and replies to that listener generation; a captured request must not become valid after listener restart, even if it was never accepted by the earlier listener. Bind readiness to the active runtime generation and the fingerprint and expiry of its served leaf certificate. Renew the leaf safely or stop the runtime at expiry; an expired listener must not report ready. Keep proxy authentication separate from control authentication.
- Keep CA private material in user-scoped protected storage with the platform protection required by the pinned runtime. Never commit, log, or transmit it. Trust only the specific owned certificate in the current user's trust scope; unsupported platforms or builds must refuse clearly. Missing trust is pending, not successful activation.
- Keep a recoverable ownership record and protected original settings before mutation. Preserve user changes and unrelated certificates/proxies. Status, stop, disable, and disconnect must identify owned resources, stop owned listeners, and offer restoration and owned trust removal. Conflicts preserve backups and give explicit recovery instructions; failures must not report successful cleanup.
- Keep Codex observe behavior separate from an explicitly confirmed bounded account-UI trial. Do not claim that native quota UI changes establish provider isolation, billing credit, or server authorization. Assess only the exact pinned supported builds; unknown builds require explicit refusal rather than speculative mutation.
- Bind consent to the verified installation identity and observed generation, not only its version string. Same-version replacement invalidates that consent. Preserve the generation change across an installation changing from A to B and back to A, including while account or build checks are in flight.
- Test with temporary homes, certificates, mock trust adapters and loopback fixtures. Cross-build and fixture evidence does not establish real OS trust, native application, or live-account acceptance. Development tests must not enroll trust or change the developer's installed applications.

#### Disconnect

- Attempt to revoke the dedicated key on the server as well as cleaning up locally.
- If the server is unreachable, continue local cleanup and report that server revocation is incomplete, with web revocation instructions.
- Apply the separate backup-preservation rule when restoration conflicts.

## Storage

OS credential stores own profile keys. Client settings and protected recovery backups follow the exact settings lifecycle above. Do not use a normal credential file as an OS-store fallback.

Native Codex generated catalogs/cache and explicitly selected source catalogs accept up to **256 MiB**. Ordinary configuration and serialized state/pending metadata retain **4 MiB** bounds. Store backups larger than **64 KiB**, or smaller backups when metadata would exceed its bound, as owner-protected `<state-dir>/snapshots/<lowercase-sha256>.blob` files. Deduplicate only identical bytes. State and transaction journals retain digest references. Before loading shared immutable snapshot bytes, validate reference syntax, safe regular-file and parent rules, and the SHA-256 digest.

Keep original backups distinct from refresh restoration baselines. Current state and pending desired/rollback records both retain their referenced snapshots. Collect unreferenced blobs only after durable journal completion and validation of all surviving references. Remove only valid owned blobs. Malformed, missing, or corrupt references stop collection and preserve recovery material. Interrupted snapshot creation may leave protected orphan blobs until a later successful transaction. Snapshot blobs remain protected client-settings backups; they are never an OS credential-store fallback.

## Security

The [service key and device lifecycle](servers-aiprouter-service-contract.md) is normative for the CLI: original ten-minute authorization lifetime, five-second polling, device-secret-authenticated redelivery, storage confirmation, and compensating revocation. Failure to store a key leaves client settings unchanged. A key in client-owned settings requires separate explicit consent. Local relay callers share the selected local-computer trust boundary.

## Logging

Use structured logs and request identifiers. Never log provider secrets, API keys, device secrets, raw prompts, or raw responses. Distinguish unknown usage, write failures, cancellation, limits, and upstream/gateway errors. See the [observability contract](servers-aiprouter-operations-contract.md).

## Build and Test

Follow the release gates and required scenarios in [operations](servers-aiprouter-operations-contract.md). Run root `go test ./...` for Go changes. Passing package tests does not establish complete runtime or live acceptance.

Build all listed target platforms and test credential-store, service, permissions/ACL, restore, conflict, offline revocation, and multi-profile behavior through mocks. Builds and mocked OS behavior do not establish actual native OS acceptance.

## Dependencies and Integrations

- [servers-aiprouter-service-contract](servers-aiprouter-service-contract.md)
- [protos-aiprouter-management-contract](protos-aiprouter-management-contract.md)
- [packages-aiprouter-libraries-contract](packages-aiprouter-libraries-contract.md)
- [servers-aiprouter-operations-contract](servers-aiprouter-operations-contract.md)

## Change Triggers

Update this contract, the project index, and affected producer/consumer contracts together when interfaces, ownership, or lifecycle rules change. Follow the [instruction-update policy](README.md#instruction-update-policy); behavior belongs in contracts, not instruction files. Record implementation status, validation results, and unresolved limits in GitHub records or CI artifacts.

## References

- [Project index](project-aiprouter.md)
- [Repository defaults](repository-defaults.md)
- [Workflow](repository-workflow-contract.md)
- [Environment](repository-environment-contract.md)
- [Licensing](repository-license-contract.md)
- [Dependency security](repository-dependency-security-contract.md)
