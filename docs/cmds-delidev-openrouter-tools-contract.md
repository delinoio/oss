# DeliDev Codex OpenRouter native tools

## Scope

The dedicated adapter supplies Codex image generation/editing and standalone
web search to an original root API execution on the exact account-selected
OpenRouter Responses profile. It does not change the conversation model or
account. Other providers, subscription executions, title jobs, Sidechat and
native review scopes do not acquire these operations.

## Interfaces and authority

System capability 108 and Worker capability 73 negotiate
`CODEX_OPENROUTER_TOOLS_V1`. The server freezes `native_openrouter_tools` only
with the existing native-image and dynamic-tool capabilities, Codex root API
configuration and exact `https://openrouter.ai/api/v1` Responses/Bearer profile.
The original immutable assignment carries the selection. Older peers keep
their original operation set. Source reuse and credential acquisition recheck
the original capability and account authority; no migration is added.

The execution token authorizes three additional fixed private POST paths:
`/api-proxy/v1/images/generations`, `/api-proxy/v1/images/edits` and
`/api-proxy/v1/alpha/search`. Original cancellation, account revocation, protected
credential access, reflection checks, private forwarding and process joins
remain mandatory. The private native forwarder admits these paths only for a
selected tool execution. No caller-selected URL or model fallback is accepted.

Codex reserves its built-in `image_gen` namespace for managed authentication.
New selected API threads register the native dynamic tool
`delidev_image.imagegen`; native Resume retains that original specification.
Older threads do not acquire a new backend during continuation. The dynamic
responder verifies the selected original namespace, call, turn and request,
commits the existing durable claim before backend I/O, and returns one native
`inputImage` only after validating actual original PNG bytes. Failures return
`success: false` with a generic error. Unknown tools retain the unavailable
response. No actor authentication or OpenAI provider identity is fabricated.

The adapter maps the installed Codex image tool's `gpt-image-2` to the exact
`openai/gpt-image-2` tool model at OpenRouter's `/images` endpoint. It requests
one PNG, preserves supported prompt/background/quality/dimensions and maps
native inline edit images to `input_references`. Remote image URLs, file IDs,
alternate models and routing fields are rejected before key access. Original
inline bytes must pass existing still-image validation. The response must
contain exactly one valid original PNG; the adapter does not download, resize,
convert or synthesize image bytes or background metadata.

Standalone search uses one nonstreaming Responses request on the original
conversation model/account with OpenRouter's search/fetch server tools and
required tool selection. The adapter carries the original bounded commands
and recent context as input. It returns original output text to the native
search client only after a completed `web_search_call`, `openrouter:web_search`
or `openrouter:web_fetch` output proves execution. Assistant-only text,
unexecuted function suggestions and failed or pending web calls are rejected.
No native reference IDs or encrypted output are invented. Serialized tool
requests must still fit the proxy body bound after context/string escaping;
oversized projections are rejected before credential access or upstream I/O.
This is a distinct tool request; the ordinary Responses relay does not inject
tools or rewrite user requests. Remote failures remain failures and are not
converted to search success. Incompatible observed tool responses return a
non-retryable adapter error so native HTTP retry policy cannot repeat a tool
operation that has already produced a response. Rejection logs retain only
closed response status, incomplete reason and item counts.

## Results and lifecycle

Subscription image items and API dynamic image items retain their distinct
native identities. API image completion uses the dynamic-tool Worker outbox,
server transcript and original completion/cleanup barriers; public media are
inert commitments under that existing contract. It does not fabricate a native
built-in image event or an authenticated attachment preview. Original PNG
results remain private in native history and a thread-owned cache within the
persistent native profile, which follows profile deletion ownership. Recent
image edits use original generated PNGs from that thread's cache; a different
thread or Fork cannot adopt that cache. Existing local image references use
original still-image bytes through a held effective read-root handle. Restricted
reads remain beneath that handle if a path component is replaced during the
operation; symlinks cannot introduce authority outside the admitted root. Native generated
image previews and references from inherited Fork images remain unimplemented
for the API adapter. Retained history uses the explicit enlarged frame profile.
The selected native image transport also admits its existing 16-MiB frame bound
for the original dynamic image reply. Ordinary tool replies, approvals and
questions retain the one-MiB response limit; another connection or method
cannot invoke the image producer through this route.
Tool completion is independent of root completion and credential, process and
workspace cleanup.

Logs contain closed operation, stage, status, correlation and duration fields.
They exclude API keys, original commands/context, prompts, native paths and
image bytes. Dedicated HTTP logs identify each tool operation. These operations do not
enter conversation inference diagnostics or manufacture native token usage
or pricing evidence.

## Validation

Test exact profile/operation admission, older peers, wrong models, alternate
routes, malformed/oversized input, original inline-image identity, PNG output,
provider errors, secret reflection, cancellation, duplicate publication and
original account revocation. Separate scripted HTTP/native fixtures from real
account and platform acceptance. The real acceptance prompt is
`너가 쓸 수 있는 툴들 다 한번씩 써봐`; uncalled tools remain unverified.

## References

- [Native image requests](https://github.com/openai/codex/blob/rust-v0.159.2/codex-rs/codex-api/src/images.rs)
- [Native search requests](https://github.com/openai/codex/blob/rust-v0.159.2/codex-rs/codex-api/src/search.rs)
- [OpenRouter image endpoint](https://github.com/OpenRouterTeam/typescript-sdk/blob/main/src/funcs/imagesGenerate.ts)
- [OpenRouter image request](https://github.com/OpenRouterTeam/typescript-sdk/blob/main/src/models/imagegenerationrequest.ts)
- [OpenRouter server tools](https://openrouter.ai/docs/guides/features/server-tools)

Search output shapes follow the [OpenRouter Responses web search](https://github.com/OpenRouterTeam/typescript-sdk/blob/main/src/models/outputwebsearchservertoolitem.ts)
and [web fetch](https://github.com/OpenRouterTeam/typescript-sdk/blob/main/src/models/outputwebfetchservertoolitem.ts) schemas.
