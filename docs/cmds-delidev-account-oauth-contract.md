# API account browser OAuth

## General API OAuth reservations (issue #964)

The approved extension covers direct ordinary API credentials only: OpenRouter
PKCE to an API key, Hugging Face PKCE to OAuth tokens, Google Gemini PKCE to OAuth
tokens with an explicit quota/billing project, and Baseten device authorization
to OAuth tokens. Coding subscriptions and flows that broker or separately create
API keys are outside this extension. Other providers retain API-key or Keyless
connections. Existing OpenRouter behavior and its issue #1146 ownership remain.

Establish these issue #964 reservations on main before dependent implementation:

- `ProviderInventoryCapability.ACCOUNT_OAUTH_V1 = 6`; preserve OpenRouter-only 5.
- `ProviderConnectionMethod.OAUTH_DEVICE = 4`; preserve values 0–3.
- New `AccountOAuthFlow`: UNSPECIFIED 0, PKCE 1, DEVICE 2.
- New `AccountOAuthGoogleOptions.quota_project_id = 1`.
- `StartAccountOAuthRequest.google = 3`.
- `StartAccountOAuthResponse.flow = 5` and `user_code = 6`. The temporary device
  approval code is available only from the live Start response, never Status.
- `CompleteAccountOAuthRequest.authorization_state = 3`; new PKCE profiles bind
  completion to the original state. Preserve the historical OpenRouter input.
- Private migration 31 follows the real migrations 26–30. It owns authentication
  profiles, protected token-generation references, durable refresh claims and
  cleanup metadata. Existing OpenRouter records and vault references survive.

These reservations activate no schema, credential exchange, capability or native
browser authority. Complete feature PRs follow reservation closure on main in
this order: common lifecycle plus Hugging Face, Gemini, then Baseten.

Go owns exchange, device polling, protected access/refresh tokens and refresh.
SQLite, logs and frontend caches contain no tokens, verifier or device code.
Credential resolution serializes refresh per connection and atomically replaces
the protected generation without changing account, connection or execution
ownership. Uncertain exchange or refresh never grants an automatic resend.
Device polling repeats only after explicit pending/slow_down responses; uncertain
token issuance stops. Cancellation, restart, restore and deletion retain durable
claims and independent cleanup. API inspection and execution share this authority.

Only an enabled exact managed official provider profile can grant OAuth.
DeliDev-owned public/native app registration and real ordinary-API credential
compatibility are activation prerequisites. Do not copy another application's
client ID. Profiles without registration or acceptance do not advertise OAuth
support and remain incomplete. User-owned OAuth app configuration is excluded.
New connections follow the current API defaults and remain unverified until the
user explicitly validates or discovers models. Native flow support and server
capabilities must both be present; old combinations keep manual connections and
the existing OpenRouter compatibility behavior.

Gemini requires an explicit Google Cloud project ID before browser launch and
uses that project for quota/billing. Baseten opens its fixed approved browser
address and displays the transient approval code without a callback listener.
Every native flow retains original window/server/attempt lifetime ownership;
PKCE also retains the original state. The common AccountOAuth card preserves the
approved 760px width, 20px padding, 12px radius, theme, wrapped actions, keyboard
focus, status announcements, narrow-window and 200% zoom behavior. Switch to an
API key only after cancellation is confirmed; success never auto-validates.

## Common lifecycle and Hugging Face

The common implementation activates migration 31 after real 26–30. The private
`account_oauth_credentials` table retains exact account, immutable connection,
provider/preset, client-profile digest, expiry, token-generation reference,
refresh claim and cleanup references. No account foreign key can erase an orphan
claim. Vault entries alone contain bounded access/refresh token envelopes. Restore
retains the current table; unresolved claims or cleanup block restore. Refresh
commits a claim before HTTP, serializes concurrent requests by connection, swaps
the protected reference atomically, then removes prior generations. Lost HTTP or
Vault acknowledgement leaves recovery/denied authority and cannot resend refresh.
Inspection and execution resolve the same current credential; doctor reads only.

The Hugging Face adapter uses the exact managed `https://router.huggingface.co/v1`
profile, public-client PKCE, `inference-api` scope and the registered localhost
`/oauth/hugging-face/callback` path with a canonical ephemeral port. Native and Go
both bind the original state. A state-bound empty code records an access-denied
receipt without HTTP. Duplicate completion replays the original receipt; explicit
code-free recovery reads only original protected local material. Start alone
returns authorization URLs. The common card uses the selected provider's title,
progress and recovery text and retains unverified success.

`cmds/delidev-cli/internal/providers/oauth_clients.json` is compiled public release
registration metadata shared by Go and native. A profile requires its DeliDev
client ID, `registered` status and ordinary API `accepted` status. Native profile
inventory and server capability 6 must agree before a new browser option appears;
older native hosts retain OpenRouter behavior. Current new registrations remain
pending/unverified. Implemented fixture paths do not establish activation or real
account/native acceptance. Baseten remains subsequent work.

CLI Start accepts `--callback-url`; Hugging Face requires the registered callback.
OpenRouter alone retains no-callback headless mode. `--callback-stdin` accepts a
bounded protobuf JSON envelope containing only base64 `authorizationCode` and
`authorizationState`. Mutation identity comes from original CLI flags, never the
callback. `--code-stdin` and explicit original `--recover` remain supported.

## Google Gemini project-bound PKCE

The Gemini adapter binds the exact managed OpenAI-compatible endpoint at
`https://generativelanguage.googleapis.com/v1beta/openai`, the DeliDev desktop
public client and registered IPv4 loopback `/oauth/google-gemini/callback` path.
Use Google PKCE with `https://www.googleapis.com/auth/cloud-platform` scope,
`access_type=offline` and `prompt=consent`. Code exchange and refresh use only
`https://oauth2.googleapis.com/token`. The native opener validates every fixed
field and the original state, and permits only bounded Google authuser/prompt
callback metadata in addition to code/state/scope or access_denied.

Before any listener, Start or browser opening, the common card requires a Google
Cloud project ID. Validate 6–30 lowercase letters, digits or hyphens, starting
with a letter and ending with a letter/digit. Start receipts include immutable
project options without changing historical OpenRouter inputs. Keep the project
in private connection metadata. Inspection and native execution use the same
protected access token with `Authorization: Bearer` and `x-goog-user-project`;
never send an OAuth token as `x-goog-api-key`. Independently validate the exact
managed provider and official Google destination before attaching the project.
Downstream project headers cannot replace this connection binding. API keys and
foreign/regional providers cannot inherit the project.

The compiled Google registration remains pending/unverified. Google OAuth for
the general API is documented, but the DeliDev client and actual compatible
inference path still require service acceptance before capability advertisement.
Fixture checks, desktop builds and public registration are distinct evidence.

## OpenRouter account OAuth PKCE (issue #1146)

## Status and ownership

[Issue #1146](https://github.com/delinoio/oss/issues/1146) owns the server/CLI
attempt lifecycle, protected credential coordination, native browser/callback
infrastructure and desktop waiting/completion flow below. Reservations alone
grant no RPC/exchange or callback authority. The complete Go/CLI/native/desktop
lifecycle implements real migration 29 after real 26–28. Shared reservations
reached main first; independent feature PRs merge in dependency order. Scripted
fixtures and builds remain separate from real-provider and platform acceptance.
The direct-action picker prerequisite #1145 is already on main. Compose the
UI with the current AI API Keys terminology and compact presentation
from #1135/#1237 and fresh Settings lifetime from #1138.

Go remains the business and credential owner; native Rust owns only the closed
trusted-window browser/listener infrastructure. The typed client and desktop
use authenticated Connect for selected local and remote servers.

## Compatibility reservations

Establish all reservations on main before dependent implementation. The owning
issue remains 1146 after an implementation PR exists. Existing numbers and the
migration order retain their current meanings.

- `ProviderInventoryCapability.OPENROUTER_OAUTH_PKCE_V1`: 5, independent of the
  four existing account-flow gates.
- `ProviderInventoryEntry.connection_method`: 9.
- New `ProviderConnectionMethod`: UNSPECIFIED 0, API_KEY 1, OAUTH_PKCE 2, KEYLESS 3.
- New `AccountOAuthState`: UNSPECIFIED 0, AWAITING_AUTHORIZATION 1, EXCHANGING 2,
  SAVING 3, CONNECTED 4, CANCELED 5, EXPIRED 6, FAILED 7, INTERRUPTED 8,
  RECOVERY_REQUIRED 9.
- Private SQLite migration 29 follows the real Claude accounting 26, request
  diagnostics 27 and subscription identity/retirement 28 implementations.
  Do not implement placeholder predecessors or activate 29 before that complete
  sequence exists. Changing
  product order requires a separately reconciled reservation sequence on main.

The allocation ledger marks each member of a wholly new enum with
`newDeclaration: true`, retaining one owner and a zero UNSPECIFIED value without
adding it to the immutable active baseline. Reservations grant no capability advertisement. The reconciled schema declares
the closed enum/field/RPC interfaces and generates Go/TypeScript bindings.
Advertise capability 5 only together with the implemented product lifecycle.

## Required implementation

### Capability and interfaces

- Add a closed connection-method enum (`API_KEY`, `OAUTH_PKCE`, `KEYLESS`) to provider inventory and an independent OAuth capability marker. Advertise OAuth only for an enabled saved **managed OpenRouter preset** with its exact official endpoint/protocol/bearer contract. Names, copied URLs and custom copies cannot grant OAuth.
- Preserve existing bearer authentication/document fields and all four current account-flow capability gates. A server satisfying those gates but lacking OAuth support uses current manual connection with no OAuth badge. Missing old gates still means update-required. Use additive protocol rollout, no feature flag or account/provider backfill.
- Add owner/paired-client-only AccountService `StartAccountOAuth`, `CompleteAccountOAuth`, `CancelAccountOAuth`, `GetAccountOAuthStatus`; Workers cannot invoke them. Start binds provider ID/current revision, request ID and optional canonical native callback; returns attempt ID/revision, state, expires-at and the authorization URL only while the original process owns that live start. Status never returns a URL or secret.
- Completion carries attempt ID, expected revision, request ID and exact opaque authorization-code bytes. Initial code is 1–8192 well-formed UTF-8 bytes with no NUL/control characters, with no trimming/normalization; code-free original local recovery is the explicit exception. These are DeliDev input limits, not provider guarantees. Initial completion requires the code and atomically binds a server-keyed, domain-separated HMAC commitment. Different codes conflict. Original completion replay never repeats exchange; after dispatch, code-free recovery is allowed only for the original completion identity and already-bound local result, never as a new exchange. Cancel carries attempt ID/revision/request ID. Responses contain closed state, optional matching Account Resource and safe typed problem only.
- States distinguish awaiting authorization, exchanging, saving, connected, canceled, expired, failed, interrupted and recovery-required. Retain stable IDs/revisions and original receipts; do not return upstream account identity/email, raw HTTP errors or credential material.
- Add explicit CLI `account oauth start`, `complete --code-stdin`, `status`, `cancel`, with provider/attempt/revision/request selectors matching the RPCs. Default CLI start uses OpenRouter's documented no-callback headless mode; completion reads bounded stdin, never a code/key argv option. CLI remains noninteractive and never implicitly starts a server.

### Server ownership and persistence

- Go generates a cryptographic PKCE verifier/challenge with S256 and builds only `https://openrouter.ai/auth`. Desktop uses the owned callback_url; headless omits it. Exchange only through `POST https://openrouter.ai/api/v1/auth/keys` with code/verifier/S256. Resolve one immutable explicit server route under the existing network contract; route or TLS failure never grants direct fallback. No registration/client secret, inference, management key, ambient cookies/proxies, redirects, automatic HTTP retry or alternate provider.
- Keep verifier and canonical callback/authorization URL in memory in their original server process. Cap application attempts at ten minutes and 32 nonterminal or unresolved-cleanup server attempts; distinguish this application deadline from the provider's code lifetime after issuance. Clear transient buffers and bound HTTP with the existing 20-second deadline/32KiB response-header conventions and a deliberately tighter new OAuth response ceiling of 64KiB (ordinary provider inspection permits 4MiB). Validate the returned key with the existing 1–8192-byte API-key validator.
- Add private `account_oauth_attempts` metadata storage in reserved schema 29 after the implemented schema 25 baseline and real migrations 26–28. Preserve the reservation's meaning; reconcile any required order change on main before implementation. Include actor/server/provider UUID+revision, original start/completion IDs, reserved account/create/connect UUIDs, process generation, state/revision/times, comparison HMAC/digests, protected-reference identity and cleanup status. No raw verifier, code, key, callback URL or approval URL in SQLite/receipts. No foreign key requiring an existing account and no cascading deletion of recovery evidence.
- Fresh stores include the table; use the existing verified backup-first transactional migration, preserving accounts/providers/settings/claims/default deletions. Exclude attempt rows from public resources/snapshots/events/portable export. Database images contain metadata, never credentials. Existing managed restore eligibility refuses unresolved OAuth attempts or cleanup. Historical images cannot replace current once-only attempt metadata; restore copies the current private attempt table and quarantines historical receipts. This adds no restore operation.
- Before HTTP, commit one durable exchange-dispatch claim, following the existing once-only HTTP-claim pattern. HTTP runs outside SQLite/account locks. No timeout, cancellation, duplicate callback, RPC retry or process restart may send that exchange again.
- Seal a successfully received key in the existing vault under `Ref{Owner:reservedAccountID, ID:originalConnectID, Purpose:account-api}`. Then create disconnected default metadata once and connect once using shared existing lifecycle helpers, exact creation revision and original identities. Factor locked helpers to avoid recursive account-gate acquisition. Recheck actor, provider and attempt authority before staging and at final commit.
- Defaults: alias OpenRouter, type api, enabled=true, exclude_automatic=false, recovery_notifications=true. This intentionally follows the approved/current API-creation default; document its scoped override of the broader requirements' default-off wording without changing saved accounts or subscription defaults. Connection sets unverified and clears prior observations under existing rules. Never auto-validate/discover.
- On a new server lifetime, awaiting attempts become interrupted; start replay returns original metadata without a fresh verifier/URL. Claimed exchanges become recovery-required unless the exact sealed credential/original accepted local result can be inspected. Read/status is observation only; explicit original completion retry may finish local create/connect without HTTP. A lost plaintext staged payload cannot be reconstructed or replaced.
- Unknown exchange response, malformed success or lost returned key means possible provider-side key creation, not safe failure/retry. Retain recovery-required and show guidance to inspect the OpenRouter keys dashboard before explicitly starting over/manual connection; local cancellation cannot claim provider revocation.
- Serialize cancel versus final connection publication at the existing account gate. Cancellation committed first prevents later publication and records cleanup before vault tombstoning; connection committed first returns connected and cannot be silently disconnected by Cancel. Preserve saved disconnected accounts after partial connect failure, account-less staged references and cleanup obligations across failures/restart. Native cleanup uncertainty stays visible; never speculatively delete evidence.

### Native and desktop

- Add a closed OpenRouter opener/callback infrastructure capability for trusted main and saved-server webviews. Preserve trusted-origin/window/server-generation checks and existing GitHub restrictions; no generic opener, new renderer navigation/CSP origin, embedded account browser profile or credential storage in Rust.
- Register `account_oauth_native` in the native build-time app command manifest and grant its dedicated `account-oauth` permission only through the existing `main` and `server-*` webview capabilities. Handler registration alone is insufficient: Tauri filters and authorizes commands through the generated ACL. Native host tests must resolve the actual generated manifests/capabilities and verify both trusted-webview admission and raw-child/remote-document denial. Runtime window, server and epoch checks remain independent. Record native preparation/browser outcomes with closed action, phase, outcome and failure metadata only; successful callback polling stays quiet.
- Own one native attempt per trusted window. Start a temporary listener on IPv4/IPv6 loopback only before obtaining the authorization URL. Use documented `http://localhost:<ephemeral-port>/...`, an unpredictable per-attempt path, exact Host/method/path, single-use callback with a <=16KiB request-target budget and <=16KiB header budget, the same exact code-byte validation, and no wildcard bind. This infrastructure port is unrelated to the fixed frontend dev port.
- Bind callback forwarding to the initiating window, selected server identity, attempt and current local generation. Code travels through a dedicated one-shot trusted completion bridge, never broadcast native events, query keys, app-owned history/storage, logs or echoed callback HTML. Return a constant non-secret callback page with no-store/no-referrer policy and remove the code from the final displayed callback URL; do not promise erasure of the external browser's own history. PKCE verifier/returned key never enter the renderer. Remote selected servers receive code through authenticated encrypted Connect; callback localhost means the desktop, while keyless provider localhost still means the server.
- The bridge captures a native window epoch before awaiting sidecar identity and checks it again after its blocking operation. Window closure invalidates admitted, queued Begin calls as well as existing listeners. A visit supplies one opaque local opening identity before Begin; scoped disposal records a tombstone even if the Begin response was lost. Original Begin replay returns its existing listener metadata without opening a browser. Tombstones/closed-window identities are bounded at 4096 per native process; exhausted capacity fails explicitly rather than evicting a revocation. A replacement opening cannot be disposed by a late predecessor.
- A deliberate OpenRouter provider/Add-account action enters the waiting screen and opens the default browser exactly once, after the destination category mounts. Consume that typed non-secret entry once. Ordinary mount, restoration, polling and Strict Mode replay must not start/reopen OAuth. Card heading `Connect OpenRouter`; subheading `Complete sign-in in your browser`; copy `Approve access on OpenRouter. DeliDev will finish connecting automatically.`; spinner/status `Waiting for authorization…`.
- Provide `Open browser again` for the same live attempt, `Cancel`, `Back to providers`, and `Use an API key instead`. Explicit cancel/back/fallback uses the dedicated cancellation outcome before abandoning a live flow; fallback opens the preserved manual form. Do not start another account/manual save while an earlier outcome remains uncertain.
- During exchange/save/connection, show distinct progress and disable flow-local actions that could duplicate or discard the write. Settings category navigation stays available and disposes only local presentation/callback authority. Success offers Edit account through the existing metadata editor, Manage account and Done. Validation/discovery remain explicit. Error/expiry/open-failure offers deliberate restart or manual fallback; expired/restarted flows never reuse prior callbacks.
- Leaving the account category or closing Settings/window disposes local listener/forwarding authority, clears local waits and rejects late callbacks, focus/navigation and new completion from that opening. Consistent with #1138, category departure or Close does not implicitly send business Cancel or undo already accepted server work; accepted saves may finish and be observed later. Reopening starts fresh, with no automatic authentication/replay.
- Keep the approved 760px/20px/12px-radius card, existing colors/font and surrounding Settings. Waiting row uses a subtle bordered background; actions are neutral outlined controls, fallback a blue text action. Footer: `Your credential will be stored securely on the selected server.` and `You can validate your account after connecting.`. No key/name input in the waiting state, search/radios/Continue, ready/verified claim, provider code/URL display or logo.
- Use accessible button names, polite live progress, safe alerts, heading focus on deliberate transitions, original-provider focus on return, current issue #1236 Settings page/child-dialog Escape and containment rules, and responsive wrapping/200% zoom. Avoid focus changes from disposable polling/late results. The category-owned OAuth controller uses direct authenticated write-only completion so transient code bytes never enter React Query mutation/query caches; clear its native and renderer byte buffers on every outcome.
- Structured logs cover stable lifecycle stage/outcome/error/correlation and non-secret attempt/account IDs only; no code/verifier/key/URL, provider response body, email or browser contents. Update requirement/account/credential/storage/desktop/protocol/client contracts and applicable AGENTS with the implementation. Record implementation and validation evidence in the owning PR, issue and CI logs/artifacts under the root policy; do not add repository evidence documents.


## Acceptance criteria and verification

- Eligible official OpenRouter selection opens one browser authorization and connects one default account automatically after approval; no copied API key is required.
- Secrets remain server-owned; native callback and write-only completion are bound to the correct actor/provider/server/window generation.
- Every uncertain/crash/cancel/replay path sends at most one upstream exchange. Recovery never reissues a verifier, recreates a provider key or removes staged evidence speculatively.
- Exact local recovery can finish an already sealed credential without contacting OpenRouter; partial saves retain the correct disconnected account/cleanup state.
- Cancel/commit races, revoked actors, provider Off/change, expired attempts and closed client lifetimes cannot cause unauthorized late connection.
- CLI/headless and remote selected-server flows use the same server lifecycle. API-key/keyless, old-server gating, subscription behavior and explicit validation/discovery stay intact.
- Migration/backup secrecy, generated bindings, frontend/native tests and truthful platform/provider evidence are recorded separately.

## Test Scenarios

1. **Primary portable flow:** inject OAuth HTTP fixture, clock/randomness, fake OS vault and native listener/opener; expose exact managed enabled OpenRouter with new/old capabilities. Click it once, return a code on its owned callback and a valid fixture key from exchange. Assert one browser dispatch, one S256 POST, one account/create/connect identity, approved defaults, unverified health, no validation/models HTTP, no returned key/email, and Edit account preserves UUID/revision.
2. **Duplicate/uncertain HTTP:** send duplicate callback/completion, then simulate timeout or connection loss after fixture accepted POST, malformed success and lost key before sealing. Assert one POST total, recovery-required, no automatic retry/fallback/second account and safe possible-upstream-key guidance.
3. **Crash/restart:** stop before dispatch claim, after claim before HTTP acknowledgment, after seal before metadata save, and after account/connect commit before response. Restart with the same metadata/vault. Assert interrupted or recovery-required as appropriate, no fresh challenge/POST; explicit original local retry completes only a sealed credential/original accepted result once.
4. **Vault/SQLite recovery:** stage without an account row; fail native Put, account save, connect commit and cleanup in turn. Assert exact reserved ref/cleanup survives; missing plaintext remains recovery-required; sealed-key recovery uses original IDs and account revision with zero HTTP. Tombstone/replacement/deletion cannot resurrect an old connection.
5. **Cancel race:** coordinate cancellation immediately before/after final connection commit and during in-flight exchange. Cancel-first blocks publication and retains cleanup; connect-first reports connected with no implicit disconnect. A provider-side minted key is not falsely described as revoked. Lost cancel acknowledgment permits exact cancel retry only.
6. **Authority/eligibility:** use wrong actor, Worker credential, revoked paired client, stale/off provider, custom copy named OpenRouter and official-looking URL without managed provenance. Assert denial before side effects/publication. Change selected server/window generation during pending callback; reject old completion.
7. **Native/lifetime/UI:** exercise owned localhost callback over both loopback families, wrong Host/path/method, oversized/duplicate callback and malicious authorization destination. Assert no wildcard listener/generic opener and cleanup on cancel/expiry/close/exit. Reopen browser explicitly using same attempt. Close/reopen Settings under #1138/Strict Mode; late callbacks do not start new completion, update/focus old state or reopen browser. Already accepted server saves remain observable. Test open failure, expiry, manual fallback and old-server manual behavior.
8. **CLI/remote:** start headless with no callback, read code via stdin, complete/status/cancel through the same service; assert no secret argv/output and no implicit server start. Run desktop callback forwarding against a remote fixture server and verify native callback is local to the client while vault is server-owned.
9. **Storage:** test fresh schema 29 and backup-first 25-to-29 success/rollback after real migrations 26–28, copied awaiting/claimed database images opened in a new process, preserved old state/receipts/default deletions, orphan staging/cleanup restart. Inspect DB/backup/receipt/status/log/native event fixtures for absence of verifier/code/key/URLs. No copied backup can reacquire exchange authority.
10. **Required checks:** run `go test ./...`, race tests and vet in `cmds/delidev-cli`; protobuf lint/breaking/generated freshness and typed-client checks; `pnpm test` in `apps/delidev`; root `cargo test` after native Rust changes. Prepare required generated dist/assets and LFS hydration first, remove generated dist afterward. Validate approved viewports/keyboard and actual browser callback on supported native hosts; record fixture versus real provider/platform evidence separately without using user credentials in automation.


## Out of scope

Subscription/Codex/Claude login; embedded browser/account-profile work (#1087/#1095); OAuth for custom or other providers; new providers; generic opener/renderer networking; inference, automatic validation/discovery; provider management-key/revocation automation; backup restore; saved-account preference migration; feature flags; implementing related naming/Settings-reset work.


## References

- [Official OpenRouter OAuth PKCE](https://openrouter.ai/docs/guides/overview/auth/oauth)
- [Source ownership and compatibility](cmds-delidev-structure-contract.md)
- [Account lifecycle](cmds-delidev-accounts-contract.md)
- [Protected credential storage](cmds-delidev-credentials-contract.md)
- [Provider provenance](cmds-delidev-provider-activation-contract.md)
- [Storage](cmds-delidev-storage-contract.md)
- [Desktop](apps-delidev-desktop-contract.md)
- [Protocol](protos-delidev-v1-contract.md)
- [Typed client](packages-delidev-api-client-contract.md)

- Exchange code/verifier and returned printable-ASCII keys remain owned, zeroizable byte buffers through JSON encoding/decoding. Transport cancellation after a valid key is returned does not discard it: settle under independent bounded original-actor authority, honoring serialized business cancellation before sealing. Expired awaiting attempts with no claimed credential cleanup do not block managed restore; exchanging/saving/recovery and cleanup obligations remain blocking.

Callback polling rechecks captured Go-verified local connection authority against its fixed descriptor without sidecar launches. Exact native BindOpen replay settles uncertain binding without opening twice; the renderer records binding only after acknowledgement. Secret exchange response parsing stays byte-backed and rejects duplicate nested object keys without immutable credential-value strings.

A new OAuth Start can expose `oauth_start_not_admitted` only for a typed rejection inside its rolled-back admission transaction. Replay, transport and post-commit errors retain the original receipt. Explicit Cancel/Back may dispose a rejected native opening before manual fallback. Database restore preserves a current connected OAuth account and its current provider only when its connection ID matches the original once-only attempt; preserve their coupled vault ownership, never a historical or disconnected generation.

If the saved provider changes after OAuth Start admission, replay returns the original attempt in interrupted state with no authorization URL. Preserve its exact ID/receipt and permit explicit original cancellation; transient provider reads retain uncertainty. This transition sends no exchange and cannot grant native callback authority.
