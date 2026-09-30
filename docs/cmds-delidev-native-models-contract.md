# DeliDev native Codex model observations

## Scope

[Issue #1206](https://github.com/delinoio/oss/issues/1206) requires an explicit,
Worker-, installed-version- and selected-account-scoped native model observation
flow. This contract records required behavior and pending protocol reservations;
it does not claim an implemented discovery capability. Go server, store, Worker
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

Reserve `SystemCapability.SYSTEM_CAPABILITY_NATIVE_CODEX_MODEL_DISCOVERY_V1 = 15`
and `WorkerCapability.WORKER_CAPABILITY_NATIVE_CODEX_MODEL_DISCOVERY_V1 = 5` in
`protos/delidev/allocations.json`. Neither member is activated in this prerequisite
change. The [structure contract](cmds-delidev-structure-contract.md) requires these
shared reservations to reach main before dependent implementation. Pending
reservations never enter status or Worker capability negotiation.

The feature implementation must expose authenticated owner/client acceptance,
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

Registration remains a separate explicit existing model-save action. Use the
selected account's provider identity and the observed executable native model ID,
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

This reservation change adds no executable database migration and does not choose
a new migration version. The executable registry remains at schema 24 and the
existing 25–27 reservations remain unchanged. If implementation needs a schema
change, establish its version on main first and preserve the complete preceding
sequence, backup-first atomic upgrades and historical records. A protocol
reservation cannot authorize a database version or an empty migration.

## Security

API discovery runs in a fresh private credential-free native profile. It receives
no upstream key or execution token, creates no thread, performs no inference and
does not invoke provider endpoints. Reconstruct the private environment and never
borrow host login or external native configuration. Join owned process/file
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

The reservation prerequisite requires allocation/structure contract checks and
the normal protocol validation/generation-freshness pipeline. It does not supply
native discovery acceptance evidence. Feature implementation must run Go race
tests and vet, protocol checks, desktop tests and opt-in isolated native tests,
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
subscription, platform and release evidence. Retain new validation in independent
`docs/evidence/delidev/issue-1206/` files.

## Dependencies and Integrations

The existing account, catalog, native harness, owned process, storage and Connect
contracts remain authoritative. The API path can be implemented after its shared
reservations reach main; managed-subscription support additionally requires #1095.
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
- [Pinned official model-list tests](https://github.com/openai/codex/blob/78c290807ce710180111df227df3b7a4fe845452/codex-rs/app-server/tests/suite/v2/model_list.rs)
