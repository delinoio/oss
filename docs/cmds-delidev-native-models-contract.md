# DeliDev native Codex model observations

## Scope

[Issue #1206](https://github.com/delinoio/oss/issues/1206) requires an explicit,
Worker-, installed-version- and selected-account-scoped native model observation
flow. The API-account path exposes durable observation controls and explicit
registration preparation. Managed-subscription observation remains typed unsupported
until its complete protected lifecycle is available. Go server, store, Worker
and CLI ownership remains under `cmds/delidev-cli`, with authenticated Connect in
`protos/delidev/v1`, generated clients in `packages/delidev-api-client` and desktop
controls in `apps/delidev`.

Initially support installed Codex `0.151.0` only. Native observations remain
separate from the implemented [provider HTTP catalog](cmds-delidev-catalog-contract.md).
Discovery never automatically registers canonical models. Other harnesses or
versions, background refresh, provider HTTP discovery changes, execution
verification, quota discovery, login implementation and external credential
import are outside this issue.

## Runtime and Language

Go owns authorization, durable jobs, publication and the bounded native adapter.
One owned native app-server runtime collects every page of an observation through
the pinned `model/list` protocol. TypeScript desktop presentation uses generated
Connect/Connect Query bindings and cannot become a second business authority.

## Users and Operators

Owner and paired clients explicitly select a Worker, its installed Codex and an
account. Only that current owning Worker may execute or report its accepted
discovery job. Worker credentials cannot invoke owner/client discovery acceptance,
observation controls or canonical model registration.

## Interfaces and Contracts

Main-established `SystemCapability.SYSTEM_CAPABILITY_NATIVE_CODEX_MODEL_DISCOVERY_V1 = 16`
and `WorkerCapability.WORKER_CAPABILITY_NATIVE_CODEX_MODEL_DISCOVERY_V1 = 7`
activate the API observation boundary. The [structure contract](cmds-delidev-structure-contract.md)
still requires shared reservations to reach main before dependent implementation;
other pending reservations never enter status or Worker capability negotiation.

`NativeModelService` in `native_models.proto` owns `DiscoverNativeModels`,
`GetNativeModelObservation`, `ListNativeModels` and `CancelNativeModelDiscovery`.
The discovery mutation selects the exact machine revision plus account revision.
Public job resources omit the private executable path and full observation body;
only authenticated original Worker assignments receive the executable selection.
Observation pages expose the validated complete model projection. Scope revisions
and installation generations in job JSON are decimal strings, preserving uint64
precision.

CLI controls are `model native-discover`, `model native-observation`,
`model native-list` and `model native-cancel`. The desktop Agent Worker wizard Model
step has an explicit native observation disclosure, selected-account and Runner
Device scope, status/cancellation, immutable pages and retained last-success
selection. Use model selects the executable ID; only final atomic Worker saving
resolves or creates its canonical model.

The feature exposes authenticated owner/client acceptance,
status, bounded observation pages and cancellation, with equivalent explicit CLI
and desktop controls. Mutation receipts use original UUID-v7 request identities
and exact revisions. An accepted durable Worker job binds machine identity and
revision, installation generation and version, selected account identity and
revision, connection identity and `include_hidden`, which defaults to false.
Installation selection must retain the independently verified executable identity;
neither PATH fallback nor a replacement installation supplies the original scope.

Return immutable bounded observations with their original scope identities,
observation time, completion or sanitized failure, and the last successful
observation for the selected scope. Preserve native picker `id` separately from
executable `model`. Reasoning, modality and service-tier metadata are advisory;
listing cached, fallback or static native data cannot prove current account
support, fresh entitlement, readiness or execution capability.

Collect all pages in one owned runtime before publication. Reject repeated
cursors, inconsistent or duplicate native identities, malformed or oversized
output, secret reflection and more than 10,000 entries. Do not publish a partial
observation after failure. Public pages retain the existing 1–200/default-50
bounds and opaque observation-bound cursors; continuing an old page cannot select
a newer observation or a different account/installation scope.

Registration remains an explicit model-save CLI/RPC action or the final atomic
Worker save. Use the selected account's provider identity and observed executable ID,
preserving manual provenance, canonical duplicate and alias rules, revision
checks, idempotent save and user-selected metadata. Discovery changes no canonical
model, account readiness or quota, session or execution capability. Existing
manual entries survive omitted entries and failed discovery.

Recheck original account/connection, installation, Worker and actor ownership at
publication. Account revocation/disconnection, installation replacement,
cancellation or ownership loss fences stale results. Exact receipt replay after
restart returns the original operation without another native launch.

## Storage

The server retains accepted operations and immutable complete observations
durably, separately from canonical model registration and account readiness.
Acceptance, receipt and job publication must be atomic. Successful observations
cannot be replaced by partial failed data; failed operations retain the selected
scope's last successful observation without claiming it is fresh.

Accepted discovery uses the existing generic job, receipt and indexed parent/machine
records. Immutable successful job output holds the complete observation; last-success
lookup binds account, machine, installation generation/digest, connection and hidden
selection. This adds no executable database migration or new migration version.
Schema 32 initializes the current accounting and diagnostic structures directly.
Historical allocations through 31 retain their owners and cannot be reused.
Any future schema allocation must reach main before dependent implementation;
no prerelease database upgrade or historical backfill is supported. A protocol
reservation cannot authorize a database version or an empty migration.

## Security

API discovery runs in a fresh private credential-free native profile. It receives
no upstream key or execution token, creates no thread, performs no inference and
does not invoke provider endpoints. Reconstruct the private environment and never
borrow host login or external native configuration. Pin the detected Codex executable
by SHA-256, verify an exclusive private copy before launch, and recheck the source
identity before returning. The 30-second profile validates effective ephemeral
credentials and a fresh keyless Responses provider namespace with a loopback
sentinel. For the pinned native model manager this disables remote refresh; its
listing is explicitly advisory static/fallback metadata. The aggregate native and
retained observation budgets are each 768 KiB. The closed reasoning projection
includes none/minimal/low/medium/high/xhigh/max/ultra/persistent; unknown
model-defined efforts remain unsupported until reviewed. Join owned process/file
cleanup before treating the operation as complete.

Subscription discovery depends on [issue #1095](https://github.com/delinoio/oss/issues/1095)'s
exclusive account lease and protected bundle transfer, refresh writeback and
cleanup boundary. Until that complete boundary is available, report typed
unsupported. When enabled, serialize discovery with the account's other native
operations and preserve generation fencing, recovery-required on lost writeback,
and independently confirmed cleanup before releasing credentials. Cached fallback
and a successful account read cannot establish freshness or readiness.

Secrets, raw native data and private paths stay outside diagnostics and ordinary
outputs. Only validated bounded public model metadata enters observations.

## Logging

Use `log/slog` with original operation/request, machine/account identities, phase,
duration, bounded model counts and closed completion/failure codes. Do not log
credentials, native response bodies, metadata content, private paths or provider
endpoints. Distinguish acceptance, native observation, cleanup and publication.

## Build and Test

Changes require allocation/structure checks, the protocol validation/generation-
freshness pipeline, Go race tests and vet, desktop tests and opt-in isolated native
tests (`DELIDEV_NATIVE_MODEL_EXECUTABLE` selects the installed 0.151.0 binary),
with the following issue acceptance scenarios:

1. Collect a multi-page API fixture and assert exact scope identities, no
   credentials, provider calls or inference, and unchanged catalog/readiness.
2. Exercise hidden models, distinct picker/executable IDs, repeated cursors,
   duplicates, oversized output and secret reflection. Fail boundedly without
   partial publication.
3. Revoke/disconnect the account or replace the installation during discovery and
   reject stale output. Replay the original receipt after restart without a second
   launch.
4. Explicitly register one observed entry and assert one manual model, idempotent
   save and preservation of existing entries after a later discovery failure.
5. Once #1095 is available, serialize discovery behind another account operation.
   Exercise cached fallback, refresh writeback and cleanup failure without false
   readiness or release of stale credentials.

Keep fixture/native-protocol validation distinct from unperformed real
subscription, platform and release evidence. Record new validation in
pull requests, issues and CI logs/artifacts.

## Dependencies and Integrations

The existing account, catalog, native harness, owned process, storage and Connect
contracts remain authoritative. The API path activates its main-established
reservations; managed-subscription support additionally requires #1095.
Regenerate Go and TypeScript bindings from reconciled schemas when activation
changes the protocol. Never edit generated outputs or activate a reserved
capability solely because its number exists in the ledger.

## Change Triggers

Update this contract and affected account/catalog/protocol/desktop contracts when
observation ownership, native versions, publication fencing, credential profiles,
registration semantics or paging bounds change. Keep scoped AGENTS rules aligned
with ownership/policy changes and the project index aligned with its domain links
and cross-domain invariants. New shared numbers and migration versions must be
established on main before dependent implementation.

## References

- [Project](project-delidev.md)
- [Repository defaults](repository-defaults.md)
- [Structure and compatibility](cmds-delidev-structure-contract.md)
- [Catalog](cmds-delidev-catalog-contract.md)
- [Account lifecycle](cmds-delidev-accounts-contract.md)
- [Native harness adapter](cmds-delidev-harness-contract.md)
- [Owned process](cmds-delidev-process-contract.md)
- [Storage](cmds-delidev-storage-contract.md)
- [Connect protocol](protos-delidev-v1-contract.md)
- [Pinned native model protocol](https://github.com/openai/codex/blob/78c290807ce710180111df227df3b7a4fe845452/codex-rs/app-server-protocol/src/protocol/v2/model.rs)
- [Pinned reasoning enum](https://github.com/openai/codex/blob/78c290807ce710180111df227df3b7a4fe845452/codex-rs/protocol/src/openai_models.rs)
- [Pinned official model-list tests](https://github.com/openai/codex/blob/78c290807ce710180111df227df3b7a4fe845452/codex-rs/app-server/tests/suite/v2/model_list.rs)

## Inline Worker models and endpoint-only completion reservation

Native model observations remain an independent bounded read-only diagnostic. Remove registration and use-model actions; never feed native observations into API completion or a saved Model registry. Preserve original Account/Worker/native-job ownership and diagnostic acceptance limits.

Follow the complete [catalog amendment](cmds-delidev-catalog-contract.md#inline-worker-models-and-endpoint-only-completion-reservation) and [current-only reset](cmds-delidev-structure-contract.md#pre-release-compatibility-reset). This reservation changes no runtime support or native/account acceptance.
