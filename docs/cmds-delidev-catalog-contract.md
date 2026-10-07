# DeliDev provider and model catalog

## API account protocol reservations

Issue #964 reserves ProviderInventory capability `ACCOUNT_API_PROTOCOL_V1 = 7`,
`ProviderInventoryEntry.api_formats = 10` and the account-list-only
`ListResourcesRequest.api_protocol = 5`. `ApiProtocol` reserves UNSPECIFIED 0,
OPENAI_RESPONSES 1, OPENAI_CHAT 2 and ANTHROPIC_MESSAGES 3.
`ApiAuthentication` reserves UNSPECIFIED 0, BEARER 1, API_KEY 2 and KEYLESS 3.
`ProviderApiFormat` reserves protocol/endpoint/authentication fields 1–3.
The complete declaration closure must reach main before dependent implementation.
Reservations add no active schemas, bindings, capability advertisement, credential
use, inference authority or database migration.

The planned feature selects one explicit protocol when adding an API key. Custom
providers declare bounded per-protocol endpoint/authentication profiles. Existing
accounts retain their original defaults; connected accounts cannot change protocol.
Disconnect and confirmed protected cleanup precede a changed selection and a new
connection. Preserve immutable original execution/account ownership, keyless
cleanup proofs and native uncertainty. API-specific resource schema 3 and portable
configuration version 4 protect explicit selections from older-client writes while
retaining legacy reads. Follow the catalog, account and proxy contracts.

## Ordered Agent Worker account sources

PR #1371 established System capability 36 and SaveAgentWorkerRequest field 5 on
main before this extension. Capability 35 remains reserved for known subscription
models. Capability 36 advertises configuration and routing support, never native
execution authority. No SQLite migration is added.

Agent schema 3 uses an ordered nonempty `routes` list. Each route contains
`model_id`, ordered `accounts` with weights 1–1,000, and an optional `routing`
override. The single-source top-level model/accounts/routing and routes are exclusive.
One Worker retains one Harness, name, instructions and native options. Every
route uses one live API Provider or service-native subscription identity; sources
and accounts cannot repeat. Across all routes the existing 1,000-account bound
applies. Current single-source schema 1 and ordered-source schema 3 are supported.
Every source requires at least one account; retirement-only schema 2 is rejected.
Adding another source selects schema 3; removing sources keeps schema 3, including
when one remains. A single-source document cannot replace an existing schema-3
Worker. Historical execution snapshots remain immutable.

`SaveAgentWorker.route_models` aligns typed selections exactly with the route
order and is exclusive with `model`. Go derives every source from current account
records inside one receipt transaction. Canonical selections require exact model
revisions; direct native IDs reuse/create source-scoped canonical models. All
model changes, complete relationship checks and the Worker save commit together,
or roll back together. Replayed original requests retain their exact identities.

The first execution claim of a new session evaluates sources in configured order.
A source advances only when every configured candidate is classified as confirmed
quota exhaustion. Missing accounts, project restrictions, disabled accounts,
invalid authentication/generation/validation, unsupported model/harness/provider
protocol, and unavailable quota that cannot establish exhaustion are blockers.
A failed query or elapsed reset time never clears exhaustion. Only observed
recovery can clear the existing server-owned exhaustion flag; later new sessions
then prefer the earlier source again. Within the chosen group all six existing
policies and inherited server defaults remain available. New UI groups default
to Priority. Sequential, rotation and quota tie state are keyed by stable live
source identity; only the selected group's state changes atomically with the
initial snapshot and dispatch. Read-only preview uses the same selector.

The initial Route preserves every source's canonical model ID/revision/native ID,
policy, ordered weighted candidates, quota evidence and blockers, plus the selected
source index. ExecutionConfiguration and the native Worker assignment retain their
existing selected-source shape. Existing executions keep their original model,
account, connection and Usage/cost attribution. Relationship checks, project
selection, deletion protection and portable version 2 include all sources.
Schedules use the ordinary first-execution boundary.

Codex requires an explicitly configured Responses provider. The OpenRouter managed
Chat preset and existing connections are not rewritten. Use a compatible custom
provider/account configuration and retain execution-time validation. OpenRouter
[documents Responses](https://openrouter.ai/docs/api/api-reference/responses/create-responses).
No automatic harness switch, active-session retry/account switch or new subscription
execution profile is introduced.

## Known subscription model suggestions

`internal/knownmodels` owns the versioned, embedded `catalog.json`, the validated
private download cache and one joined server refresh loop. Its immutable fetch
URL is `https://raw.githubusercontent.com/delinoio/oss/main/cmds/delidev-cli/internal/knownmodels/catalog.json`.
Only reviewed main data reaches installed servers. A single app/server upgrade
adds the feature; later catalog changes need no release. No SQLite migration is
introduced. Cache publication uses the existing atomic private-file writer.

Schema 1 includes a semantic SHA-256 catalog version, reviewed-data date, source
URLs/revisions/digests, and exactly ChatGPT, Claude and Grok in that order. Each
service has 1–200 unique exact native IDs with display name, order and source keys.
Known minimum harness versions and definite retirement dates are retained. A
retirement date removes a new suggestion on that UTC date, including offline use.
Unknown fields/schema, duplicate keys/IDs, invalid dates/digests, empty or oversized
inventories and incomplete provenance cannot replace the last valid catalog.

`ProviderService.ListKnownSubscriptionModels(subscription_service)` requires
owner or paired-client role plus current store authorization. It returns at most
200 candidates, catalog version/date and BUNDLED, CACHE or ONLINE source. Worker
credentials are denied. The read neither downloads data nor reads account secrets,
starts native tools or creates saved models. Initial reads use the embedded or
valid private cached catalog. Successful server downloads refresh after 24 hours;
failures retain the last valid catalog and retry after one hour. Restart retains
the last successful download deadline when the cached reviewed date is newer than
the bundled date or when both the date and semantic catalog version exactly
match. An equal-date version mismatch is ambiguous, so the server uses the
bundled catalog and refreshes immediately. Each request has a 15-second deadline
and 1 MiB body limit. Redirects, ambient proxies and route fallback are
forbidden; server shutdown cancels and joins the request and maintenance owner.

Known metadata is separate from native discovery, credential/account entitlement,
readiness and canonical saved-model authority. Known selections use the existing
atomic Worker native-ID save. Saved selections retain exact model ID/revision
checks. Removal from recommendations never removes saved models, configurations
or immutable historical execution attribution. API endpoint discovery is unchanged.

The collector uses the revision-pinned official Codex `models-manager/models.json`
(public visibility and nonempty subscription plans), official Codex retirement
notices, Claude Code family/selection instructions plus the current official
model table, and the Grok Build recommended coding default. API-only inventory,
hidden models and arbitrary configuration examples are excluded. A failed or
empty extraction is fatal. Source-only/date-only reads preserve reviewed bytes;
changes to IDs, display names, order or model metadata produce a review PR under
[the workflow contract](repository-workflow-contract.md#delidev-known-model-catalog).
If a reviewer closes that PR without merging, the publisher records the closed
candidate version and does not reopen it until a genuinely changed catalog is
collected.


## Scope

Native Codex observations and explicit registration are owned
by [the native model contract](cmds-delidev-native-models-contract.md). Its separate
Worker job and immutable pages do not alter the HTTP catalog, account readiness or
manual entries. Registration uses the existing model-save boundary with the selected
account provider and observed executable model ID, retaining manual provenance.

The server owns provider presets, API model discovery, catalog publication and canonical model selection in `cmds/delidev-cli/internal/providers`, `internal/server`, `internal/store` and the corresponding CLI commands. The complete [issue #964 requirements](cmds-delidev-requirements.md) remain normative. This contract implements the API catalog boundary; native harness observations, selected-model execution capability and subscription authentication retain their separate authority contracts. API proxy execution and first-dispatch snapshots follow their own implemented authority contracts; a catalog cannot authorize them.

## Runtime and Language

Go with authenticated Connect RPC and server-owned local SQLite. Model lists come from the bounded non-inference [provider inspector](cmds-delidev-providers-contract.md). No harness process, inference request or billing/administrator credential is used.

## Users and Operators

The owner and paired clients manage provider/model configuration. Execution Worker credentials cannot invoke catalog RPCs. The server's joined background maintenance task refreshes enabled API accounts whose providers permit discovery. A model endpoint's localhost always means the server machine; no Worker-localhost tunnel is implied.

## Interfaces and Contracts

`ProviderService` exposes these operations:

| RPC | CLI | Result |
| --- | --- | --- |
| `ListProviderInventory` | `provider list [--query TEXT] [--enabled-only] [--limit N] [--page-token TOKEN]` | Bounded active/custom provider inventory, exact account counts and compatibility capabilities |
| `ListProviderPresets` | `provider presets` | Closed, editable provider defaults with key guidance, documentation and compatibility limits |
| Existing `SaveConfiguration`, using a selected preset | `provider create --preset PRESET [--name NAME]` | Ordinary saved provider configuration and a durable request receipt |
| `DiscoverModels` | `provider discover --account-id ID --revision N` | Accepted discovery observation plus current account metadata |
| `SearchModels` | `model search [--query TEXT] [--provider-id ID] [--include-hidden] [--limit N] [--page-token TOKEN]` | Model records, represented provider records and an opaque continuation |
| `ResolveModel` | `model resolve --selector UUID_OR_ALIAS_OR_NATIVE_ID [--provider-id ID]` | One canonical model record or a typed missing/ambiguity error |

Mutations accept the normal `--request-id` option. Creating an unsaved preset uses its concrete provider document as the saved request input; it does not create an account or imply authentication. The 32 hosted presets are saved On by default, so `provider create --preset ID` for an existing hosted preset returns a duplicate conflict; use inventory and edit its saved resource to change availability. `provider create --input FILE|-` remains the custom-provider path. Presets are stable enum-style identifiers:

Provider inventory's closed capability enum separately advertises provider activation, active-provider model filtering, account-provider filtering and `ACCOUNT_TYPE_FILTER`. The first three gate provider/model controls; separate API/subscription account lists and the guided API account flow require all four. Inventory filters are cursor-bound and server-owned; callers must not replace unavailable inventory with generic resource pages.

| Preset | API base | Default protocol | Authentication |
| --- | --- | --- | --- |
| `openai` | `https://api.openai.com/v1` | OpenAI Responses | Bearer |
| `anthropic` | `https://api.anthropic.com/v1` | Anthropic Messages | API key |
| `openrouter` | `https://openrouter.ai/api/v1` | OpenAI Chat Completions | Bearer |
| `vercel-ai-gateway` | `https://ai-gateway.vercel.sh/v1` | OpenAI Chat Completions | Bearer |
| `xai` | `https://api.x.ai/v1` | OpenAI Responses | Bearer |
| `deepseek` | `https://api.deepseek.com/v1` | OpenAI Chat Completions | Bearer |
| `gemini` | `https://generativelanguage.googleapis.com/v1beta/openai` | OpenAI Chat Completions | Bearer |
| `groq` | `https://api.groq.com/openai/v1` | OpenAI Chat Completions | Bearer |
| `mistral` | `https://api.mistral.ai/v1` | OpenAI Chat Completions | Bearer |
| `together-ai` | `https://api.together.ai/v1` | OpenAI Chat Completions | Bearer |
| `fireworks-ai` | `https://api.fireworks.ai/inference/v1` | OpenAI Chat Completions | Bearer |
| `perplexity` | `https://api.perplexity.ai/router/v1` | OpenAI Chat Completions | Bearer |
| `cohere` | `https://api.cohere.ai/compatibility/v1` | OpenAI Chat Completions | Bearer |
| `cerebras` | `https://api.cerebras.ai/v1` | OpenAI Chat Completions | Bearer |
| `nebius` | `https://api.tokenfactory.nebius.com/v1` | OpenAI Chat Completions | Bearer |
| `novita` | `https://api.novita.ai/openai/v1` | OpenAI Chat Completions | Bearer |
| `deepinfra` | `https://api.deepinfra.com/v1/openai` | OpenAI Chat Completions | Bearer |
| `hugging-face` | `https://router.huggingface.co/v1` | OpenAI Chat Completions | Bearer |
| `venice` | `https://api.venice.ai/api/v1` | OpenAI Chat Completions | Bearer |
| `scaleway` | `https://api.scaleway.ai/v1` | OpenAI Chat Completions | Bearer |
| `baseten` | `https://inference.baseten.co/v1` | OpenAI Chat Completions | Bearer |
| `moonshot` | `https://api.moonshot.ai/v1` | OpenAI Chat Completions | Bearer |
| `moonshot-cn` | `https://api.moonshot.cn/v1` | OpenAI Chat Completions | Bearer |
| `minimax` | `https://api.minimax.io/v1` | OpenAI Chat Completions | Bearer |
| `minimax-cn` | `https://api.minimax.cn/v1` | OpenAI Chat Completions | Bearer |
| `siliconflow` | `https://api.siliconflow.com/v1` | OpenAI Chat Completions | Bearer |
| `siliconflow-cn` | `https://api.siliconflow.cn/v1` | OpenAI Chat Completions | Bearer |
| `qianfan` | `https://qianfan.baidubce.com/v2` | OpenAI Chat Completions | Bearer |
| `tencent-tokenhub` | `https://tokenhub.tencentmaas.com/v1` | OpenAI Chat Completions | Bearer |
| `tencent-tokenhub-international` | `https://tokenhub-intl.tencentmaas.com/v1` | OpenAI Chat Completions | Bearer |
| `alibaba-model-studio-international` | `https://dashscope-intl.aliyuncs.com/compatible-mode/v1` | OpenAI Chat Completions | Bearer |
| `alibaba-model-studio-hong-kong` | `https://cn-hongkong.dashscope.aliyuncs.com/compatible-mode/v1` | OpenAI Chat Completions | Bearer |
| `ollama` | `http://127.0.0.1:11434/v1` | OpenAI Chat Completions | Explicit keyless |
| `lm-studio` | `http://127.0.0.1:1234/v1` | OpenAI Chat Completions | Explicit keyless |
| `vllm` | `http://127.0.0.1:8000/v1` | OpenAI Chat Completions | Explicit keyless |

Every preset enables discovery initially. Local presets require the user to run/configure that local API server; if it requires a key, change authentication before connecting. DeliDev neither installs a model server/model nor treats the preset as proof of a chosen model/harness/protocol combination. Empty discovered `harnesses` explicitly means compatibility has not been configured; execution checks cannot treat it as universal support.

### Discovery publication

Explicit discovery requires a current connected API account revision, no pending credential removal and `provider.discovery=true`. It shares the inspector's one-operation-per-account and eight-inspections-per-server bounds with account validation. Authorization, connection generation, account revision and provider authority are checked before inspection and at publication. HTTP runs outside account locks and SQLite transactions. Accepted failed observations are durable results; the CLI retains the result when returning its typed nonzero exit.

One SQLite transaction commits all added/updated models, the account's `catalog` observation, revisions, events and request receipt. The observation includes request/connection IDs, completion time, state, received/added/updated counts, last successful refresh, bounded retry delay and sanitized problem. Discovery does not change account health, validation, quota, exhaustion or existing session configuration. A failed refresh preserves the last successful timestamp and all previous models, including manual registrations. Neither successful absence nor failed listing deletes a model.

New canonical `(provider_id, native_id)` pairs receive a UUID-v7 identity, upstream display name, `new=true`, empty harness compatibility and server-owned first-seen/metadata-update timestamps. Existing automatic records retain UUID, native identity, display name, alias, hidden flag, custom order, configured harnesses and acknowledged NEW state. Only changed advisory context/modalities/tools/reasoning data produce a model revision/event; an unchanged refresh does not invalidate model search pages.

Manual registration through `model create --input` assigns `manual=true`; clients cannot forge discovery timestamps or NEW. Legacy models without discovery provenance are treated as manual during refresh. Manual records and models with `metadata_source=user-declared` retain their advisory data. Provider observations may mark available advisory metadata `known`; absent fields remain `unknown`, with optional booleans distinguishing an explicit false from no evidence. Manual input cannot claim `known`, and edits to observed metadata must use `user-declared`. These labels do not prove execution availability or permissions. NEW can be acknowledged by an ordinary revision-checked model edit setting `new=false`; an edit cannot restore a cleared marker.

Explicit model deletion atomically records a provider/native-ID suppression alongside the existing UUID tombstone. Later automatic or explicit discovery skips that native ID, including after restart. Deliberate manual recreation remains possible with a new UUID. Deleting the provider removes its suppressions. Discovery receipts touching a subsequently deleted model are redacted by the ordinary deletion boundary and return `not_found`; retry cannot recreate deleted state. Other accepted receipts return the original observation with current account metadata without key access or HTTP, even after restart, disconnection or provider discovery disablement.

### Automatic refresh and cancellation

Enabled, connected API accounts with discovery enabled are scanned in bounded UUID pages. An account with no observation for its current connection is due immediately. Later observations become due after the greater of 15 minutes and a valid provider Retry-After delay, measured from completion. This is a new periodic observation, not a replay or immediate retry inside an inspection. Failed observations retain the same schedule. A four-worker pool prevents one slow endpoint from blocking all progress; an unaccepted revision/inspection conflict has a 30-second in-memory cooldown. A two-second ticker and state-change/completion notifications drive the scanner, with a 100-millisecond minimum scan spacing during high event traffic. No unbounded catch-up queue is created.

Disabling provider discovery cancels its active discovery inspections. Disconnection cancels all checks for that account; paired-client revocation cancels that client's outstanding requests. Server shutdown cancels and joins automatic work before releasing the vault, database or private scope. Canceled/stale work cannot publish. Re-enabling discovery follows the persisted due time; an explicit discovery can refresh immediately. Explicit `account validate` remains available with discovery disabled and never publishes catalog models.

### Search, groups and canonical selection

Search is a local SQL substring query over display name, native ID and CLI alias, with SQLite's built-in case folding. The query is bounded to 256 bytes, pages to 1–200 (default 50), and filters include optional provider and hidden models. Results sort by provider UUID, custom order, case-folded display name, then model UUID. Each response includes only the provider records represented in its page so clients can render provider groups. Hidden state and order are display preferences; hidden models remain explicitly resolvable and do not lose execution permissions.

Keyset cursors are HMAC-bound to server identity and filters, expire under the ordinary 24-hour cursor limit, and carry the latest model/provider event sequence from the same read snapshot. A model/provider change expires an old page rather than skipping or repeating rows from a changed order. Unrelated account/stream events do not expire it. Page size can change on continuation. Exact full pages may return a continuation whose next page is empty.

Resolution uses an exact canonical UUID first; an upstream native ID cannot shadow that UUID. Otherwise it requires one exact alias/native-ID match within the optional provider scope. It never picks the first ambiguous result. Manual writes reject duplicate canonical pairs and alias collisions with other aliases/native IDs; aliases cannot contain whitespace, separators or a canonical UUID. If later upstream discovery introduces a native ID matching an existing alias, both canonical records remain available and the ambiguous shorthand fails; use a UUID or provider scope. First-dispatch work must resolve and retain canonical provider/model IDs in immutable snapshots and history instead of retaining a display alias as execution identity.

Provider-row account links use the list-only `provider_id` selector on `ResourceService.ListResources`. Account-type filtering composes with that selector in SQL before the account page limit; the returned account page must never be treated as the provider's total account count. See the account lifecycle and protocol contracts for cursor scope and snapshot/event behavior.

## Storage

Schema v3 adds unique canonical-model and alias indexes, native-ID/display-order indexes, an indexed model/provider event epoch and `model_suppressions`. Migration from v1/v2 first synchronizes a private consistent backup and then applies schema changes transactionally. Conflicting legacy identities fail with the original version/data intact; migration never renames or merges them silently. Search, resolution and due-account scans are bounded indexed reads. A discovery publication is bounded to 10,000 retained models for one provider; exceeding the bound produces a visible failed observation without partial additions. SQLite/local search is the documented project override of cloud search/storage defaults.

## Security

Only the server retrieves the exact connection's protected key. The inspector applies the saved endpoint/authentication, fixed request paths and headers, direct verified transport, response bounds and redaction. No key, diagnostic body or arbitrary pagination URL enters model records. Native protected-store read failures become accepted sanitized failed observations; disconnect/revocation/cancellation still prevents stale publication. General configuration cannot alter account catalog evidence or claim server-owned model provenance. Preset guidance never requests a provider-wide usage, billing or administrator key.

## Logging

Structured inspection logs include operation/account/request/correlation IDs, phase, duration, authentication class, model count, bounded status and stable failure code. Committed catalog logs include observation state and received/added/updated counts. Maintenance scan/publication failures use typed codes. Logs exclude keys, endpoint URLs, model content and upstream response bodies.

## Build and Test

Run delidev package race tests and vet, Buf formatting/lint and exact generated-binding reproduction. Real SQLite tests cover v2 migration/backup rollback and persisted due-time/retry bounds. Real Connect/HTTP tests cover manual/display preservation, metadata provenance, NEW acknowledgement, atomic successful/failed results, receipt replay/restart, explicit deletion suppression, search ordering/cursor scope, hidden/ambiguous resolution, Worker denial, discovery disablement and automatic shutdown cancellation. The explicitly isolated Linux Secret Service CLI fixture additionally tests native key retrieval, automatic/explicit discovery, canonical resolution, failure output and restart replay. None of these fixtures establish real provider-account or native harness execution acceptance.

## Dependencies and Integrations

Connect Go/protobuf, SQLite, the protected account lifecycle, provider inspector and ordinary resource/configuration services. Official interface references for presets and catalog parsing are retained in the provider inspection contract; preset records expose their provider's documentation link.

## Change Triggers

Update this contract, provider/account and protocol contracts, the project index, validation records in pull requests, issues and CI logs/artifacts and applicable AGENTS whenever preset identities, observation ownership, discovery scheduling, model identity/provenance or search semantics change.

## References

- [Project](project-delidev.md)
- [Repository defaults](repository-defaults.md)
- [Provider inspection](cmds-delidev-providers-contract.md)
- [Account lifecycle](cmds-delidev-accounts-contract.md)
- [Connect protocol](protos-delidev-v1-contract.md)

### Provider activation and filtering

Follow [API provider activation](cmds-delidev-provider-activation-contract.md) for stable preset identity, explicit activation state, bounded ProviderService inventory, exact account counts, provider-scoped account pages and the additive `SearchModels.enabled_providers_only` filter. The active-provider condition is applied before SQL pagination and included in signed cursor scope. Disabled providers retain canonical model records and remain resolvable for historical references; catalog visibility never grants execution.

## Agent Worker model selection

System capability 33 and ConfigurationService.SaveAgentWorker follow the main
reservations established in PR #1351. Source-scoped account and model list
selectors are applied in SQL before LIMIT and bound into signed cursors. Unknown
services, mixed API/service filters and non-account account selectors fail.
Coherent snapshots/events and current optional filter semantics remain unchanged.

Go derives one common source from at least one current selected account inside
the receipt transaction. Native subscription services require their matching
harness. Fixed routing requires one account; ordered weights remain unchanged.
The typed selection either names a canonical model with its exact revision or
an exact native ID within that source. Native IDs never resolve through CLI
aliases or other sources. Reuse the canonical model when present and preserve
its identity, display/advisory settings and discovery/manual/NEW provenance.
Final save may add explicitly configured harness compatibility without claiming
native or account acceptance. New internal models use manual provenance and
unknown advisory metadata. Failed Worker validation/revision/template writes
roll back every model write and receipt. Concurrent saves share the existing
SQLite transaction and model-identity checks; exact replay cannot recreate a
deleted Worker. CLI agent create/edit/save and desktop writes use SaveAgentWorker. Canonical
model selection requires the model revision; exact native IDs use the same atomic
path. Generic SaveConfiguration rejects Agents. No snapshot rewrite is introduced.

## Inline Worker models and endpoint-only completion reservation

The owner-approved replacement under issue #964 removes the independent saved
Model registry, model-input history and persistent API catalogs. API completion
uses only the selected Provider model endpoint. Exact direct IDs remain local to
the selected Agent Worker configuration. Subscription completion retains the
known official advisory catalog, without merging saved records. Token pricing
uses the exact `(API Provider UUID or SubscriptionService, native ID)` identity.

Reserve System `INLINE_WORKER_MODELS_V1 = 42`, Worker
`INLINE_MODEL_EXECUTION_V1 = 22`, `ModelIdentity`, `EndpointModel`,
`ListEndpointModels` request/response, source/native-ID token-pricing declarations
and the additive usage/pricing model-identity fields in the allocation ledger on
main before implementation. Reservations advertise no runtime capability.

Compose the complete feature with the already main-reserved current-only DB
baseline 32 and protocol 2 reset. Initialize a complete Model-free current
layout with no conversion of earlier DBs or history. The 2026-10-07 owner
amendment waives earlier DB retention and permits an explicit DB/sidecar reset.
Earlier backups remain unsupported; this waiver grants no native cleanup or
protected-credential deletion authority.
Worker schema 4 embeds each route's exact native model and required non-secret
execution metadata, with its source derived and checked against all selected
Accounts atomically. There is no separate Model UUID/revision or registry write.
Current portable bundle 4 embeds those values and has no Model entries.
The earlier reset proposal's portable bundle 2 and atomic Model/Agent saving are
superseded by this complete feature. Other reset and native-lifetime boundaries
remain required. Existing wire allocations must retain their meanings when
obsolete fields and APIs are retired.

`ListEndpointModels` is owner/client-only and read-only. Its request pins Account
and Provider revisions; Go derives the Provider, reads the original protected
connection credential and rechecks actor/source/connection authority before
returning. Reuse fixed model-list profiles, pagination, redaction, explicit
outbound routing and existing 20-second/32-page/10,000-model/response limits.
Autocomplete makes no separate credential-validation request and publishes no
Models, Account changes, catalog observations, events or receipts. No fallback,
partial response, persistent catalog cache or background catalog refresh is
allowed. Disconnect, revocation and shutdown cancel and join original requests.
Logs retain only bounded identities, counts, phase, duration and stable failures.

The editor requests a fresh list on entry/explicit refresh through the first
connected selected Account, filters the current response and clears it on source,
Account, connection-generation or editor-lifetime change. Failure clears choices
and permits exact direct input. The approved existing wizard layout retains
keyboard/focus/theme/responsive behavior, with one Refresh model list button and
no Saved badge. Native model observations remain independently read-only; their
register/use actions are removed. Pricing configuration and immutable historical
prices/estimates/budgets remain independent of autocomplete and execution grants.
New prices apply only to future observations. Current external adapters, native
account ownership, same-account history and actual execution checks remain intact.

Reservations and policy do not establish implementation or native/account
acceptance. Record validation in PRs and CI, never repository evidence documents.
