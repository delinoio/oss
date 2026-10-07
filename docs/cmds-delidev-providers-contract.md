# DeliDev provider inspection

## Ownership and scope

The server owns non-inference API checks in `cmds/delidev-cli/internal/providers`, exposed through `AccountService.ValidateAccount` and `account validate --id ID --revision N`. The complete [issue #964 requirements](cmds-delidev-requirements.md) remain normative. This inspector implements bounded model-list inspection and credential evidence. Automatic catalog publication and provider presets are integrated through the separate [catalog contract](cmds-delidev-catalog-contract.md). Quota refresh, subscription authentication, API proxy execution and selected-model/harness validation remain required work and must preserve the same explicit authority and secret boundaries.

Only owner/paired-client RPCs can invoke validation. The key is read from the current immutable connection's protected server reference, used locally for the request, and cleared after use. It is never returned or forwarded to a Worker. Keyless endpoints remain loopback-only; localhost always means the server machine. Explicit account validation may inspect the models endpoint even when automatic discovery is disabled, but never registers models or changes a session configuration.

## HTTP boundary

Inspection uses only `GET` requests under the saved provider's API base path or the exact documented native/private endpoints below. OpenAI Chat Completions/Responses-compatible providers use `/models`; Anthropic Messages-compatible providers use `/models?limit=1000` and bounded `after_id` pagination. The exact OpenRouter profile uses explicit `limit=500`, numeric `offset` and `output_modalities=all`; it validates `total_count` when present and accepts older count-less responses only through a bounded short-page walk. It never follows response pagination URLs. It sends the configured bearer or `x-api-key` authentication, adds the Anthropic API version where applicable, and fixes `HTTP-Referer: https://deli.dev`. It accepts no caller-supplied headers or destination overrides. Configuration rejects credentials, queries (including an empty query marker), fragments, encoded path components, backslashes and traversal segments in base URLs.

HTTPS verifies the system trust roots and hostname; plaintext is allowed only on explicit loopback. Direct and exact-bypass routing dial literal `localhost` through actual loopback IPs instead of an external resolver. Explicit proxy routing retains the original destination authority. Plaintext loopback provider requests require Direct or an explicit matching bypass and are rejected before connection otherwise; they never expose an account key through a proxy tunnel or silently change routes. Verified HTTPS destinations may use the selected proxy. Requests do not inherit environment proxy settings or cookies. Production inspection now applies the explicit selected server profile through the [outbound networking contract](cmds-delidev-network-contract.md), including catalog and credential checks. Worker selections cannot affect it, and failures never fall back.

The entire inspection has a 20-second context deadline, with bounded dial, TLS handshake and response-header waits. Response headers are limited to 32 KiB. Successful JSON bodies are limited to 4 MiB each and private checks plus model pages to 16 MiB in aggregate, 32 pages and 10,000 models. Connections are not reused, avoiding transport retries on a previously used connection. No HTTP redirect, provider retry, protocol translation, inference or account/model fallback occurs.

Provider JSON may contain future fields, but known fields use exact names. Invalid UTF-8, duplicate keys, extra documents, excessive nesting, malformed model identity, duplicate identities and incomplete/looping pagination fail the whole inspection. Model identifiers and advisory display names are bounded; native IDs remain exact and are sorted only for stable output. Optional context limits, input/output modalities and supported tool/reasoning parameter evidence remain advisory. Missing/null fields remain unknown; an explicit supported-parameter list can establish an advisory false. Invalid known metadata rejects the complete response. Reflected raw/Base64 key strings in retained fields are rejected. Error bodies, arbitrary diagnostic headers, redirect locations, account labels and provider request IDs are discarded. Only typed failures, HTTP status and a parsed bounded Retry-After value leave the HTTP layer. The inspector never retries a request. The separate periodic catalog task uses an accepted retry delay as a minimum interval for its next new observation.

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

Logs record account/request/correlation identities, phase, state, authentication evidence, failure classification and duration. They exclude keys, raw responses, endpoints and model content. Account validation never persists models. The separate catalog publication operation preserves manual registrations, display preferences and canonical identity as specified in its contract.

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
