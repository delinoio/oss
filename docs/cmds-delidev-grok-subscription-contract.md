# DeliDev Grok Build subscription contract

## Scope

This contract owns the planned Grok Build subscription lifecycle in
`cmds/delidev-cli`, its Connect interface and the desktop AI Subscription flow.
The implementation pins Grok Build `1.0.46`. Reservation support is separate
from implemented support and actual account/platform acceptance.

## Runtime and Language

Go owns authentication, protected credentials, account leases and Worker
execution. The desktop uses TypeScript/React and a trusted Rust callback receiver.
Grok Build runs as an independently verified native child.

## Users and Operators

The owner and paired clients select a service-native account. The server owns
OAuth operations; the selected Worker owns its original native execution and
private runtime. Separate accounts can run independently.

## Interfaces and Contracts

Establish these issue #964 allocations on main before dependent implementation:

- System `GROK_SUBSCRIPTION_LOGIN_V1 = 38`.
- System `GROK_SUBSCRIPTION_EXECUTION_V1 = 39`.
- Worker `MANAGED_GROK_SUBSCRIPTIONS_V1 = 20`.
- `GetSubscriptionProgressResponse.grok_diagnostic = 8`, preserving Codex field 7.
- `GrokDiagnostic` fields detected version 1, supported version 2, phase 3,
  stable code 4, safe message 5, guidance 6 and correlation ID 7.
- Closed `GrokDiagnosticPhase` members UNSPECIFIED 0, DISCOVERY 1, VERSION 2,
  PROFILE 3, RUNTIME 4, LOGIN 5, REFRESH 6, LOGOUT 7, MODELS 8, EXECUTION 9,
  HISTORY 10 and CLEANUP 11.

These ledger reservations grant no authentication, execution, active schema or
capability advertisement. Existing System 30/35/36/37 and Worker 19 ownership
remains unchanged. The implementation reuses Request/Progress/Cancel/Callback
and protected Take/Finish Subscription RPCs. The selected account service and
`device_code` choose the flow; methods never switch automatically.

The fixed official xAI OAuth profile validates PKCE, state, nonce and tokens.
Browser callbacks bind the original IPv4 loopback address and ephemeral port.
Remote desktop callbacks retain the same original address and use a trusted
native receiver plus authenticated Connect forwarding. Device authorization
honors RFC 8628 pending, slow-down, denial and expiry.

General Chat and prepared Local/Worktree repositories require a complete ordered
manifest, primary cwd and original ownership of additional roots. Execute/Plan,
tools, questions and approvals retain original input authority. Follow-up input
resumes verified complete native history on the same account and Worker without
replaying previous prompts. Stop, restart and uncertain delivery reconcile only
original evidence. Grok 1.0.41 records and version-1 results retain attribution
and acquire no new resume authority. API execution keeps its independent support
boundary when revalidated against 1.0.46.

The approved desktop flow preserves service order, existing marks, navigation
and theme. Add account starts browser login; Use a device code is explicit.
Waiting offers reopen/copy/open-page/cancel as appropriate. Confirmed success
advances to account naming. Preparing, waiting, canceled, expired, failed and
recovery remain distinct. Exact original-request retry does not create a new
login. Polling, reconnection and Strict Mode grant no mutation authority.
The form retains a 1040px settings column, 720px login width, 40px controls,
8px corners, stacked narrow-screen actions, English/Korean text, keyboard access,
status announcements and name focus. Leaving clears display/timer/callback
authority without canceling an accepted server operation.

Quota/reset credit/pricing, enterprise SSO, external identity providers, imported
user login, account switching, cross-Worker resume, Fork, Sidechat and manual
compaction are excluded.

## Storage

Use optional existing Account/server-operation metadata and protected-store
references; add no database migration. Claim exchange and refresh durably before
sending. Uncertain results remain in recovery and are never resent. Service
bundle validation binds service, issuer, user and principal and rejects duplicate
identity ownership. Native identity and complete history have a separate owned
checkpoint. Preserve older records without granting continuation authority.

## Security

Login, refresh, execution and logout share one exclusive account lease. Logout
blocks new execution and completes only after original execution exit and
credential cleanup are confirmed; deletion reuses logout-before-delete.
Workers use the exact verified binary and a fresh private home. Managed bundles
move only through protected Take/Finish. Native refresh is confined to the lease;
verify the latest bundle and original identity, process exit and authentication
file cleanup before release. Backup/restore and deletion preserve protected
credential and unsettled original cleanup ownership.

Never retain tokens, authorization codes, device codes, login URLs or raw native
stderr in database documents, logs, events or frontend caches. Do not import or
modify a user's existing Grok home. Native callbacks remain bound to the original
trusted window, server, operation and Settings lifetime.

## Logging

Use structured Go/Rust logs with stable phase/code and original operation
correlation. Diagnostics exclude credentials, URLs, paths, identity and raw native
content. Record validation revision, commands, results and unresolved limits in
PRs and CI logs/artifacts rather than repository evidence documents.

## Build and Test

Run CLI `go test ./...`, desktop `pnpm test`, root `cargo test` for Rust changes
and protocol generation/checks. Generate required ignored `dist` outputs before
builds and remove them from the final worktree. Apply the Windows Worker/workspace
45-minute fixture watchdog independently of product deadlines.

Cover callback/state/nonce errors, cancellation races, expiry, duplicate delivery,
client disposal, server restart and uncertain exchange/refresh. Cover identity
duplication, account serialization, separate accounts, logout during execution,
cleanup failures, deletion and backup/restore. Exercise General Chat, single and
multiple repositories, Local/Worktree, Execute/Plan, native interactions, follow-up,
Stop/resume and process/server restart plus API/ChatGPT regressions.

Verify actual 1.0.46 OAuth/native profiles and real local/remote account execution
before activation. A failed profile keeps support disabled until resolved.
Fixture/build/package results do not establish actual account or OS acceptance;
record supported-platform native and packaging evidence separately.

## Dependencies and Integrations

The official [CLI reference](https://docs.x.ai/build/cli/reference) defines browser
and device login. Main-first reservations precede a complete feature PR. Use
`pnpm proto:generate` for Go/TypeScript bindings and compatibility facades.

## Change Triggers

Changes to capability ownership, OAuth authority, bundle identity, lease cleanup,
native history or UI lifetime require updates to this contract, the project index,
protocol/structure/desktop/subscription contracts and applicable AGENTS files.

## References

- [Project index](project-delidev.md)
- [Subscription lifecycle](cmds-delidev-subscription-contract.md)
- [Protocol](protos-delidev-v1-contract.md)
- [Structure](cmds-delidev-structure-contract.md)
- [Subscription settings](apps-delidev-subscription-settings-contract.md)
- [Workspace](cmds-delidev-workspace-contract.md)
- [Repository defaults](repository-defaults.md)
