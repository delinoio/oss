# DeliDev Worker-owned managed MCP

## Scope

`cmds/delidev-cli/internal/managedmcp` owns the selected Worker's managed MCP definitions, private operation receipts, explicit authentication and read-only execution-time resolution of frozen generations. The server retains bounded metadata and Agent selection references. Desktop MCP Settings uses the typed management service. Host configuration, discovered MCP servers and always-provided execution tools are separate authorities.

## Runtime and Language

Go owns persistence, authentication and credential access. TypeScript/React owns disposable Settings presentation with generated Connect Query clients.

## Users and Operators

Users explicitly choose a Runner, configure a definition, authenticate it and select it on an Agent Worker. Operators inspect safe request identities and states without secret content.

## Interfaces and Contracts

`ManagedMCPService` provides list, revision-checked save, confirmed deletion, explicit manual/OAuth authentication and original-operation reads. System `MANAGED_MCP_V1=97` and Worker `MANAGED_MCP_V1=59` establish management authority only; `ENTITY_KIND_MANAGED_MCP=36` owns server metadata. Typed declarations and all member allocations remain in the protocol allocation catalog.

The Worker never advertises `MANAGED_MCP_V1=59` on its initial attach. It appends the capability only after the original server's attach response lists it in the supported Worker capabilities, so an older server that rejects unknown enum values can still return its list; the capability is part of the reconnect support profile.

A definition uses a UUID-v7 identity and immutable revisions. Transports are stdio with an absolute executable/cwd and bounded arguments, or Streamable HTTP with HTTPS or explicit loopback HTTP. Credentials are opaque Worker-owned references. The server verifies the original requesting client, machine, authenticated Worker Device and current Worker instance before admission and publication. Device replacement cannot adopt another Worker's definitions. A pending admission prevents Agent selection or a competing mutation until the original operation settles.

Agent `managed_mcp` is additive: omission preserves existing selections; an explicit empty `selections` list clears them. Every selected reference binds machine, Worker Device and definition. Imported selections are unresolved until explicit rebind. Execution resolution copies the accepted original definition revision and credential reference into immutable execution configuration. Disabled, unresolved, unsupported, expired or foreign selections fail before native execution.

Management alone never advertises a harness as supported. Native support is a fact published by the original authenticated Worker through System `CODEX_MANAGED_MCP_V1=103` / Worker `CODEX_MANAGED_MCP_V1=64`. The server derives a definition's `supported_harnesses` from that capability on the definition's own Runner; neither a saved definition, a client request nor a Worker reply can supply it. Only the Codex adapter publishes it. Claude Code, OpenCode and Grok keep the empty managed inventory boundary and their executions reject managed definitions before launch. Configuration acceptance is never native acceptance.

Deletion requires confirmation and no current Agent references. It tombstones the current Worker catalog entry while preserving immutable generations and original credential references. No host configuration or unrelated process is removed. Retired resources require independently proven execution cleanup; management deletion does not authorize cleanup.

## Codex runtime

An execution freezes the accepted definition generations (identity, immutable revision, credential reference) in its immutable configuration. The Worker resolves them from its own immutable generation catalog and vault (`Manager.Resolve`): a generation that is not byte-identical to the Worker's retained one, a foreign machine/Worker, an expired credential or a missing vault entry fails before launch. Resolution never launches a server, logs in, refreshes a token or follows a newer revision, and a definition without protected authentication never unlocks the vault.

The Codex adapter loads exactly the selected generations as native `mcp_servers` launch overrides keyed by a stable private name (`NativeServerName`: a slug of the display name plus a digest of the definition identity, independent of the revision). Stdio servers use the absolute executable, arguments and cwd. Streamable HTTP servers use the endpoint. Protected values never enter arguments, logs, journals or published observations: manual environment values travel in the owned process environment and are named by reference (`env_vars`); manual headers and OAuth access tokens use generated `DELIDEV_MCP_<n>_*` variables referenced by `env_http_headers` and `bearer_token_env_var`. Those names are excluded from native shell-tool environments and registered as protected diagnostic values. Reserved or host-present names, conflicting values for one inherited name and `Authorization` header collisions fail closed. OAuth uses the Worker vault's access token for the execution; the adapter never starts native OAuth, refresh or a callback.

Before every input the adapter proves the exact inventory: `config/read` must contain exactly the selected servers with the launched transport fields and no layered override (`env`, `http_headers`, `oauth`, tool policy, plugins), and `mcpServerStatus/list` scoped to the original thread and each selected server (a scoped listing reuses the thread connection; an unscoped one would start duplicate servers) must report every selected generation `connected` with the expected HTTP origin and no plugin owner. Unselected user, project and plugin servers, a disabled server, a failed, cancelled, authentication-required or stuck startup all block the input. Typed per-generation outcomes (`ready`, `failed`, `authentication-required`, `cancelled`, `pending`) carry only definition identity, revision and the private native name, never native diagnostics. A failed or cancelled server gets one bounded reconnect of the same frozen definition before the first input (`config/mcpServer/reload` restarts only failed servers); authentication-required servers need the owner. Reconnect is available only for a definition selected at execution start, only while no turn or interaction is active, and never resends a call, input or elicitation.

Native tool calls become passive `mcp` tool observations bound to the original definition/revision/native server and tool. Arguments are never published; results are bounded text plus a content-block count. A foreign or built-in server call stays private. A call that is still unfinished when its turn ends is recorded as uncertain and no operation replays it. Tool approvals and other MCP elicitations arrive as native `mcpServer/elicitation/request` forms: supported form schemas (string, number, integer, boolean, single and multiple enum) become one once-only user question whose first answer is `accept`, `decline` or `cancel`; decline and cancel send no content, so a denied write never reaches the server. URL/unknown modes, missing turn context and unpresentable schemas are cancelled once without owner consent and published as a refusal. Native resolution of the transmitted response is the acceptance evidence `native-mcp-elicitation-resolved`. Foreign-server requests keep their private boundary.

Read-only Sidechat, native review, model observation and probe profiles refuse managed servers (`mcp_servers={}` remains enforced and verified); `SidechatSnapshot` drops the parent's selections. Independent Forks keep their own execution configuration and process. The owned process tree (including stdio children) is joined by the existing process-owner cleanup without touching unrelated processes.

## Storage

The Worker uses an owner-only, locked, bounded JSON journal under its existing private root, partitioned by server and Worker Device. Entries, immutable generations and request receipts survive reconnects. The server uses the existing resource store; no SQLite migration is introduced. Server placeholder metadata is not accepted until the original Worker confirms it.

Receipts keep a keyed digest of the original input and safe results, never raw secret values or callback codes. The digest key for a request that carries secret input (manual credentials or an OAuth callback) is generated once and held only in the selected Worker's protected native credential vault; the catalog stores just its non-secret reference, so the private catalog cannot be used to test guesses of secret values. Digests of requests without secret input use a separate catalog-local key. Metadata-only operations still never open the vault. Original request replay can return an already-completed receipt but cannot repeat an uncertain vault write or token exchange. A read of the original receipt can prove that no native receipt was admitted; it never resends a mutation. Uncertain effects retain recovery state. Catalog reads expose only original requesting-client/Worker pending admissions and awaiting OAuth metadata, bounded to 256 operations. This permits explicit recovery after category disposal without retaining secret form inputs, restoring an old catalog generation or replaying an effect. Expired authorization still requires explicit original-attempt cancellation before another authorization starts.

## Security

Manual environment/header values and OAuth verifier/tokens stay in the selected Worker's protected native credential vault, using the managed-MCP purpose and original definition/request identities. List responses and portable exports exclude all secret values and credential references. Read/save/list never launch a configured server, perform login or inference.

OAuth is an explicit public-client action. Before capture, bounded protected-resource and authorization-server metadata must match the configured resource, issuer, authorization/token endpoints and S256 support. PKCE, random callback state, exact redirect identity and original requesting client/definition revision fence completion. Both authorization and token requests bind the MCP resource. The no-resend boundary is persisted before the single code exchange. Cancellation is successful only after original verifier cleanup; failed cleanup or token response loss remains recovery. Safe token-expiry metadata prevents an expired credential from appearing authenticated. Metadata discovery and the token exchange run on the selected Worker and use its selected immutable outbound network route; they never consult ambient proxy settings or fall back to a direct or other route. Authentication is independent from native execution acceptance.

## Logging

Structured operation logs include machine/definition/action/state only. Raw callback URLs, codes, authorization headers, environment values, verifier material and token responses must never be logged.

## Build and Test

Focused offline Go fixtures cover Worker isolation, immutable revisions, credential non-disclosure, conflict/deletion guards, PKCE/resource binding and original-scope single exchange. Runtime fixtures cover launch syntax, secret placement, exact inventory drift, startup/reconnect, tool-call identity, uncertainty, elicitation response mapping and Worker/server publication. Skipped-by-default `TestManualNativeManagedMCP*` tests (`DELIDEV_NATIVE_THREAD_EXECUTABLE`) exercise an installed Codex against private local stdio and Streamable HTTP servers and a scripted model; they establish fixture behavior, not installed-profile account behavior. Protocol allocation validation and generated-client checks cover typed declarations. Broader Go/frontend suites, builds and native/account/platform acceptance are CI or separate acceptance work; fixtures do not establish native capability. Preserve exact validation revisions and unperformed checks in PR/CI records.

## Dependencies and Integrations

Follow the [credential](cmds-delidev-credentials-contract.md), [storage](cmds-delidev-storage-contract.md), [harness](cmds-delidev-harness-contract.md), [catalog](cmds-delidev-catalog-contract.md), [desktop](apps-delidev-desktop-contract.md) and [portable configuration](cmds-delidev-configuration-transfer-contract.md) contracts. Managed skill selection and always-provided execution MCP tools retain separate storage/runtime authorities.

## Change Triggers

Changes to authentication, native support, execution cleanup, portable formats or management fields update the affected contracts and typed allocation catalog together. Instruction files change only when procedure or ownership changes.

## References

- [DeliDev project](project-delidev.md)
- [Repository defaults](repository-defaults.md)
- [MCP authorization specification](https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization)
