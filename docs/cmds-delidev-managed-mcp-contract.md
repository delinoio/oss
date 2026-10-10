# DeliDev Worker-owned managed MCP

## Scope

`cmds/delidev-cli/internal/managedmcp` owns the selected Worker's managed MCP definitions, private operation receipts and explicit authentication. The server retains bounded metadata and Agent selection references. Desktop MCP Settings uses the typed management service. Host configuration, discovered MCP servers and always-provided execution tools are separate authorities.

## Runtime and Language

Go owns persistence, authentication and credential access. TypeScript/React owns disposable Settings presentation with generated Connect Query clients.

## Users and Operators

Users explicitly choose a Runner, configure a definition, authenticate it and select it on an Agent Worker. Operators inspect safe request identities and states without secret content.

## Interfaces and Contracts

`ManagedMCPService` provides list, revision-checked save, confirmed deletion, explicit manual/OAuth authentication and original-operation reads. System `MANAGED_MCP_V1=81` and Worker `MANAGED_MCP_V1=53` establish management authority only; `ENTITY_KIND_MANAGED_MCP=36` owns server metadata. Typed declarations and all member allocations remain in the protocol allocation catalog.

A definition uses a UUID-v7 identity and immutable revisions. Transports are stdio with an absolute executable/cwd and bounded arguments, or Streamable HTTP with HTTPS or explicit loopback HTTP. Credentials are opaque Worker-owned references. The server verifies the original requesting client, machine, authenticated Worker Device and current Worker instance before admission and publication. Device replacement cannot adopt another Worker's definitions. A pending admission prevents Agent selection or a competing mutation until the original operation settles.

Agent `managed_mcp` is additive: omission preserves existing selections; an explicit empty `selections` list clears them. Every selected reference binds machine, Worker Device and definition. Imported selections are unresolved until explicit rebind. Execution resolution copies the accepted original definition revision and credential reference into immutable execution configuration. Disabled, unresolved, unsupported, expired or foreign selections fail before native execution.

Management does not advertise a harness as supported. All native adapters currently retain their existing empty managed inventory acceptance boundary. A separate adapter must validate the actual native profile and publish verified per-harness support before managed selections can execute. Worker execution rejects managed definitions until that authority is implemented. Configuration acceptance is never native acceptance.

Deletion requires confirmation and no current Agent references. It tombstones the current Worker catalog entry while preserving immutable generations and original credential references. No host configuration or unrelated process is removed. Retired resources require independently proven execution cleanup; management deletion does not authorize cleanup.

## Storage

The Worker uses an owner-only, locked, bounded JSON journal under its existing private root, partitioned by server and Worker Device. Entries, immutable generations and request receipts survive reconnects. The server uses the existing resource store; no SQLite migration is introduced. Server placeholder metadata is not accepted until the original Worker confirms it.

Receipts keep a keyed digest of the original input and safe results, never raw secret values or callback codes. Original request replay can return an already-completed receipt but cannot repeat an uncertain vault write or token exchange. A read of the original receipt can prove that no native receipt was admitted; it never resends a mutation. Uncertain effects retain recovery state.

## Security

Manual environment/header values and OAuth verifier/tokens stay in the selected Worker's protected native credential vault, using the managed-MCP purpose and original definition/request identities. List responses and portable exports exclude all secret values and credential references. Read/save/list never launch a configured server, perform login or inference.

OAuth is an explicit public-client action. Before capture, bounded protected-resource and authorization-server metadata must match the configured resource, issuer, authorization/token endpoints and S256 support. PKCE, random callback state, exact redirect identity and original requesting client/definition revision fence completion. Both authorization and token requests bind the MCP resource. The no-resend boundary is persisted before the single code exchange. Cancellation is successful only after original verifier cleanup; failed cleanup or token response loss remains recovery. Safe token-expiry metadata prevents an expired credential from appearing authenticated. Authentication is independent from native execution acceptance.

## Logging

Structured operation logs include machine/definition/action/state only. Raw callback URLs, codes, authorization headers, environment values, verifier material and token responses must never be logged.

## Build and Test

Focused offline Go fixtures cover Worker isolation, immutable revisions, credential non-disclosure, conflict/deletion guards, PKCE/resource binding and original-scope single exchange. Protocol allocation validation and generated-client checks cover typed declarations. Broader Go/frontend suites, builds and native/account/platform acceptance are CI or separate acceptance work; fixtures do not establish native capability. Preserve exact validation revisions and unperformed checks in PR/CI records.

## Dependencies and Integrations

Follow the [credential](cmds-delidev-credentials-contract.md), [storage](cmds-delidev-storage-contract.md), [harness](cmds-delidev-harness-contract.md), [catalog](cmds-delidev-catalog-contract.md), [desktop](apps-delidev-desktop-contract.md) and [portable configuration](cmds-delidev-configuration-transfer-contract.md) contracts. Managed skill selection and always-provided execution MCP tools retain separate storage/runtime authorities.

## Change Triggers

Changes to authentication, native support, execution cleanup, portable formats or management fields update the affected contracts and typed allocation catalog together. Instruction files change only when procedure or ownership changes.

## References

- [DeliDev project](project-delidev.md)
- [Repository defaults](repository-defaults.md)
- [MCP authorization specification](https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization)
