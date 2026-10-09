# OpenCode Go subscription contract

## Scope

Issue #2097 owns the closed `opencode_go` Account and Model identity, protected key connection, OpenCode execution and AI Subscription presentation. Canonical paths are `cmds/delidev-cli/internal/{domain,server,store,worker,apiproxy}`, `protos/delidev/v1`, `packages/delidev-api-client` and `apps/delidev/src/opencode-go-account.tsx`.

## Runtime and Language

Go owns account and execution authority. React/TypeScript owns disposable presentation. The existing OpenCode HTTP adapter retains its pinned 1.18.32 profile and native provider `delidev`; negotiated direct startup validates the original actual process before input.

## Users and Operators

Authenticated owners and paired clients explicitly connect console-issued Go or Go Plus keys to the selected server. Both plans use one service identity. The authenticated original Worker owns native processes and session history.

## Interfaces and Contracts

Subscription identity 4, System `OPENCODE_GO_SUBSCRIPTIONS_V1 = 54` and Worker `OPENCODE_GO_SUBSCRIPTIONS_V1 = 28` are recorded with this complete feature. System 52 remains Project behavior ownership. These declarations add no RPC or migration. Allocation alone grants no support.

Reuse `ConnectAccount`, `DisconnectAccount` and `GetAccountStatus` with their original actor, request, revision and connection receipts. A stored key means connected; it does not prove entitlement. Do not send paid inference automatically. The only upstream profile is server-owned OpenAI Chat Completions with Bearer authentication at `https://opencode.ai/zen/go/v1/chat/completions`. There is no user Provider reference or API-format selection/change.

Execution requires both independently negotiated capabilities, the same account connection, service-bound OpenCode model and immutable assignment. The existing relay validates input, original native configuration and tools. Upstream `x-opencode-session` comes only from the original server-accepted OpenCode native session; caller headers cannot supply it. Missing proof denies inference before key use. Continuation retains the original native session; independently eligible Forks retain their own child native session and cleanup. No failure permits Zen fallback, another endpoint, automatic credential replacement or uncertain input replay.

Settings appends OpenCode Go / For OpenCode to existing flat service rows with licensed local OpenCode artwork. Connect uses the category-owned name/key form, default OpenCode Go alias, masked key, accessible reveal, API-key focus and explicit Console link. Exact uncertain requests and partial create results retain their original controller until category disposal; presentation dismissal does not manufacture a new account. Manage connection and explicit Disconnect use the shared original-key cleanup flow. English/Korean and theme/reflow semantics remain shared. Quota is unavailable: no quota bars, refresh, reset credits or native subscription login actions.

## Storage

Reuse schema-2 service Accounts, service Models, immutable execution/job/session documents and protected server `AccountAPI` vault references. Do not create native login IDs, login state, OAuth attempts, bundles or Worker key copies. Portable configuration contains no credential/native authority; restored Accounts remain disconnected and protected cleanup remains independent. No SQLite migration or schema conversion is introduced.

## Security

Preserve original actor/connection/revision/reference validation. Disconnect revokes new work, cancels and joins active relay requests before removing the original protected key. Independent durable cleanup preserves failed attempts through restart; a terminal failure requires fresh explicit confirmation. Referenced Accounts cannot be deleted without their existing reference checks. Server and Worker capability absence fails closed, preserving older peer behavior. Keep API keys, raw provider bodies and personal account data outside public resources, assignments, diagnostics and logs.

## Logging

Use existing bounded structured account/execution/proxy phase, original ID, status and safe error logs. Do not log keys, input/output bodies, native history or raw provider errors. Service attribution may be recorded as the closed identity.

## Build and Test

Run regenerated protocol freshness/lint/breaking checks, API client tests/build, relevant uncached Go domain/server/store/relay/Worker and pinned OpenCode adapter fixtures, race/vet checks, localization checks and `pnpm test` from `apps/delidev`. Fixtures use synthetic protected keys and isolated transports; they do not establish real paid entitlement, installed-native or platform acceptance. Those acceptance tasks remain owner-assigned.

## Dependencies and Integrations

Reuse the account, subscription, catalog, harness, proxy, execution startup, Fork, protocol and Settings contracts. Native subscription login and quota capabilities remain independent; known-model suggestions do not establish OpenCode Go model availability or entitlement.

## Change Triggers

Update this contract, the DeliDev project index and relevant scoped AGENTS rules when identity, allocation, fixed profile, key ownership, native proof, cleanup or presentation ownership changes. Regenerate Go and TypeScript bindings from the reconciled protocol sources.

## References

- [Project index](project-delidev.md)
- [Accounts](cmds-delidev-accounts-contract.md)
- [Subscriptions](cmds-delidev-subscription-contract.md)
- [Catalog](cmds-delidev-catalog-contract.md)
- [Harness](cmds-delidev-harness-contract.md)
- [Relay](cmds-delidev-proxy-contract.md)
- [Protocol](protos-delidev-v1-contract.md)
- [Settings](apps-delidev-subscription-settings-contract.md)
- [Repository defaults](repository-defaults.md)
- [Official Go documentation](https://docs.opencode.ai/docs/go/)
