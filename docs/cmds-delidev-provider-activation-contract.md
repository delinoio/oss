# DeliDev API provider activation

## Scope and ownership

Issue #1046 adds server-owned activation for API providers. Go owns provider identity, availability, model-filter semantics and execution admission. The Connect API, CLI and desktop are clients of that authority. This feature does not own the Settings shell geometry (#1045) or account-menu/two-step connection flow; those surfaces consume this provider state.

## Provider identity and compatibility

`domain.Provider` has optional `enabled` and `preset_id`. Missing `enabled` in stored/portable legacy data means enabled. New custom providers default enabled. An update that omits the field preserves the exact stored value, including false. Null, malformed values and unknown preset IDs are invalid. Existing providers without `preset_id` remain custom with their exact identity, endpoint, discovery setting and references; names or URLs never infer provenance.

The closed preset IDs are `vercel-ai-gateway`, `openrouter`, `openai`, `anthropic`, `xai`, `deepseek`, `ollama`, `lm-studio` and `vllm`. Inventory includes all nine as virtual Off entries before activation. First activation saves the canonical server-defined provider once; later Off/On mutations retain its UUID. A preset is an API provider, not native subscription configuration. Saved managed presets must match server-pinned name, endpoint, API protocol and discovery defaults. Generic writes and imports cannot forge or edit managed authority. Create custom copy starts a separate provider with a new UUID and no preset ID, account, credential, or model copies; its API fields are then editable.

The legacy `ListProviderPresets` JSON preserves the top-level preset ID and editable provider defaults but omits nested `provider.preset_id`, so older desktop clients can continue editing or copying those values. Activation provenance comes from provider inventory and is attached to the provider only when the activation mutation is sent.

The store enforces one saved provider for each nonempty preset ID with a durable unique index and a transactionally translated typed conflict. Concurrent first activation elects one request. Mutation receipts retain ordinary actor/request/revision semantics; exact replay is observational and cannot reapply a prior toggle. Do not backfill unrelated documents just to make defaults explicit.

## Provider inventory and account scope

`ProviderService.ListProviderInventory` is an owner/client-only bounded read. Its closed capability enum reports provider activation, active-provider model filtering and account-provider filtering. Each entry has preset identity, an optional saved resource/UUID, effective enabled state, exact total and connected account counts, and an explicit `account_counts_available` flag. Virtual presets never receive invented resource IDs. Connected counts use current account connection state and pending credential-removal state, not health, model readiness or the currently loaded account page. Failure or absence is unavailable, never zero.

Inventory supports search, enabled-only filtering and signed bounded pagination. Filters and relevant provider/account event state are bound to the cursor. Presets are stable and precede saved custom providers; custom providers remain paged. Workers cannot read this owner inventory. A desktop enables provider/model workflows only after a valid inventory response includes all three required capabilities. Missing, unknown or malformed capabilities show update-required/unavailable; callers never substitute a generic provider list or unfiltered model read.

`ListResourcesRequest.provider_id` is list-only and valid only with account kind. It composes with `session_id` and `project_id` filters, and all three filters are included in pagination scope. `GetSnapshot` and `WatchEvents` keep their existing scope and behavior. CLI `account list --provider-id` uses the same contract; snapshots and non-account lists reject the flag.

## Catalog and selection

`SearchModels.enabled_providers_only` is additive and omitted/false preserves legacy search. When true, SQL filters to enabled API providers before pagination; the filter is cursor-bound. Disabling a provider expires affected catalog pages. Hidden models do not bypass provider filtering. Model rows are grouped by provider. New provider/model selection and model discovery use active API providers only. Canonical resolution and historical records remain available so existing disabled references can still be rendered and edited without silently replacing them.

The API Providers screen groups the six hosted presets, three local API presets, and custom providers; it supports search and bounded custom pagination. Each row shows a named On/Off control, truthful account counts/state, and provider-scoped Manage accounts or Add account. Off is independent of disconnect and does not hide retained account status. Enabled providers without a connected account show Account required. Model screens preserve manual registration, NEW acknowledgement, hidden state, custom order, native/display identity, aliases, harness compatibility and token pricing. Catalog membership or activation alone never proves inference/harness compatibility.

Selectors preserve an existing off-provider reference and label it Off provider without making it a new choice. Edits to unrelated fields may retain that reference. Provider and model selectors use the validated capability-backed inventory/catalog queries; no generic unfiltered configuration query is an eligibility fallback. Distinguish no enabled providers, no connected accounts, no models and no search matches. Loading, permission, stale, saving, conflict and unknown mutation outcomes are not empty success.

## Discovery and execution admission

Turning a provider Off blocks new model discovery and new execution admission, but does not disconnect accounts, delete keys/models, cancel work, reset routing, rewrite references, change pause state or fail over to another provider. Explicit account validation and credential management remain separate. Turning Off cancels only in-flight catalog work and prevents stale publication while preserving prior model data, manual entries, preferences and discovery timestamps. Re-enabling follows the existing due/backoff policy and does not force refresh or resume paused sessions.

The server checks effective provider availability before initial selection/claim, before continuation and Resume (including empty Resume), and again inside the first durable execution-grant transaction. A disabled-provider result is a sanitized typed failure with enable/select guidance. Queue acceptance is not execution authority: denial preserves queued inputs and must not consume them, create replacement routing or select a fallback.

Transaction order defines the disable/grant race. A grant committed before disable may finish only its original turn, including original questions/approvals and valid same-turn Steer. If disable commits first, queued or claimed work cannot obtain a fresh grant. A successor turn always needs a fresh check and grant. Exact replay of an existing grant remains observational and retains all current owner/device/Worker/epoch/Stop/recovery checks. Do not put provider-enabled revocation into shared relay/scope checks, original interaction replies, or already-authorized same-turn controls. This preserves current turns without broadening new-turn authority.

Provider-state and admission-denial logs use structured metadata only (operation, UUIDs/revision, enabled state and stable reason). Never log provider names, endpoint, key, prompt, model text or request payload.

## Interfaces, transfer and rollout

Proto additions are backward-compatible: provider inventory request/response and capability enums, list-only account `provider_id`, and model `enabled_providers_only`. Regenerate Go and TypeScript/Connect Query bindings from `protos/delidev/v1/delidev.proto`; handwritten generated output is not allowed. Provider inventory and account/model filters have CLI equivalents. Ordinary `provider create/edit --input` supports enabled. `provider create --preset ID` creates the managed provider; `provider create --preset ID --name NAME` explicitly creates a custom copy with a fresh identity and no preset provenance.

Portable export/import carries optional activation and preset identity. Missing values retain legacy semantics. Imports reject duplicate preset identity within the bundle or against target state atomically, including a recheck at deferred apply. Existing explicit equal-configuration reuse remains valid; imports never merge by name/URL or import connection/readiness evidence. New choices and mutations recheck enabled state at the authoritative server boundary.

The feature does not add feature flags. Older same-version servers that ignore additive filters are detected through the required validated inventory capabilities before the desktop enables workflows. Keep normal exact request/revision, conflict, uncertain-response and current-state retry behavior.

## Validation and evidence

Tests cover legacy omitted-field reads/updates, explicit false preservation, preset integrity, custom-copy identity, concurrent unique activation, inventory ordering/counts/cursors/capabilities/Worker denial, filtered model pagination, provider-scoped account cursors, import collision and deferred recheck, discovery cancellation, and grant-before/after-disable behavior including Resume and replay. Protocol lint/breaking/freshness and Go race/vet checks are required. Frontend tests cover capability gating, truthful counts, named switches, pagination, custom copy and active-only models.

Automated fixtures do not establish real desktop layout/keyboard, real provider account or inference acceptance. Record native/manual evidence separately in `cmds-delidev-evidence.md`; do not claim provider readiness from a UI or protocol fixture.

## References

- [Project index](project-delidev.md)
- [Provider and model catalog](cmds-delidev-catalog-contract.md)
- [Provider activation and discovery](cmds-delidev-providers-contract.md)
- [Account lifecycle](cmds-delidev-accounts-contract.md)
- [Session admission](cmds-delidev-sessions-contract.md)
- [Native API relay](cmds-delidev-proxy-contract.md)
- [Portable configuration](cmds-delidev-configuration-transfer-contract.md)
- [Desktop client](apps-delidev-desktop-contract.md)
- [Connect protocol](protos-delidev-v1-contract.md)
- [TypeScript client](packages-delidev-api-client-contract.md)
