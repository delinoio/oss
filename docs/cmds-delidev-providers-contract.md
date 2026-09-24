# DeliDev provider inspection

## Ownership and scope

The server owns non-inference API checks in `cmds/delidev-cli/internal/providers`, exposed through `AccountService.ValidateAccount` and `account validate --id ID --revision N`. The complete [issue #964 requirements](cmds-delidev-requirements.md) remain normative. This increment implements bounded model-list inspection and credential evidence, not automatic catalog publication, provider preset setup, quota refresh, subscription authentication, API proxy execution or selected-model/harness validation. Those remaining interfaces must use the same explicit authority and secret boundaries.

Only owner/paired-client RPCs can invoke validation. The key is read from the current immutable connection's protected server reference, used locally for the request, and cleared after use. It is never returned or forwarded to a Worker. Keyless endpoints remain loopback-only; localhost always means the server machine. Explicit account validation may inspect the models endpoint even when automatic discovery is disabled, but never registers models or changes a session configuration.

## HTTP boundary

Inspection uses only `GET` requests under the saved provider's API base path. OpenAI Chat Completions/Responses-compatible providers use `/models`; Anthropic Messages-compatible providers use `/models?limit=1000` and bounded `after_id` pagination. It sends the configured bearer or `x-api-key` authentication, adds the Anthropic API version where applicable, and fixes `HTTP-Referer: https://deli.dev`. It accepts no caller-supplied headers or destination overrides. Configuration rejects credentials, queries (including an empty query marker), fragments, encoded path components, backslashes and traversal segments in base URLs.

HTTPS verifies the system trust roots and hostname; plaintext is allowed only on explicit loopback. The literal `localhost` is dialed through loopback IPs instead of an external resolver. Requests do not inherit environment proxy settings or cookies. Explicit protected server proxy configuration is still required future work; the current direct transport must not claim to apply unsupported proxy settings.

The entire inspection has a 20-second context deadline, with bounded dial, TLS handshake and response-header waits. Response headers are limited to 32 KiB. Successful JSON bodies are limited to 4 MiB each and model pages to 4 MiB in aggregate, 32 pages and 10,000 models. Connections are not reused, avoiding transport retries on a previously used connection. No HTTP redirect, provider retry, protocol translation, inference or account/model fallback occurs.

Provider JSON may contain future fields, but known fields use exact names. Invalid UTF-8, duplicate keys, extra documents, excessive nesting, malformed model identity, duplicate identities and incomplete/looping pagination fail the whole inspection. Model identifiers and advisory display names are bounded; native IDs remain exact and are sorted only for stable output. Reflected raw/Base64 key strings in retained fields are rejected. Error bodies, arbitrary diagnostic headers, redirect locations, account labels and provider request IDs are discarded. Only typed failures, HTTP status and a parsed bounded Retry-After value leave the HTTP layer. The retry delay is advisory; DeliDev never retries automatically.

## Authentication evidence

Endpoint response and credential evidence are independent:

- Exact official OpenAI, Anthropic, xAI and DeepSeek authorities with their documented API paths/authentication use the authenticated models interface as non-inference credential evidence.
- Exact OpenRouter and Vercel AI Gateway authorities validate their key/credits interface before reading the public model catalog. The response's account labels, monetary values and usage totals are discarded; they do not become DeliDev usage, cost or quota observations.
- An explicitly keyless local endpoint records `keyless-endpoint` after a valid catalog response.
- Other compatible/custom endpoints record authentication `unknown` even when their catalog responds. A public listing cannot prove that a configured key authorizes execution. Current `account validate` preserves that successful endpoint observation but returns a typed unsupported credential-validation result and leaves health unverified. Completing custom authenticated execution support remains required work; it cannot be claimed from this inspector alone.

Known authenticated and keyless success can set account health ready. This is neither proof that every listed model is allowed for inference nor a grant of native harness execution capabilities. A 401 records rejected credentials without inventing an expired/revoked distinction; a 403 records denied access; a 429 records request limiting without asserting quota exhaustion. Unsupported, malformed, TLS, timeout and network failures remain distinct. No validation result clears existing exhaustion or quota evidence, and a passed reset timestamp is never interpreted as recovery.

## Durable account result

Validation requires the current account revision and connection with no pending removal. Authorization, connection generation and provider authority are checked before key access and again at publication. A server-wide limit of eight checks and one check per account bounds outstanding work. HTTP never holds the account gate or a SQLite transaction, so slow providers do not block unrelated account checks, metadata work or disconnection.

Disconnection cancels that account's outstanding check before credential cleanup; client revocation and server shutdown cancel its request context. A canceled or stale generation cannot publish readiness. The accepted observation, health, account revision, event and request receipt commit atomically. Completed request replay returns the original observation and current account metadata without network access, including after restart or later disconnection. A new observation requires a new request identity/current revision. Accepted failed observations are also replayed without repeating network calls.

Account metadata contains the connection-bound `validation` observation: request identity, timestamp, state, authentication evidence, model count, bounded status/retry metadata and sanitized problem. General configuration cannot forge or remove it. Disconnect clears the current observation. `ValidateAccount` returns both the current account and the accepted observation; CLI output retains both when a validation problem produces a nonzero typed exit code.

Logs record account/request/correlation identities, phase, state, authentication evidence, failure classification and duration. They exclude keys, raw responses, endpoints and model content. Models are not currently persisted by this operation; automatic discovery must separately preserve manual registrations, display preferences and canonical identity when integrated.

## Verification and references

Real loopback HTTP tests cover headers, direct transport, redirects, error redaction, TLS verification, cancellation, response bounds, exact JSON fields, pagination and malformed input. Real Connect/SQLite tests cover readiness, unsupported custom credential evidence, failure receipts, restart/replay, disconnect cancellation and independent account progress. The disposable Linux Secret Service CLI test additionally retrieves its native protected key, sends it only to an in-container loopback fixture, preserves the unsupported authentication observation and replays without another request. These are fixture/native composition results, not real provider-account acceptance.

Official interface sources checked for this implementation:

- [OpenAI model listing](https://developers.openai.com/api/reference/resources/models/methods/list)
- [Anthropic model listing](https://platform.claude.com/docs/en/api/models/list)
- [OpenRouter current API key](https://openrouter.ai/docs/api/api-reference/api-keys/get-current-api-key)
- [Vercel model listing](https://vercel.com/docs/ai-gateway/sdks-and-apis/openai-chat-completions/rest-api) and [official SDK credit check](https://github.com/vercel/ai/blob/main/packages/gateway/src/gateway-fetch-metadata.ts)
- [xAI inference API authentication](https://docs.x.ai/developers/rest-api-reference/inference)
- [DeepSeek model listing](https://api-docs.deepseek.com/api/list-models/)
- [Ollama OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility), [LM Studio model listing](https://lmstudio.ai/docs/developer/openai-compat/models), and [vLLM online serving](https://docs.vllm.ai/en/latest/serving/online_serving/)
