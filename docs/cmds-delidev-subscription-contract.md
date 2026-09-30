# DeliDev managed Codex subscriptions

## Scope

Issue #1095 implements dedicated Codex subscription login, refresh, execution and logout in `cmds/delidev-cli`, `protos/delidev/v1/subscription.proto` and the generated DeliDev clients. The server owns authorization, encrypted credentials, generations and exclusive account leases. The explicitly selected paired Worker owns installed native Codex processes and private authentication files. The complete product requirements remain in [issue #964's snapshot](cmds-delidev-requirements.md).

Existing-login import, externally supplied token bundles, internal-only `chatgptAuthTokens`, Claude subscriptions and concurrent use of one managed bundle are excluded. Desktop login controls, full native recovery, quota observation and real-account/platform acceptance remain separate work. This implementation does not complete issue #964 or claim a release.

## Runtime and Language

Go owns server and Worker business logic. The native profile pins installed Codex `0.151.0`; DeliDev never installs it. The managed profile uses the official app-server protocol, a fresh private `CODEX_HOME`, file-backed native authentication and the built-in OpenAI provider. API execution and discovery retain their existing ephemeral credential profile.

## Users and Operators

The server owner or an authorized paired client initiates lifecycle operations. A paired Worker advertises `managed-codex-subscriptions-v1` only after an empty-home native configuration handshake and owned cleanup; the server echoes this closed capability. Each operation selects an explicit execution machine. Workers cannot initiate owner lifecycle operations or read another machine's bundle.

Execution admission requires that selected Worker's negotiated managed capability before claiming a session or consuming queued input. Ordinary native installation discovery alone cannot admit subscription execution; an unsupported Worker leaves the original input queued without selecting a fallback.

## Interfaces and Contracts

Native subscription providers opt into the closed `subscription_harness: "codex"` field, retain `native-subscription` protocol and `subscription` authentication, and have an empty endpoint. Legacy providers without this selector remain metadata-only until explicitly configured; names never infer authentication authority. Accounts retain their independent enablement configuration.

Configure the provider selector before creating account references. Like endpoint, protocol and authentication changes, changing this selector requires removing the provider's account references first; configuration cannot replace an active account's authority.

`SubscriptionService.RequestSubscription` accepts login, refresh and logout with a UUID-v7 mutation receipt, current account revision and selected machine. `CancelSubscription` cancels the original pending login. `GetSubscriptionProgress` returns short-lived browser/device presentation data only to the original initiating principal. CLI equivalents are:

```sh
delidev account login --id ID --revision N --machine-id ID --device-code
delidev account login-progress --id ID --operation-id ID
delidev account cancel-login --id ID --revision N
delidev account refresh --id ID --revision N --machine-id ID
delidev account logout --id ID --revision N --machine-id ID
```

Omitting `--device-code` selects native browser login. Ordinary acceptance/status output contains references and state, never a login bundle. The explicit progress command presents the native one-time URL/code without persisting or logging them. Cancellation may race native login completion, but its durable state wins before connection publication.

Revoking a paired initiating client atomically cancels its still-queued login, refresh and logout operations. This grants no lease, changes no credential generation, and preserves logout's accepted execution revocation and any independently granted lease. A fresh authorized request may replace a canceled queued operation. Successful execution write-back may retain the newest protected generation but cannot restore readiness after accepted logout revocation; a fresh authorized logout must complete cleanup. Claimed operations retain their original ownership and recovery obligations; revocation never proves native cleanup.

An independently authorized outbound `WatchSubscription` lane supports four concurrent account operations per Worker. `TakeSubscription`, `PublishSubscriptionProgress` and `FinishSubscription` are restricted to the selected current Worker/device/instance. Bundles travel only in the protected Take/Finish messages, with a 64 KiB bound; they never enter jobs, execution output, events or ordinary resource responses. Take receipt replay never redistributes a bundle or authorizes another native launch.

One durable account lease spans login, refresh, execution or logout. Refresh/logout requests can wait behind an execution lease. Other sessions retain their selected account and wait after a definite busy refusal; unknown delivery is never retried. Other accounts acquire independent leases. Native execution uses an immutable subscription selector and the existing publication/history/workspace machinery. Its publication registration exposes no API proxy path and cannot obtain an API relay lease.

The lease pins operation, account generation, Worker machine/device/instance, server epoch and original lease revision. Completion checks that original revision while preserving later metadata edits and cancellation. The connection ID remains stable across bundle rotations. The final native bundle is saved under a new immutable generation, old protected references are tombstoned, and independently confirmed native/file cleanup precedes another grant.

Successful `account/read` does not prove refresh. Both Worker and server require changed token material, a strictly newer native `last_refresh` value and unchanged account/user identity. Server identity commitments enforce one managed owner for the same provider account/user across account aliases. Local logout proves native/file removal and server vault cleanup; provider-wide revocation remains best-effort and is never reported as confirmed.

The pinned native login-completed envelope includes nullable `onboardingEntrypoint` metadata. Accept null or the pinned closed `life_sciences` value without launching another onboarding flow or treating presentation metadata as authentication evidence; reject unknown values while still requiring the original login identity and successful completion.

## Storage

Optional server-owned `Account.subscription` JSON preserves historical account bytes when absent and adds no destructive schema migration. It retains only generation references, keyed identity commitments, pending-operation metadata, original actor and lease fences. Configuration writes cannot manufacture or change this state. Unresolved ownership prevents configuration deletion.

The existing OS-backed [credential vault](cmds-delidev-credentials-contract.md) stores bundles with `account-login` purpose and immutable UUID-v7 references. Native plaintext exists only in the leased Worker's private runtime; it is removed after owned process closure. Execution installs credential cleanup at the owned auth-file write boundary, before publisher setup and registration, for both current and predecessor runtime selections. Failures before native ownership remove and synchronize the original auth file; a failed native Open permits removal only when its process cleanup is independently confirmed. Uncertain native ownership retains the protected lease and cannot authorize optimistic deletion. Execution retains original history and checks its bounded remaining files for credential remnants. Worker journals contain only original claim/completion identities and cleanup outcomes, never bundle bytes or token digests. Temporary login presentation lives only in server memory.

Uncertain delivery or completion closes the Worker subscription lane and joins its children so the server records lost ownership. An acknowledged failed operation retains its confirmed completion and leaves unrelated accounts on that lane active. Missing refresh write-back, unconfirmed cleanup, Worker loss/replacement or server restart retains the original lease as recovery-required. The old generation cannot be redistributed. Recovery publication purges the in-memory login presentation under the account gate while retaining the original lease and pending operation. Progress reads reject recovery-required or previous-epoch ownership, and the lost Worker cannot republish a URL or device code. These lifecycle commands neither erase recovery evidence nor clear an uncertain lease; independent native recovery remains a separate product boundary.

## Security

Uncertain protected execution completion escapes ordinary job-error reporting and closes the primary work lane after joined cleanup. The original started job claim and protected completion journal remain retained; the server records the lost execution lease as recovery-required. Workspace cleanup failures preserve this completion uncertainty, and receipt replay cannot manufacture a successful write-back.

Remote transport retains authenticated TLS; loopback development retains the existing protected local RPC boundary. Authorization is rechecked after vault I/O and at publication, including the original initiating client's revocation. Managed login starts with no existing authentication file and requires the original native login-completed observation. Bundle validation rejects API keys, external auth modes, unknown credential fields, foreign identities, symlinks and oversized files. JWT claim comparisons are identity consistency checks, not signature verification or substitutes for native OAuth completion.

The merged native configuration must retain file storage, ChatGPT login and the built-in OpenAI provider without inherited endpoint, command authentication, bearer, query or header overrides. Native execution never inherits unrelated system logins. Private files do not promise an OS sandbox against unrestricted same-user access. All tests use isolated temporary state and synthetic credentials.

Both native thread start and resume recheck the merged configuration for the actual execution workspace before sending the mutation. A successful startup-directory handshake cannot authorize a workspace-specific provider or authentication override.

Credential cleanup scans retained native files for raw token material and padded or unpadded standard/URL Base64 copies under the existing file/count/byte bounds. Finding a remnant leaves cleanup unconfirmed and retains recovery ownership without erasing the original native history.

## Logging

Use structured `slog` events for accepted operations, grants, completion, capability availability and recovery failures. Log only opaque account/operation/lease/machine identities, closed action, cleanup classification and stable error code. Native protocol messages, token bundles, JWT identities, URLs, device codes and filesystem paths never enter these logs.

## Build and Test

Run `go test -race ./cmds/delidev-cli/...`, `go vet ./cmds/delidev-cli/...`, `pnpm proto:check`, and the API client's tests/typecheck. Generate bindings through pinned root Buf tooling. Controlled native-process fixtures cover browser/device progress, completion, cancellation, file rotation, unchanged refresh evidence, logout and symlink refusal. Real loopback Connect/SQLite fixtures cover lease races, protected-channel authorization, generation fencing, lost write-back, cancellation, identity uniqueness, independent accounts, API relay denial and secret-free outputs/database files.

These fixtures do not authenticate real accounts, execute hosted inference or establish installed-Codex/desktop/Windows/Linux/release acceptance. Record actual executed checks and their limits in the [issue #1095 evidence](evidence/delidev/issue-1095/validation.md).

The [current-main replacement record](evidence/delidev/issue-1095/replacement-2026-09-30.md) records subsequent integration and regression checks independently of that preserved historical evidence.

## Dependencies and Integrations

Reuse authenticated Connect, the server vault, current Worker discovery, owned process supervision and existing Codex session/history publication. No provider brokerage, native retry emulation, account/model failover or new dependency is added.

## Change Triggers

Update the account/harness/session/protocol/client contracts and affected scoped `AGENTS.md` owners when the native profile, public operations, generations or recovery boundaries change. Record validation in independent `docs/evidence/delidev/issue-1095/` files, preserving the historical ledger. Update the project index only for ownership, domain catalog or cross-domain invariant changes.

## References

- [Project index](project-delidev.md)
- [Repository defaults](repository-defaults.md)
- [Account lifecycle](cmds-delidev-accounts-contract.md)
- [Credential storage](cmds-delidev-credentials-contract.md)
- [Native harnesses](cmds-delidev-harness-contract.md)
- [Sessions](cmds-delidev-sessions-contract.md)
- [Wire contract](protos-delidev-v1-contract.md)
- [Pinned official account protocol](https://github.com/openai/codex/blob/d8673cb68e349c208659b986697773d3145dbb14/codex-rs/app-server-protocol/src/protocol/v2/account.rs)
- [Pinned native authentication storage](https://github.com/openai/codex/blob/d8673cb68e349c208659b986697773d3145dbb14/codex-rs/login/src/auth/storage.rs)
