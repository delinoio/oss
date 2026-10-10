# DeliDev provider and model catalog

## Key-preserving API format change reservations

The originating change established the complete reservations on main before activation.
The feature owns ProviderInventory capability
`ACCOUNT_API_FORMAT_CHANGE_V1 = 9` separately from capability 7 and OAuth
reservation 8. The owner/client `AccountService.ChangeAccountApiFormat` RPC
uses request fields mutation 1, api_protocol 2, alias 3, enabled 4,
exclude_automatic 5 and recovery_notifications 6; its response uses account 1,
request_id 2 and replayed 3. Reuse the existing `ApiProtocol` enum.
Capability 9 now activates this dedicated operation. The reservation-only PR
granted no format change, key access or execution authority.

The approved extension permits changing a connected API account's format while
keeping its protected key. Go owns atomic preference/profile publication and
new connection generations sharing the original protected credential reference.
Running executions and existing-session continuation retain their original
connection/profile and admission evidence. New sessions use the new format only
after explicit validation; saving sends no provider request. Preserve original
OAuth receipts, account/Worker/history attribution, immutable referenced profiles,
exact actor/revision/request retries, and explicit Disconnect/deletion cleanup
across all generations. Do not convert formats or change Provider identity or
keyless credential ownership. No SQLite migration or native change is authorized.
Capability 7 and its existing cleanup-required configuration operation remain
unchanged; capability 9 owns the dedicated extension.


## OAuth format selection extension

The [OAuth format reservations](cmds-delidev-account-oauth-contract.md#oauth-api-format-selection-reservations)
own the recorded capability 8 and Start/attempt format fields established
by reservation the originating change. Preserve manual
format profiles, original defaults and independent OAuth eligibility. The common
manual/OAuth connection UI requires selection for multiple profiles and displays
a sole profile read-only; server-owned Start pins explicit OAuth selections through
completion/recovery. Provider registration remains independently gated. No
database migration is added.

## API account format selection

The reservation change reached main before this implementation. The feature owns
ProviderInventory `ACCOUNT_API_PROTOCOL_V1 = 7`, inventory `api_formats = 10`,
account-list `api_protocol = 5`, and the complete closed `ApiProtocol`,
`ApiAuthentication` and `ProviderApiFormat` declarations. The protocol values are
UNSPECIFIED 0, OPENAI_RESPONSES 1, OPENAI_CHAT 2 and ANTHROPIC_MESSAGES 3;
authentication values are UNSPECIFIED 0, BEARER 1, API_KEY 2 and KEYLESS 3.
Profiles contain protocol, endpoint and authentication at fields 1–3. No SQLite
migration or Worker assignment extension is added.

API Provider documents retain the original flat protocol/endpoint/authentication
for legacy accounts and additionally declare `api_formats`, with one to three
unique API protocols and independently validated URLs/authentication. Current
managed preset resources project the registry profiles without rewriting stored
legacy documents. The original preset tuple and identity must match before this
projection; an arbitrary preset ID never grants official inspection evidence.
Explicit API Account documents save `api_protocol`. These API families use
resource schema 3, independently of ordered-source Agent schema 3. A legacy
account without `api_protocol` continues to use its original flat provider tuple.
An older-client write cannot clear an explicit account selection or a provider's
profiles. Legacy preset reads retain their original editable flat shape.

The server's common account profile resolver owns connection, validation,
discovery, routing, atomic Worker configuration, execution admission, title
creation and proxy authorization. Account format filtering applies before list
pagination, including provider-scoped lists and legacy tuple resolution, and is
bound to the cursor. Unknown protocol filters and subscription combinations are
rejected. The CLI negotiates capability 7 before `account list --api-protocol`.
Model identity and Usage attribution remain provider/model scoped; a format does
not create another model identity or duplicate history.

Connect fixes the selected tuple in the original connection generation. Capability
9 permits the dedicated key-preserving change described above. Capability 7 alone
retains the original configuration operation: explicit Disconnect and confirmed
protected cleanup precede a format edit, key re-entry and validation.
Accounts cannot change between keyless and credential-owning profiles; create a
new account for that transition. Existing keyless cleanup proofs remain valid.
Every referenced provider tuple is immutable, even for disconnected accounts and
legacy defaults. Unreferenced profiles may be added or edited. Resume, Fork and
Sidechat keep the original account/connection and immutable execution; there is
no format conversion, automatic selection, active execution switch or alternate
account fallback. Codex requires Responses; Claude Code requires Messages;
OpenCode and the existing Grok API profile require Chat Completions. Format
compatibility does not establish model access or native readiness.

Legacy JSON configuration writes and version 1–3 imports retain their original
configuration semantics. Their execution admission still checks the resolved
original protocol. Explicit account selections reject incompatible Worker
configuration writes. The typed atomic Worker save resolves every account through
the same profile and compatibility checks within its model/account transaction.

The desktop requires an explicit manual format selection between Entry name and
API key when multiple formats are supported, or displays the sole format read-only. Lists, connection details and Worker compatibility use that account's
format. Custom Provider forms use one card per supported format, with URL and
authentication inputs, and require at least one. Account-referenced cards are
locked; the server independently verifies every reference. Capability reads,
permission errors, unavailable profiles and profile changes block new saves with
an explanation. Profile changes invalidate secret handoffs. Accepted uncertain
mutations keep only their original request identity and bytes under the existing
secret disposal rules. Metadata refetch cannot substitute a request or cancel
an already accepted exact connection handoff.

Portable configuration version 4 preserves profiles and explicit selections;
versions 1–3 retain their historical import semantics and cannot carry the new
fields. Connections, protected credentials, observations and history remain
excluded. Imported accounts start disconnected. The separate capability-8 OAuth
extension preserves authentication ownership while selecting inference formats.
Native subscription profiles and service-specific API protocols are outside this
extension.

### Official REST profile registry

The following 35 presets are checked against their linked official REST docs.
R means OpenAI Responses (`POST /responses`), C means OpenAI Chat Completions
(`POST /chat/completions`), and M means Anthropic Messages (`POST /messages`).
Paths are appended to the listed base URL. B means Bearer, K means `x-api-key`,
and L means explicit keyless loopback authentication. Listed profiles are REST
availability declarations, not promises about each model, tool or installed
server version. Regional keys, billing/access restrictions, preview features and
provider limitations remain independent validation and execution conditions.

| Preset | Inference profiles | Official source |
| --- | --- | --- |
| Vercel AI Gateway | R: `https://ai-gateway.vercel.sh/v1` (B)<br>C: `https://ai-gateway.vercel.sh/v1` (B)<br>M: `https://ai-gateway.vercel.sh/v1` (B) | [REST 1](https://vercel.com/docs/ai-gateway/sdks-and-apis), [REST 2](https://vercel.com/docs/ai-gateway/anthropic-messages-api) |
| OpenRouter | R: `https://openrouter.ai/api/v1` (B)<br>C: `https://openrouter.ai/api/v1` (B)<br>M: `https://openrouter.ai/api/v1` (B) | [REST 1](https://openrouter.ai/docs/api/api-reference/responses/create-responses), [REST 2](https://openrouter.ai/docs/api/api-reference/anthropic-messages/create-messages) |
| OpenAI | R: `https://api.openai.com/v1` (B)<br>C: `https://api.openai.com/v1` (B) | [REST 1](https://developers.openai.com/api/reference/resources/responses), [REST 2](https://developers.openai.com/api/reference/resources/chat) |
| Anthropic | C: `https://api.anthropic.com/v1` (B)<br>M: `https://api.anthropic.com/v1` (K) | [REST 1](https://platform.claude.com/docs/en/api/messages), [REST 2](https://platform.claude.com/docs/en/cli-sdks-libraries/libraries/openai-sdk) |
| xAI | R: `https://api.x.ai/v1` (B)<br>C: `https://api.x.ai/v1` (B)<br>M: `https://api.x.ai/v1` (B) | [REST 1](https://docs.x.ai/developers/rest-api-reference/inference), [REST 2](https://docs.x.ai/developers/rest-api-reference/inference/legacy) |
| DeepSeek | R: `https://api.deepseek.com/v1` (B)<br>C: `https://api.deepseek.com/v1` (B)<br>M: `https://api.deepseek.com/anthropic/v1` (K) | [REST 1](https://api-docs.deepseek.com/quick_start/agent_integrations/codex/), [REST 2](https://api-docs.deepseek.com/guides/anthropic_api/) |
| Ollama | R: `http://127.0.0.1:11434/v1` (L)<br>C: `http://127.0.0.1:11434/v1` (L)<br>M: `http://127.0.0.1:11434/v1` (L) | [REST 1](https://docs.ollama.com/api/openai-compatibility), [REST 2](https://docs.ollama.com/api/anthropic-compatibility) |
| LM Studio | R: `http://127.0.0.1:1234/v1` (L)<br>C: `http://127.0.0.1:1234/v1` (L)<br>M: `http://127.0.0.1:1234/v1` (L) | [REST 1](https://lmstudio.ai/docs/developer/rest) |
| vLLM | R: `http://127.0.0.1:8000/v1` (L)<br>C: `http://127.0.0.1:8000/v1` (L)<br>M: `http://127.0.0.1:8000/v1` (L) | [REST 1](https://docs.vllm.ai/en/latest/serving/online_serving/) |
| Google Gemini | C: `https://generativelanguage.googleapis.com/v1beta/openai` (B) | [REST 1](https://ai.google.dev/gemini-api/docs/openai) |
| Groq | R: `https://api.groq.com/openai/v1` (B)<br>C: `https://api.groq.com/openai/v1` (B) | [REST 1](https://console.groq.com/docs/responses-api) |
| Mistral | C: `https://api.mistral.ai/v1` (B) | [REST 1](https://docs.mistral.ai/api/endpoint/chat) |
| Together AI | C: `https://api.together.ai/v1` (B) | [REST 1](https://docs.together.ai/reference/chat-completions) |
| Fireworks AI | R: `https://api.fireworks.ai/inference/v1` (B)<br>C: `https://api.fireworks.ai/inference/v1` (B)<br>M: `https://api.fireworks.ai/inference/v1` (B) | [REST 1](https://docs.fireworks.ai/api-reference/post-responses), [REST 2](https://docs.fireworks.ai/api-reference/anthropic-messages) |
| Perplexity Router | R: `https://api.perplexity.ai/router/v1` (B)<br>C: `https://api.perplexity.ai/router/v1` (B)<br>M: `https://api.perplexity.ai/router/v1` (B) | [REST 1](https://docs.perplexity.ai/docs/router/quickstart), [REST 2](https://docs.perplexity.ai/api-reference/gateway-messages-post) |
| Cohere | C: `https://api.cohere.ai/compatibility/v1` (B) | [REST 1](https://docs.cohere.com/docs/compatibility-api) |
| Cerebras | C: `https://api.cerebras.ai/v1` (B) | [REST 1](https://inference-docs.cerebras.ai/api-reference/chat-completions) |
| Nebius Token Factory | C: `https://api.tokenfactory.nebius.com/v1` (B) | [REST 1](https://docs.tokenfactory.nebius.com/api-reference/inference/create-chat-completion) |
| Novita | C: `https://api.novita.ai/openai/v1` (B)<br>M: `https://api.novita.ai/anthropic/v1` (B) | [REST 1](https://novita.ai/docs/api-reference/model-apis-llm-create-chat-completion), [REST 2](https://blogs.novita.ai/es/use-minimax-m2-7-in-claude-code-via-novita-ai/) |
| DeepInfra | R: `https://api.deepinfra.com/v1` (B)<br>C: `https://api.deepinfra.com/v1/openai` (B)<br>M: `https://api.deepinfra.com/anthropic/v1` (B) | [REST 1](https://docs.deepinfra.com/api-reference/responses/openai-responses), [REST 2](https://docs.deepinfra.com/api-reference/chat-completions/anthropic-messages) |
| Hugging Face Inference Providers | R: `https://router.huggingface.co/v1` (B)<br>C: `https://router.huggingface.co/v1` (B)<br>M: `https://router.huggingface.co/v1` (B) | [REST 1](https://huggingface.co/docs/inference-providers/guides/responses-api), [REST 2](https://huggingface.co/docs/inference-providers/integrations/claude-code) |
| Venice | C: `https://api.venice.ai/api/v1` (B) | [REST 1](https://docs.venice.ai/api-reference/endpoint/chat/completions) |
| Scaleway | C: `https://api.scaleway.ai/v1` (B) | [REST 1](https://www.scaleway.com/en/developers/api/generative-apis) |
| Baseten | C: `https://inference.baseten.co/v1` (B)<br>M: `https://inference.baseten.co/v1` (B) | [REST 1](https://docs.baseten.co/reference/inference-api/messages) |
| Moonshot / Kimi — Global | R: `https://api.moonshot.ai/v1` (B)<br>C: `https://api.moonshot.ai/v1` (B)<br>M: `https://api.moonshot.ai/anthropic/v1` (B) | [REST 1](https://platform.kimi.ai/docs/api/responses), [REST 2](https://platform.kimi.ai/docs/guide/claude-code-kimi) |
| Moonshot / Kimi — China | R: `https://api.moonshot.cn/v1` (B)<br>C: `https://api.moonshot.cn/v1` (B)<br>M: `https://api.moonshot.cn/anthropic/v1` (B) | [REST 1](https://platform.kimi.com/docs/guide/codex-kimi), [REST 2](https://platform.kimi.com/docs/guide/claude-code-kimi) |
| MiniMax — Global | R: `https://api.minimax.io/v1` (B)<br>C: `https://api.minimax.io/v1` (B)<br>M: `https://api.minimax.io/anthropic/v1` (B) | [REST 1](https://platform.minimax.io/docs/api-reference/responses-create), [REST 2](https://platform.minimax.io/docs/api-reference/text-chat-anthropic) |
| MiniMax — China | R: `https://api.minimax.cn/v1` (B)<br>C: `https://api.minimax.cn/v1` (B)<br>M: `https://api.minimax.cn/anthropic/v1` (B) | [REST 1](https://platform.minimax.cn/docs/api-reference/responses-create), [REST 2](https://platform.minimax.cn/docs/api-reference/text-chat-anthropic) |
| SiliconFlow — Global | C: `https://api.siliconflow.com/v1` (B)<br>M: `https://api.siliconflow.com/v1` (B) | [REST 1](https://www.siliconflow.com/es/blog/deepseek-v4-pro-claude-code-siliconflow) |
| SiliconFlow — China | C: `https://api.siliconflow.cn/v1` (B)<br>M: `https://api.siliconflow.cn/v1` (B) | [REST 1](https://api-docs.siliconflow.cn/docs/api/messages-post) |
| Baidu Qianfan | R: `https://qianfan.baidubce.com/v2` (B)<br>C: `https://qianfan.baidubce.com/v2` (B) | [REST 1](https://cloud.baidu.com/doc/qianfan-api/s/vmhejnuy8) |
| Tencent TokenHub — China | R: `https://tokenhub.tencentmaas.com/v1` (B)<br>C: `https://tokenhub.tencentmaas.com/v1` (B)<br>M: `https://tokenhub.tencentmaas.com/v1` (K) | [REST 1](https://cloud.tencent.com/document/product/1823/138677) |
| Tencent TokenHub — International | R: `https://tokenhub-intl.tencentmaas.com/v1` (B)<br>C: `https://tokenhub-intl.tencentmaas.com/v1` (B)<br>M: `https://tokenhub-intl.tencentmaas.com/v1` (K) | [REST 1](https://cloud.tencent.com/document/product/1823/130078), [REST 2](https://intl.cloud.tencent.com/ind/document/product/1300/84216) |
| Alibaba Model Studio — International | R: `https://dashscope-intl.aliyuncs.com/compatible-mode/v1` (B)<br>C: `https://dashscope-intl.aliyuncs.com/compatible-mode/v1` (B)<br>M: `https://dashscope-intl.aliyuncs.com/apps/anthropic/v1` (B) | [REST 1](https://help.aliyun.com/zh/model-studio/base-url), [REST 2](https://help.aliyun.com/en/model-studio/qwen-api-via-openai-responses), [REST 3](https://help.aliyun.com/en/model-studio/anthropic-api-messages) |
| Alibaba Model Studio — Hong Kong | R: `https://cn-hongkong.dashscope.aliyuncs.com/compatible-mode/v1` (B)<br>C: `https://cn-hongkong.dashscope.aliyuncs.com/compatible-mode/v1` (B)<br>M: `https://cn-hongkong.dashscope.aliyuncs.com/apps/anthropic/v1` (B) | [REST 1](https://help.aliyun.com/zh/model-studio/base-url), [REST 2](https://help.aliyun.com/en/model-studio/qwen-api-via-openai-responses), [REST 3](https://help.aliyun.com/en/model-studio/anthropic-api-messages) |

OpenRouter's three inference paths are `/api/v1/responses`,
`/api/v1/chat/completions` and `/api/v1/messages`, all with Bearer. Its credential
inspection remains `GET /api/v1/key`; model inspection remains bounded
`GET /api/v1/models`, parsed as an OpenAI model catalog. Other known profiles
likewise retain their separate canonical catalog/private-authentication routes
under the [provider inspection contract](cmds-delidev-providers-contract.md),
including Fireworks management models and DeepInfra private/model endpoints.
Matching requires the complete documented selected tuple; custom endpoints stay
advisory and cannot inherit official evidence from a protocol or name alone.

Anthropic's Chat compatibility layer has provider-defined restrictions. xAI's
Messages endpoint is documented as legacy/deprecated. Baseten Messages and
Perplexity Router availability remain subject to the provider's preview/access
conditions. LM Studio Responses requires 0.3.29 or later and Messages 0.4.1 or
later. Ollama's Responses endpoint has stateless limitations. Responses support
on Qianfan, Tencent and other gateways does not imply every Responses tool,
continuation feature or model is available. Novita's official Claude Code guide
specifies the Anthropic base and Bearer authentication; its model access remains
separate from the ordinary OpenAI catalog. Provider-side compatibility is not
DeliDev translation. Together Link, SiliconFlow's local Responses converter and
Venice's local Codex/Claude converter do not declare another direct REST profile
on their ordinary preset endpoints.


A disconnected SQL record alone does not prove cleanup: a failed native Connect
may retain protected staging intents. Disconnected format-change admission holds the account
gate, checks the original revision and credential class, then verifies no remaining
native references outside SQLite before publication. Failed enumeration rejects
the edit. Exact accepted receipt replays do not reopen the vault. Keyless proof
skips native enumeration and cannot be relabeled as credential-owning authority.

## Ordered Agent Worker account sources

The originating change established System capability 36 and SaveAgentWorkerRequest field 5 on
main before this extension. Capability 35 remains reserved for known subscription
models. Capability 36 advertises configuration and routing support, never native
execution authority. No SQLite migration is added.

Agent schema 3 uses an ordered nonempty `routes` list. Each route contains
`model_id`, ordered `accounts` with weights 1–1,000, and an optional `routing`
override. The legacy top-level model/accounts/routing and routes are exclusive.
One Worker retains one Harness, name, instructions and native options. Every
route uses one live API Provider or service-native subscription identity; sources
and accounts cannot repeat. Across all routes the existing 1,000-account bound
applies. Legacy schema 1, retired schema 2 and accountless CLI/RPC writes remain
compatible. Adding another source upgrades a Worker to schema 3; removing sources
keeps schema 3, including when one remains. Legacy writes cannot replace an
existing schema-3 Worker. Historical documents and sessions are never migrated.

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
selection, deletion protection and portable version 3 include all sources.
Schedules use the ordinary first-execution boundary.

Codex requires the selected API account's Responses profile. OpenRouter's original
Chat default and existing connections are not rewritten. A new manual OpenRouter
account may explicitly choose Responses; an existing account may use capability
9 to keep its key while changing format for new sessions. Capability 7 alone
requires disconnect, confirmed cleanup and reconnection. Retain
execution-time validation. OpenRouter
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

The server owns provider presets, API model discovery, catalog publication and canonical model selection in `cmds/delidev-cli/internal/providers`, `internal/server`, `internal/store` and the corresponding CLI commands. The complete [the feature requirements](cmds-delidev-requirements.md) remain normative. This contract implements the API catalog boundary; native harness observations, selected-model execution capability and subscription authentication retain their separate authority contracts. API proxy execution and first-dispatch snapshots follow their own implemented authority contracts; a catalog cannot authorize them.

## Runtime and Language

Go with authenticated Connect RPC and server-owned local SQLite. Model lists come from the bounded non-inference [provider inspector](cmds-delidev-providers-contract.md). No harness process, inference request or billing/administrator credential is used.

## Users and Operators

The owner and paired clients manage provider/model configuration. Execution Worker credentials cannot invoke catalog RPCs. The server's joined background maintenance task independently validates enabled API accounts and refreshes catalogs where the enabled provider permits discovery. A model endpoint's localhost always means the server machine; no Worker-localhost tunnel is implied.

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

Enabled, connected API accounts whose providers are enabled are scanned in bounded UUID pages for independently due validation and permitted discovery. Validation runs first when both are due, then discovery uses a freshly read confirmed revision of the same connection. Failed or unsupported accepted validation does not prevent permitted discovery. Discovery-disabled providers still receive validation; their catalogs remain untouched. An account with no observation for its current connection is due immediately. Later observations become due after the greater of 15 minutes and a valid provider Retry-After delay, measured from completion. This is a new periodic observation, not a replay or immediate retry inside an inspection. Failed observations retain the same schedule. A four-worker pool prevents one slow endpoint from blocking all progress; an unaccepted revision/inspection conflict has a 30-second in-memory cooldown. A two-second ticker and state-change/completion notifications drive the scanner, with a 100-millisecond minimum scan spacing during high event traffic. No unbounded catch-up queue is created.

Disabling provider discovery cancels its active discovery inspections. Disconnection cancels all checks for that account; paired-client revocation cancels that client's outstanding requests. Server shutdown cancels and joins automatic work before releasing the vault, database or private scope. Canceled/stale work cannot publish. Re-enabling discovery follows the persisted due time; an explicit discovery can refresh immediately. Explicit `account validate` remains available with discovery disabled and never publishes catalog models.

### Search, groups and canonical selection

Search is a local SQL substring query over display name, native ID and CLI alias, with SQLite's built-in case folding. The query is bounded to 256 bytes, pages to 1–200 (default 50), and filters include optional provider and hidden models. Results sort by provider UUID, custom order, case-folded display name, then model UUID. Each response includes only the provider records represented in its page so clients can render provider groups. Hidden state and order are display preferences; hidden models remain explicitly resolvable and do not lose execution permissions.

Keyset cursors are HMAC-bound to server identity and filters, expire under the ordinary 24-hour cursor limit, and carry the latest model/provider event sequence from the same read snapshot. A model/provider change expires an old page rather than skipping or repeating rows from a changed order. Unrelated account/stream events do not expire it. Page size can change on continuation. Exact full pages may return a continuation whose next page is empty.

Resolution uses an exact canonical UUID first; an upstream native ID cannot shadow that UUID. Otherwise it requires one exact alias/native-ID match within the optional provider scope. It never picks the first ambiguous result. Manual writes reject duplicate canonical pairs and alias collisions with other aliases/native IDs; aliases cannot contain whitespace, separators or a canonical UUID. If later upstream discovery introduces a native ID matching an existing alias, both canonical records remain available and the ambiguous shorthand fails; use a UUID or provider scope. First-dispatch work must resolve and retain canonical provider/model IDs in immutable snapshots and history instead of retaining a display alias as execution identity.

Provider-row account links use the list-only `provider_id` selector on `ResourceService.ListResources`. Account-type filtering composes with that selector in SQL before the account page limit; the returned account page must never be treated as the provider's total account count. See the account lifecycle and protocol contracts for cursor scope and snapshot/event behavior.

### Workflow integration

- Known subscription model suggestions follow `cmds-delidev-catalog-contract.md#known-subscription-model-suggestions`. The originating change established System capability 35 and the advisory declarations on main before activation. Keep bounded read-only owner/client metadata, explicit server outbound routing, joined cache refresh and review-only daily PRs separate from account/native/execution authority. Preserve atomic native-ID Worker saves and saved revision/history checks; no database migration.

### Project requirements

- Key-preserving API format changes follow `cmds-delidev-catalog-contract.md#key-preserving-api-format-change-reservations`: the originating change established ProviderInventory 9 and the complete ChangeAccountApiFormat RPC declarations on main before activation. Preserve capability 7, OAuth reservation 8, protected shared key references, original execution/continuation generations, new-format validation, immutable referenced profiles and all-generation explicit cleanup. No SQLite migration or native change. Reservation-only changes activate no support.

- API account format selection follows `cmds-delidev-catalog-contract.md#api-account-format-selection` and the account/protocol contracts. The originating change established ProviderInventory capability 7, inventory profile field 10, account-list protocol field 5 and the complete closed declarations on main before activation. Preserve schema-3 API families, portable v4 with v1–3 imports, original defaults, capability-7 disconnect/confirmed cleanup and capability-9 key-preserving format changes, account-referenced profile immutability, immutable executions and keyless cleanup proofs. Resolve the selected account profile in every consuming flow; add no database migration or format conversion.

- Inline Worker models and endpoint-only completion follow the allocation amendment in `cmds-delidev-catalog-contract.md`. Record System 42 / Worker 22 and the complete endpoint/identity/pricing declarations in the owning feature PR. Compose complete DB 32 / protocol 2 reset with Worker schema 4 and portable bundle 4, removing independent Models/persistent API catalogs while preserving exact inline settings, pricing history and native/account authority. Earlier DBs and backups are unsupported. The owner waives earlier DB retention, permitting an explicit reset of its DB/sidecars without conversion; protected credentials and native ownership retain their original cleanup authority. Reservation-only changes grant no support.

- DeliDev execution options follow `cmds-delidev-catalog-contract.md#native-execution-option-selection`: forward available native settings exactly without advertised-support gates or a product concurrency cap. Keep unavailable saved values, reject them explicitly at execution, and preserve applied-setting, account, authentication, cleanup and original-recovery proofs. Codex API/subscription accept all four permission modes; full-access may expose managed authentication files. No RPC/storage migration.

- The feature follow `cmds-delidev-catalog-contract.md#new-session-defaults-and-literal-branch-prefixes--issues-2054-and-2057`: System 56/Worker 38, schema-3 Project/Settings and portable v6 preserve capability52, schema1/2 reads and v1–5 imports. Reject destructive legacy writes. Resolve creation Plan defaults until explicit checkbox edit; freeze pending/uncertain mode and exact requests. Preserve literal `delidev/` default, absent inheritance/empty disable, exact UTF-8 ref validation, original source revisions and legacy execution omission bytes. Compose bounded shared prefix instructions in Execute only through existing four-harness channels, never in Plan/read-only Sidechat or shell interpolation. Preserve all native/account/workspace/history/cleanup authority; no migration or numeric native gate.

### cmds/delidev-cli constraints

- Project behavior settings follow `cmds-delidev-catalog-contract.md#project-behavior-settings-issue-1965`: System 52, schema-2 Project/Settings and portable v5 preserve continuous typed inheritance, explicit project context, source-route/Agent and repository precedence, all three fetch gates, immutable snapshots and first-publication durable plan response decisions. Previously pending plans stay manual; uncertainty never permits resend. Preserve v1–4 imports and reject destructive legacy writes. No SQLite migration or Worker capability is added.

### cmds/delidev-cli/internal/cli constraints

- Current protocol-2 model catalogs are retired: ordinary Model CRUD/search/resolve returns typed Unsupported before server selection or authentication input. Keep native observation commands independent. Inline schema-4 fixture routes require original same-source Accounts; disconnected fixture Accounts remain disconnected and grant no native readiness. Token pricing uses Provider-or-SubscriptionService plus the exact native ID, never a Model UUID.

### cmds/delidev-cli/internal/domain constraints

- OpenCode foreground children follow the feature and the subagent contract: close original task input/metadata and product/native tool references, retain exact independent child IDs, same model and nullable response counters. Keep task observations separate from independently read content/history, reject nested/reused/background ownership and never add child usage to root accounting.

- OpenCode context metadata preserves a selected known/user-declared model limit in an optional immutable execution snapshot before its first digest. Omitted legacy limits and limits with unknown metadata sources remain unknown and do not change the execution digest. Original automatic context progress uses the closed OpenCode native part identity and ordered lifecycle; summaries/continuation users grant no product input or fabricated accounting.

- Agent Worker ordered source routes follow the catalog, desktop, protocol and sessions contracts. Main reservation the originating change owns System capability 36 and SaveAgentWorkerRequest.route_models field 5; preserve capability 35. Keep schema-3 routes exclusive with legacy fields, all models/accounts referenced and saved atomically, per-source routing state updated only with a successful first claim, confirmed-exhaustion-only fallback and observed-recovery preference for later new sessions. Preserve immutable executions, old-client write protection and portable v1/v2/v3 compatibility; add no SQLite migration.

- `native_options.go` separates saved selection validity from execution adapter availability under the catalog native-option contract. Preserve unavailable saved values and report exact option names before execution. Bounded efforts and approval policies are not support enums; uint32 concurrency has no product cap. Retain exact applied observations and immutable same-account model references.

### cmds/delidev-cli/internal/harness/codex constraints

- Empty native model verification is discarded metadata only for the original known root turn. Populated/foreign verification remains a private extension; missing/null/malformed arrays or unknown fields fail. Closed extension classifications provide diagnostics without logging native methods or payloads. Installed thread fixtures must reject unhandled extensions.

- Before native thread Start/Resume, pass explicit child model and effort selections unchanged without gating on advertised native models or effort lists. Native rejection remains an execution error and cannot authorize omission or default retry. Canonical same-account child model references remain mandatory and grant no additional account authority. Concurrency retains the exact native `agents.max_concurrent_threads_per_session` semantics, including native V2 composition with the root; zero omits the override. Native Code Mode/runtime and collaboration availability remain independently required and must not be inferred from a saved model selection.

- Discard only notification-form `skills/changed` with a required empty object as closed process metadata. Never refresh skills/catalogs, enumerate packages or alter immutable selected input; null/missing/non-object/nonempty/malformed payloads, same-name server requests and unknown families retain rejection. Preserve original input, continuation, recovery and cleanup fences under the harness contract.

### cmds/delidev-cli/internal/knownmodels constraints

- On restart, restore a cache only when its reviewed date is newer than the bundled catalog or its date and semantic version exactly match; an equal-date version mismatch is ambiguous, so prefer the bundle and refresh immediately.

### cmds/delidev-cli/internal/server constraints

- Agent Worker wizard saving follows the catalog/protocol contracts: derive one current account source, require at least one account, preserve ordered routing, resolve exact model identity and write model plus Worker in one receipt transaction. Bind source-scoped list cursors before pagination. Preserve canonical metadata/history and legacy accountless APIs; capability 33 is configuration support, never native readiness.

- Preserve each child’s original explicit requested model and reject Claude-only task/tool metadata on Codex batches. Check historical native/product/execution ownership once for the complete batch before publication; keep atomic rejection and source-specific nullable usage.

- Windows OpenCode General Chat dispatch uses the separate root/checkpoint profile in the harness/session contracts. Remove only the OS exclusion; preserve immutable selection, current account/model/Worker and preparation gates. The owning Worker independently verifies exact native `/` and canonical filesystem roots before native work. Continuation and completed-report recovery require the original accepted report/history and independent root/process/workspace proof; no server-native path interpretation, legacy checkpoint promotion or input replay.

- Native execution option availability follows the catalog native-option contract. Reject missing adapters before execution admission with the exact field name; do not gate forwarded values on advertised support or rewrite accepted snapshots. All four Codex API/subscription permissions preserve original leases, account/Worker authority and cleanup/recovery.

### cmds/delidev-cli/internal/store constraints

- Follow `cmds-delidev-catalog-contract.md` for presets and model discovery. Join bounded automatic refresh work before releasing server resources; cancel discovery when disabled, and publish models/observation/events/receipt atomically. Preserve manual registrations, user-declared metadata, display preferences, acknowledged NEW state and explicit-deletion suppressions. Discovery cannot grant account readiness or harness compatibility. Canonical UUIDs take precedence over upstream names; ambiguous alias/native selectors fail. Search cursors bind filters and the model/provider event epoch, and legacy identity conflicts must fail migration without rewriting original data.

- Current protocol-2 stores directly initialize the complete Model-free schema 32 from `schema_032.go`. Startup, inspection and restore reject earlier layouts before writes; there is no schema 1–31 conversion, replay or compatibility repair. Preserve original allocation ownership and all current native/protected cleanup records. Frozen historical schema fixtures document earlier layouts and grant no current upgrade path.

- Project behavior settings follow `cmds-delidev-catalog-contract.md#project-behavior-settings-issue-1965`: System 52, schema-2 Project/Settings and portable v5 preserve continuous typed inheritance, explicit project context, source-route/Agent and repository precedence, all three fetch gates, immutable snapshots of the exact selected route policy and first-publication durable plan response decisions. Previously pending plans stay manual; uncertainty never permits resend. Preserve v1–4 imports and reject destructive legacy writes. No SQLite migration or Worker capability is added.

### cmds/delidev-cli/internal/tokenprices constraints

- Keep 15-second/16 MiB and bounded inventory limits, exact decimal rates and source/native identity. Automatic rates grant no model/account/native support. Preserve missing values, unsupported variants and original unpriced Grok accounting.

### cmds/delidev-cli/internal/worker constraints

- The Grok first-text Worker runner and public initial dispatch are limited to Execute/default settings, owned General Chat and the pinned API profile. Snapshot the selected model's explicit context window plus known/user-declared provenance; match it against original native setup without inventing a limit for historical/unknown metadata. Reject unsupported selections atomically before consuming input/routing. Retain registration metadata outside the empty runtime, replay only original outbox receipts and require independent workspace-lease cleanup before completion output. Before acceptance and on stream loss, cancellation joins the process and retains uncertainty. Accepted text Stop, including before the first output chunk, uses the original once-only native claim, independently retained terminal/idle/cleanup, acknowledged content and server cancellation proof within a bounded grace; force cleanup cannot manufacture native Stop, a continuation checkpoint or a replacement input.

- Grok Build's private API initialization/session-creation profile is separate from discovery and public execution. Reconstruct the private environment, inspect machine/runtime/workspace configuration, pin primary and auxiliary native model selectors to one registered relay model, and accept only the documented original native purge marker when configuration changes. Preserve strict JSON-RPC 2.0 and bounded redacted output. Persist original creation and native-binding claims before further work; failed or uncertain creation never permits another attempt on that runtime. Native setup/MCP/model observations grant no prompt, permission, subscription, continuation or Worker dispatch authority by themselves. MCP initialization may finish after the native session actor is spawned but before the remaining ordered setup phases; retain independent barriers and require both before accepting input. Reject premature, duplicate, foreign or incomplete initialization.

- Grok's private original Read input profile uses the same once-only input claim and native binding as text, then composes only the validated default file-read observations through the original callback/lifetime boundary. Keep individual model-response counters, the last response's RPC metadata, aggregate original-input usage and context separate; compare reported aggregate primitives with overflow-checked original response totals, without deriving absent total tokens, duration or auxiliary costs. Reject Write/request authority, unsettled tools, changed accounting and replay; Read completion cannot borrow plain-text Stop, native-close/history or continuation authority.

- Codex metadata cannot silently change the accepted root/model/options/permission or active mode. Keep internal instructions, native warning bodies and remote identities out of product records/logs. Native token observations preserve cumulative versus last-request counters and unavailable fields, carry immutable assignment-derived account/model provenance, and replay through the same durable outbox identity. Never sum these observations, fabricate zeros/costs or treat an empty quota bucket as recovery; counter-reset/fork/subagent aggregation and populated quota require their own validated adapters.

- Claude API initialization must verify separately correlated native applied model/effort before returning a stream. Preserve native default/null effort and exact immutable explicit selections; raw settings, requested flags or model advertisements cannot substitute for applied evidence. The current private profile requires empty effective/source collections and no advisor/ultracode. Reject malformed or mismatched observations before input, join cleanup, keep raw sources private, and treat retained initial settings as historical metadata that needs a fresh read after configuration changes. Native configuration evidence does not prove hosted-provider capability.

- OpenCode Worker requested settings must preserve the immutable model/instructions/input mode and reject unsupported explicit options before native work. Requested native defaults are not observations. Emit the private initial `opencode_agent` observation only after original owned configuration/context/agent verification, for an explicit empty session override array; keep missing effort/tier null and reject cross-harness or input-mode drift. Do not reinterpret native defaults as a sandbox or grant public dispatch/publication from shared shape validation.

- Codex empty model-verification metadata grants no account/model/input authority and is consumed only after the harness binds it to the original known root turn. Unsupported event logs use closed extension classifications, never raw native methods or payloads. Preserve unknown-event rejection and original accepted-input recovery.

### Protocol integration

- Inline Worker models and endpoint-only completion follow the allocation amendment in `cmds-delidev-catalog-contract.md`. Record System 42 / Worker 22 and the complete endpoint/identity/pricing declarations in the owning feature PR. Compose complete DB 32 / protocol 2 reset with Worker schema 4 and portable bundle 4, removing independent Models/persistent API catalogs while preserving exact inline settings, pricing history and native/account authority. Earlier DBs/backups/sidecars remain untouched and unsupported; reservation-only changes grant no support.

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

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References

- [Project](project-delidev.md)
- [Repository defaults](repository-defaults.md)
- [Provider inspection](cmds-delidev-providers-contract.md)
- [Account lifecycle](cmds-delidev-accounts-contract.md)
- [Connect protocol](protos-delidev-v1-contract.md)

### Provider activation and filtering

Follow [API provider activation](cmds-delidev-provider-activation-contract.md) for stable preset identity, effective legacy defaults, bounded ProviderService inventory, exact account counts, provider-scoped account pages and the additive `SearchModels.enabled_providers_only` filter. The active-provider condition is applied before SQL pagination and included in signed cursor scope. Disabled providers retain canonical model records and remain resolvable for historical references; catalog visibility never grants execution.

## Agent Worker model selection

System capability 33 and ConfigurationService.SaveAgentWorker follow the main
reservations established in the originating change. Source-scoped account and model list
selectors are applied in SQL before LIMIT and bound into signed cursors. Unknown
services, mixed API/service filters and non-account account selectors fail.
Unspecified fields keep legacy behavior; coherent snapshots/events are unchanged.

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
deleted Worker. Existing model/configuration CLI and RPC paths retain their
accountless behavior. No migration or historical snapshot rewrite is introduced.

## Inline Worker models and endpoint-only completion reservation

The owner-approved replacement for this feature removes the independent saved
Model registry, model-input history and persistent API catalogs. API completion
uses only the selected Provider model endpoint. Exact direct IDs remain local to
the selected Agent Worker configuration. Subscription completion retains the
known official advisory catalog, without merging saved records. Token pricing
uses the exact `(API Provider UUID or SubscriptionService, native ID)` identity.

Record System `INLINE_WORKER_MODELS_V1 = 42`, Worker
`INLINE_MODEL_EXECUTION_V1 = 22`, `ModelIdentity`, `EndpointModel`,
`ListEndpointModels` request/response, source/native-ID token-pricing declarations
and the additive usage/pricing model-identity fields in the allocation ledger
with the owning feature PR. Records alone advertise no runtime capability.

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
allowed. The Provider editor omits the retired automatic-discovery control.
Retain any existing `Provider.discovery` value as inert configuration metadata
through unrelated edits and portable export; it grants no refresh, catalog or
credential authority. Disconnect, revocation and shutdown cancel and join
original requests.
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

## Native execution option selection

Saved Agent options retain bounded exact values, including selections that lack a native execution adapter. Saving or changing harnesses never deletes or converts those values. Execution validates adapter availability before admission and reports the exact unavailable option name. The editor disables unavailable controls, explains why below the field and offers explicit clearing of a retained value.

| Harness | Forwarded native settings | Unavailable settings |
| --- | --- | --- |
| Codex API and subscription | All four permission modes; bounded exact root effort, approval policy, service tier, same-account child model/effort, uint32 concurrency and User/AI approval reviewer | Custom approval-review model and Claude permission |
| Claude | Native permission modes, including auto, and bounded exact effort | Codex sandbox, approval policy, service tier and Codex child overrides |
| OpenCode | Native Build/Plan primary-agent policy and exact model `options.reasoningEffort` | Codex sandbox/approval, Claude permission, service tier and Codex child overrides |
| Grok Build | Existing default permission and native Execute/Plan selection | Effort without an applied-setting observation adapter, Codex sandbox/approval, Claude permission, service tier and Codex child overrides |

Forwarded settings are not restricted by advertised model/effort lists or a DeliDev concurrency maximum of 64. Preserve format/size bounds, canonical model/account references and immutable execution digests. Unselected values omit native overrides and retain native defaults. Native initialization must independently verify actual applied settings before input; drift is an error, never permission to normalize or retry with a default. Native rejection uses the existing execution error path. Positive no-send and cleanup proof remain necessary for any retry; uncertain input delivery retains original recovery ownership.

Codex subscription default/full-access do not waive managed authentication ownership, canonical non-overlapping private homes, original-process verification, final credential comparison or independent cleanup. Full-access may let native tools access managed authentication files. Structured failure logs contain the stage and closed option names, never credentials, selected values or raw native output. Public RPC, stored document formats and historical execution records remain unchanged; add no migration or format conversion.


Automatic validation uses the [provider verification boundary](cmds-delidev-providers-contract.md#automatic-api-verification). Its persisted due time and Retry-After do not replace catalog scheduling. Authentication and model observations retain separate request receipts, connection ownership and completion times. New-format generations require fresh observations; original executions and shared key references remain unchanged. No public protocol, migration, native adapter or reserved model-system activation accompanies this scheduler.

Provider inventory and model search pages retain complete entries within a 4 MiB budget measured in both protobuf and protobuf JSON, including cursor/envelope and capability metadata. Model pages charge and deduplicate only the providers represented by returned models. Byte-limited continuation follows the last returned entry/model under the existing query/filter/epoch binding. A single unfit entry returns correlated ResourceExhausted with narrowing guidance; it cannot produce an empty nonadvancing page.

## Project behavior settings

System capability `PROJECT_BEHAVIOR_SETTINGS_V1 = 52` owns the complete project
settings feature. Project and Settings documents use schema 2. Schema-1 documents
remain readable with inherited project values and disabled plan automation.
Legacy full-document writes cannot erase schema-2 settings. No SQLite migration
or Worker capability is introduced.

A project stores typed boolean inheritance (`inherit`, `enabled`, `disabled`),
optional routing and an optional complete remediation policy. Absence means
continuous inheritance from the currently connected server. Explicit false and
empty selections remain explicit. Routing priority is source-route/Agent,
project, then global. Remediation priority is repository, project, then global;
an override replaces the complete policy. Automatic Worktree fetch requires
server permission, effective project permission and repository permission.
Local execution never automatically fetches. Resolve only explicit selected or
original retained project associations; repository membership cannot select a
project. Existing execution, workspace and remediation snapshots stay immutable. Freeze the exact selected route policy in the execution configuration; selection and historical attribution must agree for inherited, project and source-route/Agent policies.

Global plan automation defaults to disabled. General Chat uses the global value.
At the first original native plan approval publication, retain the policy
decision and eligible ordinary durable response atomically. Claude ExitPlanMode
and Grok `_x.ai/exit_plan_mode` use their original affirmative replies. Questions,
tool permissions and plan-capable profiles without an approval request remain
outside this feature. Previously pending requests stay manual across setting
changes, replay and restart. Manual/automatic races share the original response
controller. Original execution/account/Worker/artifact checks and uncertain
native delivery recovery remain required; uncertainty cannot authorize resend.
Policy failures retain normal handling with sanitized phase/outcome diagnostics.

Portable configuration version 5 preserves these values and complete remediation
references with explicit binding and atomic import. Versions 1–4 retain their
original defaults and cannot carry the new settings. Project remediation machine
references contribute to the existing complete machine bound. Credentials and
native ownership do not enter portable documents.

The once-only automatic plan policy decision stays in the original server-owned interaction record. Public schema-1 Interaction JSON omits that private provenance so strict legacy Worker decoders continue to read the unchanged native request and queued response. Restart receipts retain the private first decision and original response ID.

### Explicit Codex Fast mode
Agent Worker configuration presents Native default, Fast mode and Custom service tier through the shared legacy and ordered-source form. Native default explicitly omits `options.service_tier`; Fast saves exact `fast`. Existing other nonempty strings select Custom and remain exact, including whitespace, until edited. Opening the editor never changes absent, empty or null values. Custom entry retains the existing bound. Other harnesses retain disabled saved values and the original explicit clearing action; source/loading/save admission gates remain owned by the existing wizard/editor.

Fast is an explicit native Codex request for API and ChatGPT subscription sources, subject to original model/account availability. The localized control links [official guidance](https://learn.chatgpt.com/docs/agent-configuration/speed) and explains potentially higher subscription usage without a fixed billing multiplier, entitlement or speed claim. Saving grants no native support. No account defaults, session switches, feature flag, version gate, protocol allocation or migration are added. Immutable execution and continuation configurations retain their original tier after Worker edits. Existing applied-setting verification rejects null/different native tiers before input and never downgrades or retries with defaults.

## New-session defaults and literal branch prefixes
The complete feature uses System capability 56 and schema-3 Project/Settings
JSON, preserving capability 52 and readable schema-1/2 documents. Legacy full
writes cannot replace a newer document with an older schema. Portable exports
and reviewed import plans use version 6; versions 1–5 retain their original
semantics and cannot declare the new fields. No SQLite migration is added.

Server `plan_mode_default` defaults to false. The project's typed
`settings.plan_mode_default` inherits or explicitly enables/disables it.
New Session follows the selected project override and then the global value;
General Chat follows the global value. Defaults apply until an explicit mode
checkbox edit. Prompt, Agent, Runner and budget edits do not count as mode edits.
Loading, failed, incomplete, stale or malformed reads cannot authorize a
provisional mode. Explicit mode selection remains available. Freeze original
mode during pending/uncertain creation, preserve exact retries and mounted draft
ownership, and reset automatic resolution after accepted creation.

Server `branch_prefix` defaults to literal `delidev/`. Optional project
`settings.branch_prefix` inherits when absent; explicit empty disables the
instruction. Preserve exact UTF-8 input, add no separator and accept no variable
expansion. Nonempty values are bounded to 256 UTF-8 bytes and must form a valid
Git branch name when concatenated with the fixed safe suffix `branch`. Domain
and desktop validators reject the same control/space/ref grammar. Values never
enter shell commands or diagnostics.

The accepted configuration transaction pins the selected prefix and original
Settings/Project IDs and revisions in optional version-1 immutable execution
metadata. Omitted declarations preserve historical bytes and digests. Current
settings never rewrite prior generations. Retain selection through Plan/Execute
transitions, continuation, retry, recovery, compaction and Fork snapshots.
The existing ordered template text remains exact. Compose one deterministic
shared instruction after it in Execute only, including it in the aggregate
256 KiB limit. Plan and read-only Sidechat receive no prefix instruction. Codex,
Claude Code, OpenCode and Grok Build use their existing instruction channels;
Grok's unchanged empty-template Plan profile remains independently required.
Worker capability 38 negotiates the complete declaration before native dispatch;
unsupported Workers receive an explicit update requirement, with no native
version gate. This preference never creates/renames branches, changes detached
Worktree or Local preparation, grants Git workspace authority, or guarantees
model compliance. General Chat gains no repository authority.

UI uses the existing explicit-save Project defaults singleton editor for both
the Plan checkbox and the global Branch prefix. Project
settings expose typed Plan inheritance and Use server default / Override project
prefix choices with effective values and literal-input guidance. Preserve hidden
fields, revision conflicts, exact uncertain requests, English/Korean text,
Settings search, keyboard access and responsive presentation. Diagnostics retain
operation identities, revisions and safe outcomes, never prefixes/instructions.

## Codex reviewer selection
Configure exposes a separate localized User / AI auto-review picker for Codex. AI selection explicitly sets `on-request` while preserving sandbox and unrelated native options. The server capability `CODEX_APPROVAL_REVIEW_V1 = 55` enables this configuration surface; the selected Runner must independently advertise Worker capability 30 before dispatch. Unsupported consumers fail without stripping a selected reviewer. The retained foreign option has an explicit Clear action; harness switching alone never clears it. Portable configuration and atomic Agent/Worker saves preserve this optional enum through the existing typed options document, without changing absent historical bytes or adding a database migration. Follow the [native reviewer profile](cmds-delidev-harness-contract.md#codex-ai-approval-reviewer).

## OpenCode Go subscriptions
The [OpenCode Go contract](cmds-delidev-opencode-go-subscription-contract.md) owns the exact key-backed `opencode_go` exception, fixed server relay profile, original native session header and independently confirmed cleanup. Identity 4, System 54 and Worker 28 retain separate ownership; System 52 remains Project behavior. Native login and quota authority remain unavailable. No migration is added.

## Inherited harness configuration

Current Agent documents use schema 5 when `harness_selection` is present. Project
and Settings documents use schema 4 when `harness_defaults` is present. The existing
configuration owner stores all three scopes; project behavior settings remain
independent. No SQLite migration is added.

`harness_selection` contains typed `inherit` or `override` values for effort and
each native option. An inherited value has no `value`; an override retains an
explicit value, including empty strings and zero concurrency. Missing selections
inherit. Each source route has a typed `model_selection`. Its retained inline
model preserves source identity and old-client compatibility; an inherited model
selection never treats that retained native selector as a default. Explicit model
overrides must retain the same provider or subscription identity.

Settings and Projects contain at most 256 `harness_defaults` entries. An entry
selects a harness, an optional provider or subscription source and an optional
API protocol for that provider. It holds an optional exact inline default model
and typed native-option selections. Duplicate scopes fail. Resolution applies
server harness, server source and server API-profile values, then project values
in the same specificity order, then Agent overrides. Source/account order,
weights, routing, templates and package ownership remain unchanged.

On opening the current store, one atomic transaction converts all existing
Agents without `harness_selection` to inherited effort/options/model selection.
It changes current configuration documents and revisions only. The nonnull
selection is the durable completion marker, so restart cannot repeat conversion.
A malformed document, cancellation or failed publication rolls back the complete
conversion. Sessions, execution snapshots, receipts and historical digests retain
their exact bytes. Later explicit overrides remain valid.

First-execution routing chooses the exact account with its original eligibility, order, weights and routing policy before resolving inherited defaults for that account's validated API profile. Incompatible sibling profiles remain blocked candidates; they cannot supply default evidence or block an otherwise eligible source. Unknown selected-profile defaults refuse admission rather than selecting another account or profile. Only the selected source receives effective model attribution; unselected inherited source anchors remain unavailable as effective models. The original connection profile also owns the authentication check.

First-execution routing resolves configuration in its original store transaction
and freezes exact model/native options plus Settings and Project revision pairs.
It performs no separate native execution probe. Actual original native startup
still verifies applied settings before input. Unspecified native effort/options
retain native defaults. A model without a configured or independently verified
default fails with the named inherited-model-default configuration error before
input. Advisory model lists and old explicit model values are not default proof.
No installed adapter currently supplies independent default-model evidence; do
not invent one or run a probe to fill this gap.

Destructive legacy Agent, Settings and Project writes fail before mutation.
Current clients retain complete inheritance fields and original request/revision
identity. Protected credentials, routing state, account authority and original
cleanup remain independent. Structured upgrade and resolution logs contain phases,
counts, harness and safe error codes only.
