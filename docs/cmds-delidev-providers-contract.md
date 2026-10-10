# DeliDev provider inspection

## Inference profiles and inspection profiles

The [REST registry](cmds-delidev-catalog-contract.md#official-rest-profile-registry)
declares each offered API format separately. The common server resolver first
selects the account's declared format or original legacy tuple and checks its
connection-generation pin. The inspector then matches that complete official
tuple to the fixed canonical catalog/authentication routes already owned here.
Inference suffixes never become model-list or key-inspection paths. OpenRouter
Messages and Responses both use `GET /api/v1/key` and `GET /api/v1/models` with
Bearer and the OpenAI catalog parser; neither sends Messages or Responses during
validation. Custom URLs remain advisory. Credential state, late-publication
revision/connection checks, bounded GET inspection, outbound routing and secret
clearing retain their existing ownership.


## Ownership and scope

The server owns non-inference API checks in `cmds/delidev-cli/internal/providers`, exposed through `AccountService.ValidateAccount` and `account validate --id ID --revision N`. The complete [issue #964 requirements](cmds-delidev-requirements.md) remain normative. This inspector implements bounded model-list inspection and credential evidence. Automatic catalog publication and provider presets are integrated through the separate [catalog contract](cmds-delidev-catalog-contract.md). Quota refresh, subscription authentication, API proxy execution and selected-model/harness validation remain required work and must preserve the same explicit authority and secret boundaries.

Only owner/paired-client RPCs can invoke validation. The key is read from the current immutable connection's protected server reference, used locally for the request, and cleared after use. It is never returned or forwarded to a Worker. Keyless endpoints remain loopback-only; localhost always means the server machine. Explicit account validation may inspect the models endpoint even when automatic discovery is disabled, but never registers models or changes a session configuration.

## HTTP boundary

Inspection uses only `GET` requests under the saved provider's API base path or the exact documented native/private endpoints below. OpenAI Chat Completions/Responses-compatible providers use `/models`; Anthropic Messages-compatible providers use `/models?limit=1000` and bounded `after_id` pagination. The exact OpenRouter profile uses explicit `limit=500`, numeric `offset` and `output_modalities=text`; it validates `total_count` when present and accepts older count-less responses only through a bounded short-page walk. The text-output filter retains models with image/audio inputs and excludes image-, audio- and video-only output models whose token context may be zero. Context-limit validation remains strictly positive; this filtered observation never deletes previously saved models. It never follows response pagination URLs. It sends the configured bearer or `x-api-key` authentication, adds the Anthropic API version where applicable, and fixes `HTTP-Referer: https://deli.dev`. It accepts no caller-supplied headers or destination overrides. Configuration rejects credentials, queries (including an empty query marker), fragments, encoded path components, backslashes and traversal segments in base URLs.

HTTPS verifies the system trust roots and hostname; plaintext is allowed only on explicit loopback. Direct and exact-bypass routing dial literal `localhost` through actual loopback IPs instead of an external resolver. Explicit proxy routing retains the original destination authority. Plaintext loopback provider requests require Direct or an explicit matching bypass and are rejected before connection otherwise; they never expose an account key through a proxy tunnel or silently change routes. Verified HTTPS destinations may use the selected proxy. Requests do not inherit environment proxy settings or cookies. Production inspection now applies the explicit selected server profile through the [outbound networking contract](cmds-delidev-network-contract.md), including catalog and credential checks. Worker selections cannot affect it, and failures never fall back.

The entire inspection has a 20-second context deadline, with bounded dial, TLS handshake and response-header waits. Response headers are limited to 32 KiB. Successful JSON bodies are limited to 4 MiB each and private checks plus model pages to 16 MiB in aggregate, 32 pages and 10,000 models. Connections are not reused, avoiding transport retries on a previously used connection. No HTTP redirect, provider retry, protocol translation, inference or account/model fallback occurs.

Provider JSON may contain future fields, but known fields use exact names. Invalid UTF-8, duplicate keys, extra documents, excessive nesting, malformed model identity, duplicate identities and incomplete/looping pagination fail the whole inspection. Model identifiers and advisory display names are bounded; native IDs remain exact and are sorted only for stable output. The bounded model-ID character set includes `~`, preserving OpenRouter latest aliases without resolving or rewriting them. Optional context limits, input/output modalities and supported tool/reasoning parameter evidence remain advisory. Missing/null fields remain unknown; an explicit supported-parameter list can establish an advisory false. Invalid known metadata rejects the complete response. Reflected raw/Base64 key strings in retained fields are rejected. Error bodies, arbitrary diagnostic headers, redirect locations, account labels and provider request IDs are discarded. Only typed failures, closed content-free diagnostic classifications, HTTP status and a parsed bounded Retry-After value leave the HTTP layer. The inspector never retries a request. The separate periodic catalog task uses an accepted retry delay as a minimum interval for its next new observation.

Context limits use the same raw/Base64 key comparison as string metadata, applied to the exact decimal representation that will be retained. A reflected numeric credential rejects the whole model page without exposing a partial catalog.

## Authentication evidence

Endpoint response and credential evidence are independent:

- Exact official OpenAI, Anthropic, xAI and DeepSeek authorities with their documented API paths/authentication use the authenticated models interface as non-inference credential evidence.
- Exact OpenRouter and Vercel AI Gateway authorities validate their key/credits interface before reading the public model catalog. The response's account labels, monetary values and usage totals are discarded; they do not become DeliDev usage, cost or quota observations.
- An explicitly keyless local endpoint records `keyless-endpoint` after a valid catalog response.
- Other compatible/custom endpoints record authentication `unknown` even when their catalog responds. A public listing cannot prove that a configured key authorizes execution. Current `account validate` preserves that successful endpoint observation but returns a typed unsupported credential-validation result and leaves health unverified. Completing custom authenticated execution support remains required work; it cannot be claimed from this inspector alone.

Known authenticated and keyless success can set account health ready. This is neither proof that every listed model is allowed for inference nor a grant of native harness execution capabilities. A 401 records rejected credentials without inventing an expired/revoked distinction; a 403 records denied access; a 429 records request limiting without asserting quota exhaustion. Unsupported, malformed, TLS, timeout and network failures remain distinct. No validation result clears existing exhaustion or quota evidence, and a passed reset timestamp is never interpreted as recovery.

## Durable account result

Validation requires the current account revision and connection with no pending removal. Authorization, connection generation and provider authority are checked before key access and again at publication. A server-wide limit of eight checks and one check per account bounds outstanding work. HTTP never holds the account gate or a SQLite transaction, so slow providers do not block unrelated account checks, metadata work or disconnection.

Disconnection cancels that account's outstanding check before credential cleanup; client revocation and server shutdown cancel its request context. A canceled or stale generation cannot publish readiness. The accepted observation, health, account revision, event and request receipt commit atomically. Completed request replay returns the original observation and current account metadata without network access, including after restart or later disconnection. A new observation requires a new request identity/current revision. Accepted failed observations, including protected native-store read failures, are also replayed without repeating key access or network calls. Observation timestamps use completion time so Retry-After is not shortened by a slow request.

Account metadata contains the connection-bound `validation` observation: request identity, timestamp, state, authentication evidence, model count, bounded status/retry metadata and sanitized problem. General configuration cannot forge or remove it. Disconnect clears the current observation. `ValidateAccount` returns both the current account and the accepted observation; CLI output retains both when a validation problem produces a nonzero typed exit code.

Logs record account/request/correlation identities, phase, state, authentication evidence, failure classification and duration. Credential and model-catalog failures also log closed `inspection_stage` and `inspection_reason` classifications; parser error prose never enters logs. These fields are transient diagnostics, not public RPC or durable receipt fields. They exclude keys, raw responses, endpoints and model content. Account validation never persists models. The separate catalog publication operation preserves manual registrations, display preferences and canonical identity as specified in its contract.

## Verification and references

Real loopback HTTP tests cover headers, direct transport, redirects, error redaction, TLS verification, cancellation, response bounds, exact JSON fields, pagination and malformed input. Real Connect/SQLite tests cover readiness, unsupported custom credential evidence, failure receipts, restart/replay, disconnect cancellation and independent account progress. The disposable Linux Secret Service CLI test additionally retrieves its native protected key, sends it only to an in-container loopback fixture, preserves the unsupported authentication observation and replays without another request. These are fixture/native composition results, not real provider-account acceptance.

Official interface sources checked for this implementation:

- [OpenAI model listing](https://developers.openai.com/api/reference/resources/models/methods/list)
- [Anthropic model listing](https://platform.claude.com/docs/en/api/models/list)
- [OpenRouter current API key](https://openrouter.ai/docs/api/api-reference/api-keys/get-current-api-key), [paginated model catalog](https://openrouter.ai/docs/api/api-reference/models/list-all-models-and-their-properties) and [API-key guidance](https://openrouter.ai/docs/api_reference/authentication)
- [Vercel model listing](https://vercel.com/docs/ai-gateway/sdks-and-apis/openai-chat-completions/rest-api) and [official SDK credit check](https://github.com/vercel/ai/blob/main/packages/gateway/src/gateway-fetch-metadata.ts)
- [xAI inference API authentication](https://docs.x.ai/developers/rest-api-reference/inference)
- [DeepSeek model listing](https://api-docs.deepseek.com/api/list-models/)
- [Ollama OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility), [LM Studio model listing](https://lmstudio.ai/docs/developer/openai-compat/models), and [vLLM online serving](https://docs.vllm.ai/en/latest/serving/online_serving/)

Successful model discovery with unobservable authentication records `state=unsupported` and an unsupported problem while leaving the account unverified; HTTP success alone never records successful credential validation.

## Provider-wide activation

Provider enabled state and preset provenance are governed by the [provider activation contract](cmds-delidev-provider-activation-contract.md). Discovery requires both provider and discovery enabled; turning a provider Off cancels only its in-flight catalog work, prevents stale publication, and preserves previous model observations. Explicit account validation remains separate. Legacy providers without an activation field remain effectively enabled, while generic writes preserve a stored false value.


## Additional fixed hosted inspection profiles (#1148)

Official key-creation metadata is static presentation data on each hosted preset.
The native desktop compiles the tool-owned allowlist generated from Go by
`go -C cmds/delidev-cli run ./internal/providers/cmd/guidance > apps/delidev/src-tauri/provider-guidance.generated.json`
at the repository root. The freshness test compares exact generated bytes.
Explicit help clicks send only a closed preset/action, recheck the trusted native
window instance and dispatch its compiled HTTPS destination through a bounded OS
opener. Selected-server metadata cannot add a destination. Regional services keep
distinct key guidance; a general official guide is used when a fixed console link
would select the wrong region. Opening documentation creates no credential or
validation authority. Custom/nonnative clients retain inert help text.

The catalog contract lists all 35 presets. The 26 additions use ordinary Bearer keys and Chat Completions inference defaults. Inspector profiles match the exact HTTPS authority, base path, protocol and authentication, including equivalent custom copies. A different authority/path/protocol/authentication is custom and cannot inherit official credential evidence. Catalogs cannot distinguish a regional, subscription or coding-plan key type.

Gemini uses fixed `/v1beta/models`, `x-goog-api-key`, `pageSize=1000` and bounded `pageToken`; no key query or Bearer header is sent. It retains `generateContent` models and removes only the exact `models/` prefix. Together parses its official top-level array and retains chat models. Fireworks uses `/v1/accounts/fireworks/models`, page size 200 and token pagination; ready serverless `HF_BASE_MODEL` entries retain their full account/model ID. Cohere uses `/v1/models?endpoint=chat&page_size=1000`, `next_page_token` and `page_token`, retaining nondeprecated chat entries. Mistral excludes explicit `completion_chat=false`; SiliconFlow uses `sub_type=chat`; Qianfan requires chat type and text output.

Alibaba uses the selected International or Hong Kong authority's `/api/v1/models`, `capabilities=TG`, numbered pages of 100, `success=true`, and exact stable total/page metadata. It rejects incomplete pages and stops only after the total is reached. Baseten uses only `https://api.baseten.co/v1/model_apis?limit=100` and bounded cursor pagination, parsing name, optional display name/context while ignoring invocation URLs, organization and pricing. Empty/repeated/cyclic cursors, duplicate identities including filtered models, malformed known fields, inconsistent totals or capacity limits reject the whole inventory. Absent optional metadata remains unknown; uint64 context limits retain integer precision.

Private verification precedes the public catalogs of Novita (`https://api.novita.ai/openapi/v1/billing/balance/detail`, five bounded decimal strings), DeepInfra (`https://api.deepinfra.com/v1/me`, nonempty bounded UID), Hugging Face (`https://huggingface.co/api/whoami-v2`, bounded identity/type and closed auth type) and Venice (`https://api.venice.ai/api/v1/api_keys/rate_limits`, `data.accessPermitted=true`). These requests use Bearer and discard all identity, financial and optional token fields. False Venice permission is access denial. DeepInfra's listing is the fixed `/v1/models` data-array profile with optional nested context metadata. No private response creates quota, cost, pricing or usage. Remaining additions use their documented authenticated model interfaces. All profiles share existing transport, cancellation, redaction and all-or-nothing publication.

Official model-interface sources for the additions:

- [Google Gemini](https://ai.google.dev/gemini-api/docs/openai)
- [Groq](https://console.groq.com/docs/models)
- [Mistral](https://docs.mistral.ai/api/endpoint/models)
- [Together AI](https://docs.together.ai/reference/models)
- [Fireworks AI](https://docs.fireworks.ai/api-reference/list-models)
- [Perplexity Router](https://docs.perplexity.ai/api-reference/gateway-models-get)
- [Cohere](https://docs.cohere.com/docs/compatibility-api)
- [Cerebras](https://inference-docs.cerebras.ai/api-reference/models/list-models)
- [Nebius Token Factory](https://docs.tokenfactory.nebius.com/api-reference/models/list-models)
- [Novita](https://docs.novita.ai/api-reference/model-apis-llm-list-models)
- [DeepInfra](https://docs.deepinfra.com/api-reference/models/openai-models)
- [Hugging Face Inference Providers](https://huggingface.co/docs/inference-providers/en/tasks/chat-completion)
- [Venice](https://docs.venice.ai/api-reference/endpoint/api_keys/rate_limits)
- [Scaleway](https://www.scaleway.com/en/developers/api/generative-apis/models)
- [Baseten](https://docs.baseten.co/reference/management-api/model-apis/gets-all-model-apis)
- [Moonshot / Kimi — Global](https://platform.kimi.ai/docs/api/list-models)
- [Moonshot / Kimi — China](https://platform.kimi.com/docs/api/list-models)
- [MiniMax — Global](https://platform.minimax.io/docs/api-reference/models/openai/list-models)
- [MiniMax — China](https://platform.minimax.cn/docs/api-reference/models/openai/list-models)
- [SiliconFlow — Global](https://docs.siliconflow.com/cn/api-reference/models/get-model-list)
- [SiliconFlow — China](https://api-docs.siliconflow.cn/docs/api/models-get)
- [Baidu Qianfan](https://cloud.baidu.com/doc/qianfan-api/s/Dmba8k71y)
- [Tencent TokenHub — China](https://cloud.tencent.com/document/product/1823/130078)
- [Tencent TokenHub — International](https://cloud.tencent.com/document/product/1823/130078)
- [Alibaba Model Studio — International](https://help.aliyun.com/en/model-studio/list-models)
- [Alibaba Model Studio — Hong Kong](https://help.aliyun.com/en/model-studio/list-models)

The generated native guidance JSON uses exact-path LF normalization so Windows checkouts preserve the canonical Go-generated bytes. Registry freshness is compared byte-for-byte on every supported test host; line-ending differences cannot hide stale guidance.

## Inline Worker models and endpoint-only completion reservation

ListEndpointModels reuses fixed listing profiles and existing pagination/redaction/outbound/cancellation bounds, without gateway credential-validation requests. Its read neither changes account state nor stores catalogs, resources, events or receipts. Account validation keeps its independent credential-evidence behavior.

Follow the complete [catalog amendment](cmds-delidev-catalog-contract.md#inline-worker-models-and-endpoint-only-completion-reservation) and [current-only reset](cmds-delidev-structure-contract.md#pre-release-compatibility-reset). This reservation changes no runtime support or native/account acceptance.


## Automatic API verification

The joined server maintenance owner validates enabled connected API accounts whose providers are enabled and whose credential removal is absent. Reuse the explicit `ValidateAccount` implementation, protected selected-profile resolver, once-only receipts and atomic observation/health publication. No inference is performed. Custom public model listings cannot establish credential authentication; unsupported remains unverified. Discovery alone never changes authentication health or clears exhaustion.

Missing validation for the current connection is immediately due, including after restart and API format changes. Later validation uses its own persisted completion time plus the greater of 15 minutes and accepted Retry-After. Catalog due time remains independent. When both are due, validation runs first, then the worker rereads the confirmed account revision and original connection before permitted discovery. Accepted failed/unsupported validation does not prevent discovery. Provider discovery disablement prevents catalog publication but does not prevent validation; validation may itself inspect a model endpoint.

Automatic-only admission and publication require enabled account/provider authority. Account or provider disablement cancels the applicable automatic check under the account gate; discovery disablement preserves its catalog-only cancellation. Disconnect, deletion, connection replacement, revision/profile change, authorization and shutdown retain the original inspector fences. Explicit RPC/replay semantics remain unchanged. Keep the four-worker joined pool, eight-inspection bound, one operation per account, two-second ticker, 100-ms scan spacing and 30-second cooldown for unaccepted work. Shutdown joins work before vault/database release. Logs retain safe operation/account/request/correlation identities, closed outcomes and duration only.

CLI account validation and explicit provider discovery use 50-second outer and response-header limits: up to 20 seconds for an optional OAuth refresh, five seconds for independent settlement, 20 seconds for inspection and five seconds for the typed response. Earlier caller cancellation wins. Ordinary provider reads/account connection and every upstream limit remain unchanged; this adds no retry or fallback.

## cmds/delidev-cli/internal/domain constraints

- ProviderPresetID includes the 26 additions allocated in 10–35, preserving old IDs and fixed managed definitions. Preset guidance and advisory catalog metadata cannot grant connection, readiness or inference/harness authority. Follow the catalog/inspection contracts.

- Issue #1961 native Fast diagnostics permit `fast` only for original Codex native input evidence. Keep provider HTTP requested/effective service-tier domains unchanged, unknown-value redaction, immutable execution snapshots and independent applied-setting validation. A diagnostic does not grant entitlement, billing, acceleration or retry authority.

## cmds/delidev-cli/internal/harness/codex constraints

- Recheck the merged managed configuration using the actual execution workspace before both thread start and resume. Startup-directory verification alone cannot authorize workspace-specific provider or authentication overrides.

## cmds/delidev-cli/internal/harness/opencode constraints

- Foreground child observation requires original accepted root input/task message/part/call metadata plus an independently read child `parentID`. Preserve same provider/model, one newly created child level, default depth one and disabled experimental background support. Task reuse, nesting, promotion and unsupported child interactions grant no response or control authority.

- Explicit reasoning effort maps unchanged to the selected model `options.reasoningEffort`. Before input, independently reread and validate complete loaded `/config` and `/provider` profiles and project the applied value from native bytes. Empty selections omit model options and retain native defaults. Bind explicit effort in checkpoint settings digests with omitted empty metadata so historical default digests remain unchanged. Preserve original process, provider relay, instructions and cleanup ownership.

## cmds/delidev-cli/internal/providers constraints

- Per-key API formats follow the catalog/provider contracts and PR #1646's recorded allocation closure. The fixed 35-preset registry declares direct Responses, Chat Completions and Messages profiles; preserve original flat defaults. Resolve the selected account profile through the common server resolver and keep inference URLs separate from canonical non-inference inspection targets. Format availability grants no model, OAuth or native execution authority.

- Follow `cmds-delidev-providers-contract.md`, the activation/catalog contracts and issue #1148. Go owns the canonical 35-entry ordered registry, fixed non-inference inspection targets and protected credential use. Endpoint/protocol/authentication must match the closed official profile; copied names, preset IDs, response URLs and public catalogs cannot grant authentication authority.

- Bound private verification and complete discovery together to the original 20-second/32-page/10,000-entry/4-MiB-per-response/16-MiB-total/32-KiB-header profile. Count filtered identities, reject duplicate identities/cursor cycles and publish no partial catalog. Discard private identity, financial, invocation URL and organization fields; keys never enter URLs or diagnostics.

- `cmd/guidance` generates `apps/delidev/src-tauri/provider-guidance.generated.json` from reconciled static official documentation/key-creation metadata. Regenerate with `go -C cmds/delidev-cli run ./internal/providers/cmd/guidance > apps/delidev/src-tauri/provider-guidance.generated.json` from the repository root. Keep the freshness test and native compiled selector aligned; guidance grants no account, discovery or execution capability.

- Keep generated native guidance byte-identical across hosts through its exact-path LF attribute; regenerate from the registry and preserve the freshness check rather than accepting platform-specific JSON bytes.

## cmds/delidev-cli/internal/server constraints

- Follow `cmds-delidev-providers-contract.md` for non-inference validation. Keep HTTP outside account locks/transactions, cancel on disconnect/revocation, revalidate authority and generation at publication, and replay accepted failed observations without network work. Public/custom model lists do not establish credential validity; unobservable authentication records an unsupported validation state. Never follow redirects, inherit environment proxies, retry provider requests, ingest diagnostic bodies or infer quota recovery/costs from validation.

- Manual PR fixes belong in `pr_fix.go` and the additive authenticated PullRequestFixService. Bind exact original evidence/project selections and final session/link revisions, recheck provider/Worker/native gates outside/inside their owning boundaries, and atomically compose ordinary queue/routing with stable-PR ownership. Explicit Resume cannot bypass PR assignment proof; automatic remediation remains separate. Follow the integration contract.

- Coalesce identical initial manual-fix actor/request selections before provider inspection with bounded cancellable ownership. Recheck durable receipts after waiting; accepted replay bypasses gate capacity, and foreign input/actors cannot share ownership.

- Repeated native history-mode observations must revalidate live execution authority through a read-only path when the retained sticky mode is unchanged; do not create a receipt or wake store watchers for each provider request. Recheck authority and history mode at the commit boundary for actual transitions.

- Manual fix selections must use the retained set's exact local repository resource before policy or profile lookup. Slow provider preflights occupy a separate four-session cancellable dispatch lane, joined at shutdown; ordinary execution retains its five-second claim bound and cannot wait behind remote reads.

- Service-native accounts/models use schema 2 and capability 17 independently of API Providers. Preserve immutable source/service identity in configuration, routing, execution publication, pricing and diagnostics; API configuration stays schema 1. Historical retired reads are explicit owner/client metadata-only projections and cannot authorize mutation, Resume or dispatch. Portable v2 strips subscription ownership; API-only v1 import rejects every native subscription graph atomically.

- Issue #1208 OpenCode child publication validates the original task product message, native message/part/call, current execution root/input, independent native child parent and same immutable provider/model before atomic writes. Only the original task source may establish a new one-level child; original history may refine it. Preserve historical unique parent-tool claims, nullable telemetry and version-1 cleanup gates.

- Codex Fast diagnostics follow issue #1961: project selected `fast` from the original immutable execution configuration and effective `fast` only from its native applied-setting event. Use the source/harness-specific native filter; provider HTTP tiers, current Worker edits, unknown redaction and original receipt/history ownership remain independent.

- Issues #2015/#2016 root Claude web blocks share the original provider-message transaction and strict sessions contract. Independently reject foreign/repeated call/result IDs, local/server namespace reuse, unsupported server families and malformed native projections before publication. Preserve original actor/account/Worker/thread/turn, sequence/receipt atomicity, private content and independent usage/citation/cleanup gates. Public web history remains excluded from unsupported native continuation; no allocation or migration.

## cmds/delidev-cli/internal/store constraints

- OpenCode session mutation transport remains separate from discovery and effective account/provider initialization. Synchronize the exact original request/kind/body digest and native input identities before one HTTP attempt; retain the claim after callback, cancellation or response uncertainty, and never recreate a session or resend input automatically. Preserve native session/message/part identifiers and exact original settings/metadata ownership. HTTP 204 is scheduling only; exact stored-input observation, assistant completion, continuation and native Steer are independent. Missing input is not rejection, confirmed storage cannot be erased, and a contradictory session read must not regain send authority from a later matching read. Keep routes/authority closed, diagnostics private and all unsupported native history/interaction extensions gated until their own adapters.

- Durable PR feedback follows the integration contract. Schema v18 shares stable remote PR ownership across aliases/sessions, retains immutable original versions and separates provider state/current membership from local handling. Publish complete inventories only after current selection/generation/actor and captured set-revision checks; quota failure rolls back all writes. Exact version/revision Dismiss retains audit provenance and invalidates concurrent collectors/pages. Keep owner/client reference-only receipts, signed epoch-bound history, synchronized migration backups and original history after configuration deletion. No persistence or dismissal grants execution authority.

## cmds/delidev-cli/internal/worker constraints

- No harness installation, account failover, TTY scraping, unsupported-feature emulation, unprotected secrets, or raw provider diagnostics.

- Claude typed content observations preserve provider message identity, ordered block indices, partial versus completed content, original tool ownership and native result blocks. The final result text cannot replace a multi-block transcript. Keep thinking signatures private, bound aggregate in-flight content and release validated completed buffers. Preserve both streamed tool proposals and Claude-enriched completed inputs; callback authority must use the actual original callback, never the proposal. Validate all tool results before committing any completion, and do not complete a synchronous parent with active observed children. Main-loop usage is per turn; model ledgers and native cost estimates are cumulative native observations. Preserve null versus zero and exact native decimal spelling without adding overlapping counters, inferring charges or authorizing fallback routing. Remaining rich/child extensions, publication and interaction acceptance require separate adapters.

- The API relay may preserve exactly `beta=true` for its authorized Anthropic Messages/count-tokens operations. Reject every other query spelling, duplicate/additional parameter and all queries on other protocols before acquiring provider authority. This exception cannot change the immutable account/model/operation scope or introduce retries. Accept dual native credential headers only as one exact equal valid token on Anthropic operations; duplicate or differing values and dual headers on other protocols fail before authority acquisition. Reject request-selected `fallbacks` chains, including null/empty values and case aliases, before provider-key access or upstream transmission. Inject only the server-owned upstream key.

- Codex thread control is an explicit private protocol mode, separate from discovery probes. Persist request/settings before sending, pin validated history and provider fallback behavior, preserve base instructions, and compare effective native settings before accepting a binding. Serialize root-thread control, retain original inspection authority after a foreign response, and block new mutations on uncertain or mismatched acceptance. Native thread creation alone does not prove persisted history or account/model execution readiness. Opt-in native tests use an explicitly selected executable, private environment and loopback scripted provider only; ordinary tests never invoke installed harnesses.

- Provider error bodies, including HTTP-200 error envelopes, must pass secret reflection checks even when their native machine codes are allowlisted; retain failure status without reflecting a colliding protected value.

- Claude provider server tools retain separate original call/result ownership and share bounded identities/open work with local tools. Server results cannot satisfy local callbacks or tool results; changed, duplicate, foreign-parent/provider or mixed partial snapshots cannot commit ownership. Pending server child work blocks parent closure. Preserve tool errors, subprocess codes and advisor truncation independently of native turn outcome, keep encrypted/document/diagnostic payloads private, and never fabricate unforwarded child events from parent text or native history. Provider files/edits are not local resources, and tool-search references cannot modify installed capabilities.

- Claude programmatic callers require an original unfinished code-execution parent in the same native Agent/provider lineage. Retain caller metadata through native completion, original local callbacks and results without substituting Agent ancestry or granting permissions. Parent completion must join observed children. Only the original validated pause/tool-use stop can carry unfinished server ownership to the next streamed provider message, after observed local descendants settle; preserve the original call message and prohibit reuse across input/automatic-turn/process boundaries. Native container omission and pause failure remain explicit capability limitations, never emulated completion, automatic retry or hosted-provider acceptance. Child history and nonstreamed cross-message reconciliation require their own original evidence.

- Claude inline Read, synchronous Bash and Write/Edit replacement requires original input/turn/provider/block/tool/result ownership and exact input/structured-result digests, joined to the original main transcript. Preserve restored provider/tool registries when initializing fresh stream state; an empty retained tool list must still reject observed tool calls. Retain no paths, commands or output contents in checkpoints, never resolve result locators, and keep errors, media, persisted-output references, background work, child and programmatic tools outside this proved profile. Tool approvals require original input/turn/tool/request/arrival ownership, separate request/reply digests, exact native echo and independent successful inline result. A checkpoint cannot reconstruct approvals from conversation files or replay historical replies; denied/canceled, question and Plan callbacks retain their separate gates.

- OpenCode original retry notifications retain only bounded typed attempt/deadline facts and original event/assistant ownership; private provider prose cannot enter diagnostics or publication. Reject unowned/finalized sources and provider action payloads. A live Stop may claim an already observed current backoff, but an intervening native busy state clears that backoff authority; earlier retries cannot justify a later missing finish/error. Confirm canceled-backoff completion only through the original HTTP acknowledgment, exact history and owned cleanup. Native retry observations never authorize DeliDev input replay, another account/model, provider translation or replacement execution.

- The private OpenCode API session initializer owns a fresh authenticated process and fixed paired-server relay path, validates scoped-token format without claiming registration, and returns only after exact configuration/provider/path/selected-primary-agent checks. Reject noncanonical or overlapping runtime/workspace roots, unsupported agents and present/uninspectable OS managed policy; never read or override administrator content. Preserve Windows ProgramData solely as the native managed-policy lookup context. Disable inherited credentials, project/external configuration, automatic native downloads and plugins using the pinned private profile; this does not establish an OS sandbox or credential-free tool children. Keep startup and lifetime cancellation separate, fail immediately on any extra stdout bytes after readiness, and join original handle/journal cleanup after initialization failure without deleting state. The inspected native root remains immutable observer comparison authority; public Worker/account integration and other agent/capability profiles remain separate.

- Public OpenCode first dispatch shares the atomic snapshot/routing/input/job transaction with Codex while preserving distinct native versions, protocols and provider API families. Validate every explicit option before claiming; recheck current Worker, account validation/connection/authentication, model, Agent/project policy and prepared workspace. Unsupported continuation profiles, roots or settings leave input/routing unconsumed. Discovery alone never grants account readiness, and actual Worker initialization must independently verify effective native settings. Version-1 OpenCode completion remains paused without checkpoint-backed continuation authority.

- Claude text publication stores each original provider message as one indexed message with ordered native text/thinking/redacted blocks. Preserve block completion before block-stop and separate provider message-stop; never concatenate separate blocks, expose opaque thinking signatures, infer session outcome from message closure or synthesize hidden reasoning. Bound aggregate text and block counts, reject unsupported rich or unowned child observations before terminal authority, and keep usage as a separate native observation. Exact outbox retries cannot repeat a delta or original input.

- Claude root tools retain original provider block references and separate tool-message payloads. Commit proposal mutations with their provider mutation atomically, preserve original assembled and native applied JSON bytes, omitted/direct caller and independent result/error evidence. Validate whole native result batches before publication and replay only original durable receipts. Native string metadata requires explicit native error evidence. Neither proposal nor result grants approval, terminal outcome, continuation or public dispatch; rich/child ownership remains separate.

- Failed Claude root Read restoration requires independently retained explicit native error, inline text, original JSON-string metadata digest and exact provider/call/result/history ownership. Preserve optional error evidence without changing legacy successful checkpoint bytes; no other tool family may inherit this profile. Tool failure stays independent of successful root completion, new-input intent and owned process cleanup.

- Claude callback publication binds original transport arrival and native request IDs to the original stored root tool/provider/index/applied input. Preserve question text keys, explicit multiple-selection flags, native-enriched plans and nullable metadata/suggestions without applying them. Requests/index/inbox/sequence commit atomically; reject overlapping unanswered callbacks on one tool and replay only exact receipts. Native cancellation retains original request content and cannot prove reply acceptance or root outcome. Claude responses use the dedicated original response adapter below; other harness response shapes cannot acquire their authority.

- Original completed root Read success/error can join Claude continuation only after acknowledged exact proposal/result, no non-execution classification or callback, complete provider ownership and private native history verification. Retain only candidate eligibility after releasing acknowledged Worker tool payloads. A Read error is independent from the root outcome, and later file changes must never replace the originally observed result. Other tool/media/child profiles need their own evidence.

- Original Claude tool results may precede provider message-stop only after their exact original proposal block stopped. Independently validate provider/index/tool ownership and proposal/result identity in Worker and server; never complete the provider or task from a result. Reject incomplete/foreign blocks and validate a complete native result batch before any publication. Retain original receipt replay and separate callback/terminal/cleanup gates, with closed-stage diagnostics only.

- Issues #2015/#2016 native Claude web publication preserves original accepted input/account/Worker/provider ownership, exact streaming-input/static-result comparisons and once-only receipts under the sessions/harness contracts. Release acknowledged display payloads; never publish encrypted/native binary bytes or run an independent fetch. A settled server-tool history retains its version-1 completion under the original private checkpoint exclusion, rather than attempting unsupported replacement or declaring publication failure. Keep independent cleanup and native code-execution/programmatic/child gates.
