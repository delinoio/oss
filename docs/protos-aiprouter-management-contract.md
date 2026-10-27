# AIPRouter management protocol contract

## Scope

Owns `protos/aiprouter/v1`: versioned Protobuf schemas and committed generated Go bindings.

## Runtime and Language

Protobuf with Go/Connect bindings in root module `github.com/delinoio/oss`. Generated TypeScript belongs to `packages/aiprouter-client`.

## Users and Operators

Server, console, and CLI connection-flow consumers.

## Interfaces and Contracts

Define versioned Connect RPC contracts for organization, member, key, provider, usage, and operator management, plus the CLI/browser connection flow. `organization.proto`, `provider.proto`, `usage.proto`, `operator.proto`, and `device.proto` own domain messages and methods. `common.proto` declares shared messages and method authorization scopes; runtime handlers enforce those scopes. Keep public inference wire protocols under the service compatibility contract. A stable public management API for external automation is excluded from the first release. Inference keys do not imply management permissions. New persisted entities use UUID v7; preserve externally required protocol ID shapes.

### Service boundaries

- `AccountService`, `OrganizationService`, `MembershipService`, `InvitationService`, and `OwnershipService` own identity and organization lifecycle.
- `KeyService`, `ProviderService`, `PoolService`, and `RoutingService` manage personal resources within a selected organization.
- `HostedService` owns personal helper selections and loop limits in `hosted.proto`; helper operations retain the original admission and send budget.
- `UsageService` and `AuditService` expose authorized metadata only.
- `OperatorService` exposes instance settings and aggregate health without organization inspection parameters.
- `DeviceAuthorizationService` binds CLI polling, confirmation, and compensation to a device secret. `DeviceApprovalService` requires a browser session and explicit approval.
- `ClientService` grants only model discovery and self-revocation for the authenticated inference key.

RPC paths use `/aiprouter.v1.<Service>/<Method>`. Personal requests select an organization; the authenticated session determines the user. Optional token and price fields distinguish unknown values from known zero. Decimal costs use strings. Never add prompts, responses, or stored secret values to metadata messages.

Provider configuration carries typed custom wires, model overrides, endpoint paths, and presence-aware policy overrides. An absent boolean inherits its default; explicit `false` overrides it. Custom header values are write-only, with explicit replacement/reset semantics. Operator destination rules preserve stable identifiers and canonical origins. Empty CIDR lists approve the exact origin under the service policy; nonempty lists restrict all resolved addresses for that origin and cannot fall back to default public-HTTPS approval.

Vertex configuration preserves optional `vertex_project` and `vertex_location` strings through generated clients, editing, and persistence. Apply these fields only to the selected personal Vertex connection. An explicit `auto` project is supported only for API-key project discovery under the compatibility contract; service-account credentials must reject it. Never use an omitted project or location to import the service host's identity. Project discovery must retain the original send budget and current credential authorization.

`ProviderPolicyOverrides.responses_item_id_repair` is an optional object, not a boolean. Its `message` and `reasoning` arrays contain exact identifiers; `repair_invalid_ids` and `repair_missing_terminal_ids` retain optional boolean presence. Omitting the object inherits the preset. A present empty object overrides the preset with no repair. Preserve this distinction through generated clients, settings persistence, UI editing, and request-policy resolution.

### Routing and native pool option presence

Routes carry an optional `ReasoningRetentionConfig` with `enabled`, optional `max_context_percent`, and optional `max_tokens`. Retention defaults to off. Absent or zero limits select 20% and 100,000 tokens respectively. A nonzero percentage must be finite, greater than zero, and at most 100; a nonzero token limit must be positive and fit the unsigned 32-bit field. Validate configured limits even when retention is disabled. Route updates replace this configuration in full; omission clears it. The context window comes only from authenticated model metadata and is not a management input. Apply the temporary storage and missing-state rules in the service contract.

JEV decisions retain the selected personal decision connection or ordinary route, timeout, and optional session pin lifetime. Route updates replace the complete configuration; omitting the decision configuration clears it, and non-JEV routes must not retain it. Quota signals are opt-in advisory data, not authorization or a replacement for current eligibility checks. Thresholds measure used quota in percent and must be finite within 0–100. The effective limited and nearly-exhausted defaults are 70 and 90; require limited < nearly exhausted. Moderate is optional and, when present, must be less than limited. An absent moderate threshold differs from explicit zero. Validate configured thresholds even when quota signals are disabled. Missing quota remains unknown.

Fast policy retains optional `fast_enabled` and case-preserving `model_supports_service_tier` entries, including explicit false values. Reject duplicate model keys that differ only by case. An omitted override inherits policy; configuration alone must not advertise unsupported fast capability. Model discovery and execution must agree on the authorized fast selector.

Native pool options apply to homogeneous personal OAuth pools for `openai`, `anthropic`, or `anthropic2`; keep the two Anthropic families independent. Explicit native pool creation materializes `enabled=true` when absent and honors explicit false. Imported configuration with absent enablement remains disabled. Generic resolution must not infer enablement from absence. Pool updates use full replacement: omitted native options clear them, and omitted enablement does not reuse the creation default.

Preserve optional native pool and member values through generated clients, persistence, and console editing. Native strategies are quota, round-robin, fill-first, and Codex-only reset-first. An unspecified strategy resolves to quota. Sticky limits default to 1 and accept 1–100; pool switching thresholds default to 80 and accept 0–100, with zero disabling voluntary quota switching. A member threshold overrides the pool; use-remaining-quota makes the effective threshold zero. Codex cache affinity defaults to true, plan exclusions remain case-insensitive, and member credit use defaults to false. Anthropic quota windows distinguish five-hour, weekly, and maximum utilization; an unspecified window resolves to five-hour. Native Messages defaults to true; explicit false selects legacy translation.

Native member priority is signed, ranges from −100 to 100, and sorts higher first. It does not change the existing smaller-first pool priority. Ordered Anthropic model routes retain unique safe names and glob patterns, personal connection allowlists, and explicit fallback. The first matching route wins; absent fallback must not widen the account roster. The generated schema owns field bounds and allowed characters, and runtime validation must enforce them.

Codex-only health options preserve optional presence: `upstream_failover_threshold` defaults to 3 and accepts 0–20, with zero disabling that threshold; `failure_window` defaults to true; `pinned_transient_policy` defaults to keep, with an explicit detour-new-threads alternative. Unspecified enum values use the keep behavior but remain distinct from omission in stored configuration. Detour applies only to new threads and must not silently move an existing pinned conversation. Reject these fields on Anthropic pools even when explicitly set to zero, false, or the unspecified enum value. Generated getters do not apply runtime defaults. Full replacement updates clear omitted options.

## Storage

Schemas describe lifecycle and ownership; bindings do not own persisted state.

## Security

Encode required authorization context and distinguish permission failure, expiry, cancellation, unknown usage/cost, and incomplete delivery. Server enforcement remains authoritative; generated interfaces cannot grant access to secrets or another user’s content.

## Logging

Use structured logs and request identifiers. Never log provider secrets, API keys, device secrets, raw prompts, or raw responses. Distinguish unknown usage, write failures, cancellation, limits, and upstream/gateway errors. See the [observability contract](servers-aiprouter-operations-contract.md).

## Build and Test

Run `protos/aiprouter/generate.sh` to generate Go, Connect Go, and TypeScript bindings. The script pins protoc 33.0 and the generator versions; install workspace dependencies first. Run `protos/aiprouter/generate.sh --check` to compare bytes and file sets without rewriting. Run `buf lint`, `buf format --diff --exit-code`, `go test ./protos/aiprouter/...`, and `pnpm --filter @aiprouter/client typecheck` plus its `test` command. Add protocol contract tests for authentication, error mapping, field presence, and cross-tenant isolation; use the [operations scenarios](servers-aiprouter-operations-contract.md).

## Dependencies and Integrations

- [servers-aiprouter-service-contract](servers-aiprouter-service-contract.md)
- [packages-aiprouter-client-contract](packages-aiprouter-client-contract.md)
- [cmds-aiprouter-client-contract](cmds-aiprouter-client-contract.md)
- [apps-aiprouter-console-contract](apps-aiprouter-console-contract.md)

## Change Triggers

Update this contract, the project index, and affected producer/consumer contracts together when interfaces, ownership, or lifecycle rules change. Follow the [instruction-update policy](README.md#instruction-update-policy); behavior belongs in contracts, not instruction files. Record implementation status, validation results, and unresolved limits in GitHub records or CI artifacts.

## References

- [Project index](project-aiprouter.md)
- [Repository defaults](repository-defaults.md)
- [Workflow](repository-workflow-contract.md)
- [Environment](repository-environment-contract.md)
- [Licensing](repository-license-contract.md)
- [Dependency security](repository-dependency-security-contract.md)

ClientService.ConfigureClientContext and BindClientContextSession are narrow, explicit inference-key client permissions. They affect only the authenticated key in its current organization/user scope; they confer no general management authority. Configuration requires explicit enable/disable. Binding confirms only a server-observed successful Responses request carrying the same session header, never a caller-supplied account or fresh pool selection.
