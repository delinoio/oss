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

- System `GROK_SUBSCRIPTION_LOGIN_V1 = 39`.
- System `GROK_SUBSCRIPTION_EXECUTION_V1 = 40`.
- Worker `MANAGED_GROK_SUBSCRIPTIONS_V1 = 21`.
- `GetSubscriptionProgressResponse.grok_diagnostic = 10`, preserving Codex field 7.
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

The optional `server_operation.grok_oauth` metadata uses closed prepared,
device-sending, waiting, poll-sending, exchange-sending, refresh-sending, sealed
and cleaned phases. It retains only an AccountLogin reference and a bounded poll
sequence. Prepared material and sealed native bundles belong to the protected
store. Each send claim binds the original account, actor, server epoch, operation,
generation and lifetime. A confirmed pending or slow-down response alone permits
another original device poll. A restart cannot grant another exchange or refresh.
The existing `native_started` server fence represents exclusive credential
ownership; Go OAuth does not create a server-side Grok process. Codex native
cleanup and Grok joined HTTP/listener cleanup retain separate evidence.

Use only the fixed `https://auth.x.ai` issuer and public CLI client registration.
Discovery, authorization, token, device, userinfo and JWKS endpoints are pinned.
The browser callback is the original canonical `http://127.0.0.1:<port>/callback`.
The device presentation uses the fixed `https://accounts.x.ai/oauth2/device` page
and a separate transient user code. Do not expose its complete code-bearing URL.
Token validation requires Bearer type, bounded expiry, requested grants and the
fixed ES256 issuer/audience profile. Browser ID tokens require the original nonce.
Refresh responses without an ID token require an authenticated fixed-issuer
userinfo read of the original identity. No unverified token hint grants identity.
Grok logout waits for the original Worker lease and confirms protected deletion
before success. It does not assert remote issuer revocation.

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

The private managed native profile pins OIDC and the selected built-in model for
every auxiliary operation. API provider overrides, API-key environment variables
and imported authentication are rejected. Install original-lease cleanup before
publishing the private authentication file, use only headless cached-token
authentication and validate the complete native authentication metadata against
the current protected bundle. Native settings and announcements are bounded,
discarded presentation. They grant no model, account, permission or execution
authority; an access or consent gate is not bypassed. This private profile does
not independently activate Worker dispatch, repository execution or Resume.

Worker credential cleanup uses an explicit service and retains the original full
Grok identity across bundle rotation. Scan retained native files for both original
and latest access/refresh tokens, including padded and unpadded standard/URL
Base64 copies. Clean authentication-file absence alone is insufficient; uncertain
scans retain history and exclusive ownership. Codex cleanup keeps its separate
service profile and historical bundle semantics.

The managed harness captures the final native bundle once while its original
wire is live, before terminal session/process closure. Retain full identity and
original token digests, validate rotation without retaining a second original
plaintext bundle, and never retry a failed capture. A single protected handoff
may use the retained bytes after closure without querying the closed wire.
Independent process exit and exact post-close authentication-file comparison
still precede credential cleanup and Finish; capture alone cannot return a lease.

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
Production login and refresh admission stays closed independently of private
fixture transports. Closing admission preserves explicit original logout and
protected cleanup; a transport override or reserved capability cannot enable it.
Fixture/build/package results do not establish actual account or OS acceptance;
record supported-platform native and packaging evidence separately.

Native renewal acceptance must independently prove that an uncertain response
cannot cause a repeated official token exchange. Expired/foreign synthetic IdP
probes are rejected-profile observations only and cannot authorize an account,
issuer override or product capability. A successful cached-token acknowledgment
after failed renewal is not fresh credential evidence. Keep server admission and
Worker advertisement closed when this renewal requirement is unresolved.

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
