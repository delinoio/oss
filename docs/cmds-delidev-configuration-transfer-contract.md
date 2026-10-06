# DeliDev Portable Configuration

## Scope

`cmds/delidev-cli` owns portable configuration validation, consistent export, change previews and atomic import. `apps/delidev` presents these owner/client operations through generated Connect Query bindings. Version 2 covers the eight existing editable configuration kinds: providers, models, account preferences, instruction templates, Agent Workers, repositories, projects and server preferences. Schedules, historical pricing versions, device-specific notification preferences, integration authentication and Worker installation settings are separate contracts, not silently included in this format. This increment does not complete the remaining issue #964 requirements.

## Runtime and Language

Go owns all validation, relationship remapping, authentication and SQLite transactions. React retains exact original JSON bytes for export, preview and apply; display parsing must never round-trip integer values through JavaScript numbers into authoritative documents.

## Users and Operators

An owner or paired desktop client can move supported non-secret configuration between explicitly selected servers. Import creates independent accounts requiring a new explicit connection and validation. It never reuses an account by alias, source UUID, provider name or available credentials. Workers cannot export, preview or apply owner configuration.

## Interfaces and Contracts

`ConfigurationService.ExportConfiguration` returns one consistent version-2 bundle with ordered entries and descriptive machine references. Export excludes authentication material, account connections/health/quota/catalog observations, model discovery provenance, device registrations, native installations, routing state and session/history contents. Provider-observed model metadata becomes user-declared advisory information; imported models are manual registrations. Instruction text, ordering, permissions, provider endpoints, project restrictions and preferred repositories remain exact. Unsupported integration references reject the complete export instead of disappearing. Server remediation defaults and optional complete repository overrides are included. Their Agent Worker references are remapped as configuration identities, and their execution machines require explicit mappings even when absent from all checkout lists. Stable GitHub reviewer actor/App IDs and permission selectors remain exact external identities; no current access or execution grant is imported.

`PreviewConfigurationImport` takes the bundle plus explicit configuration bindings, machine bindings and checkout paths. Every referenced machine needs an explicit one-to-one mapping to a registered enabled target machine, even when its UUID matches. Every checkout needs an explicit target path, including when reusing a repository. Unused, duplicate, cross-kind, unknown and missing mappings fail. Source resource UUIDs are document-local references; newly created target UUIDs are fresh. Both current global identities and permanent deletion tombstones are rechecked before acceptance and deferred publication, so a reused/deleted target becomes a terminal preview conflict rather than an endlessly failing Worker report. Existing providers/models/templates/Agents/repositories/projects may be explicitly reused only when their complete portable values match after remapping. Account reuse is forbidden. Only server preferences can replace an existing entry, with its exact expected revision; no implicit singleton overwrite is permitted. Model identity/alias conflicts, broken dependencies and incompatible model/account/harness references reject the complete preview.

The preview runs in an authorized read transaction without receipt, event, filesystem inspection or configuration writes. Its complete before/after plan includes target UUIDs, intended actions, expected revisions and target machine descriptors. A 24-hour signed token binds the exact plan, server and authenticated actor. Altering the document requires a new preview. All relationships and relevant current revisions are checked again at application and before deferred publication.

`ApplyConfigurationImport` requires the reviewed preview and a UUID-v7 mutation identity. Exact receipt replay returns the original job's current outcome without another import, including after preview-token expiry. New attempts must still validate the signed token. If later configuration deletion redacts an affected receipt, its original digest remains and replay reports NotFound without recreation; the known coordinator job remains independently readable. Receipt data is reference-only; request digests bind the actor and exact reviewed document. Metadata-only logs contain operation/request/job identity, state and replay status, never configuration text or paths.

CLI:

- `configuration export [--output PATH]`
- `configuration preview --input PATH|- [--output PATH]`
- `configuration apply --input PATH|- [--request-id ID]`
- `job get --id ID` observes a pending validation job. Repeating the original apply request observes its current outcome through the durable receipt.

Without `--output`, export/preview use the ordinary versioned JSON result envelope. With `--output`, they create a new owner-only file containing the directly consumable document and synchronize it and its directory; they never overwrite a file or follow an existing link. A failed/uncertain write preserves the selected path for inspection. Configuration stdin cannot share stdin with an authentication token.

## Storage

Imports with no new repositories publish all entries, metadata events, the completed coordinator job and its receipt in one transaction. Imports with new repositories first create only an `import-configuration` coordinator and ordinary read-only repository-inspection jobs. The server does not open Worker paths. Every selected checkout must report the exact canonical path shown in the preview; a differently resolved path requires a new explicit preview. Reuse of identical existing repository configuration does not claim a new live inspection.

All new repository inspections must succeed before any imported configuration is written. The same transaction checks current configuration, machine identity/enablement and the importing client's non-revoked authority and publishes the entire graph plus parent outcome. A failed inspection, Worker revocation/loss, stale settings, model collision or revoked importing client preserves existing configuration and marks the coordinator failed. Late reports cannot revive it. Unexpected storage failures roll back the child report and all tentative writes, preserving its exact retry authority. Existing accounts and sessions are never disconnected, resumed or dispatched by import.

Pending coordinators retain the non-secret reviewed plan needed to complete after restart. Terminal coordinators drop staged configuration and instruction copies, retaining only the version marker, metadata resource outcomes and sanitized failure. No schema migration is required; the existing durable job, receipt and metadata-event boundaries own the operation. Existing backup copies remain governed by the separate backup/deletion contract.

## Security

A bundle contains at most 256 configuration entries, 64 distinct referenced machines, 64 checkouts and 384 KiB of JSON. The machine count includes the complete union of checkout references, repository remediation overrides and server remediation defaults; repeated references count once. Export rejects an excessive count with the existing ResourceExhausted transfer error, without returning a partial document or changing source configuration. It never drops a policy or reference to fit the bound. Plans are limited to 768 KiB; complete RPC documents retain the existing 1 MiB domain limit. Relationship validation is bounded at 10,000 current configuration entries and 16 MiB, failing without partial results. Unknown schema versions, fields, enum values, credential-bearing extensions and injected server-owned observations are rejected. Portable templates are user-authored contents, not a mechanism for exporting a protected credential store.

Preview tokens grant only the exact reviewed import to the same currently authenticated actor. They are not credentials, device registration, account readiness, native capability or execution authority. Import must not invoke inference, provider discovery, login, Git fetch, checkout, filesystem repair or native installation.

## Desktop

Settings includes Import / Export, presented as a shared left-aligned 1040px maximum column with flat Export and Import sections and 720px form bounds under issue #1256, always-visible labeled file/JSON inputs and a noninteractive state-derived Load / Map / Review & apply indicator. Mapping, complete review, uncertain retry and result panels remain below the original inputs; the full scope/exclusion guidance and responsive presentation are owned by the desktop contract. Exports are selectable for copying; imports accept a bounded UTF-8 file or pasted JSON. The client displays source machines, requires explicit target selections and target checkout paths, and offers create/reuse choices plus explicit server-preference replacement. It displays the complete exact change document before the separate apply action. Editing the document or mappings invalidates the preview. Forms remain mounted across responsive changes, same-category reselection and same-identity reconnect within the active category, and pending requests retain their original wire bytes and request identity there. Category departure or leaving Settings discards the document, mappings, preview, client waits and retry presentation without implicit apply/replay or server job cancellation; fresh reads may observe already accepted server effects. An acknowledged malformed result remains blocked for inspection rather than permitting a replacement mutation. Job reads distinguish accepted/pending validation, completed application and unchanged-configuration failure. No Web Storage or persistent cache is used.

## Logging

Structured import logs contain the request UUID, coordinator job UUID, closed state and replay flag. Never log instruction/configuration documents, paths, provider responses or authentication material. Existing correlated sanitized RPC errors provide validation guidance.

## Build and Test

Run focused server and CLI tests with the race detector, complete DeliDev Go tests/vet, Buf lint and deterministic regeneration, and `pnpm test` in `apps/delidev`. Verify graph remapping, account isolation, exact instructions, all-repository atomicity, stale/revoked authorities, file no-overwrite behavior, signed-preview tampering, reference-only replay, and exact frontend bytes including integers outside JavaScript's safe range. Label real Worker/Git integration and actual native desktop interaction separately in pull requests, issues and CI logs/artifacts.

## Dependencies and Integrations

Uses existing configuration validation, SQLite transactions, durable Worker jobs, Connect authentication, signed cursor primitives and the private generated API client. No new listener or Tauri business binding is introduced.

## Change Triggers

Update this contract, project/catalog links and scoped AGENTS for new portable kinds, trust state, remapping rules, application semantics or bounds. Additional portable surfaces require explicit schemas and their own authentication/revision/activation semantics; never infer them from ordinary resource serialization.

## References

- [Project](project-delidev.md)
- [Repository defaults](repository-defaults.md)
- [Requirements](cmds-delidev-requirements.md)
- [Configuration and storage](cmds-delidev-contract.md)
- [Accounts](cmds-delidev-accounts-contract.md)
- [Desktop](apps-delidev-desktop-contract.md)

### Provider activation fields

Portable provider documents preserve optional `enabled` and `preset_id`. Missing activation uses the historical enabled default; update omission preserves stored false. Managed preset identity is validated against pinned server values. Six hosted presets exist by default on new and upgraded servers, so imports must explicitly reuse an equal target managed provider or reject its identity collision; collision checks occur both at preview and deferred apply. Imports never merge providers by name/URL or transfer account connection/readiness/key state. See [provider activation](cmds-delidev-provider-activation-contract.md).

## Service-native bundle version 2

Version 2 carries independent service-native accounts and models without Provider references. Import remaps their account/model UUIDs while preserving the closed service/harness identity; account reuse remains forbidden and every imported account is disconnected with no protected generation, native lease or quota observation. Export strips all managed subscription ownership extensions. Agents that still require legacy subscription reconfiguration reject export explicitly instead of exporting a dangling retired model or silently omitting the Agent.

API-only version-1 bundles remain importable and produce a version-2 reviewed plan. Any version-1 graph containing subscription accounts, native model source/service fields or native-subscription Providers rejects the entire preview before publication; no service is inferred. Version-2 relationship validation and model uniqueness compare the full source identity, retaining global CLI alias collision checks. Migration 28 owns legacy retirement independently of portable-format validation.
