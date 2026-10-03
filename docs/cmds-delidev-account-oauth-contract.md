# OpenRouter account OAuth PKCE (issue #1146)

## Status and ownership

[Issue #1146](https://github.com/delinoio/oss/issues/1146) owns the server/CLI
attempt lifecycle, protected credential coordination, native browser/callback
infrastructure and desktop waiting/completion flow below. Reservations alone
grant no RPC/exchange or callback authority. The owner-approved single integrated
PR composes real migrations 26–29 in order under the narrow structure exception;
independent implementation PRs retain the main-first prerequisite.
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

- Go generates a cryptographic PKCE verifier/challenge with S256 and builds only `https://openrouter.ai/auth`. Desktop uses the owned callback_url; headless omits it. Exchange only through `POST https://openrouter.ai/api/v1/auth/keys` with code/verifier/S256. No registration/client secret, inference, management key, ambient cookies/proxies, redirects, automatic HTTP retry or alternate provider.
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
- Own one native attempt per trusted window. Start a temporary listener on IPv4/IPv6 loopback only before obtaining the authorization URL. Use documented `http://localhost:<ephemeral-port>/...`, an unpredictable per-attempt path, exact Host/method/path, single-use callback with a <=16KiB request-target budget and <=16KiB header budget, the same exact code-byte validation, and no wildcard bind. This infrastructure port is unrelated to the fixed frontend dev port.
- Bind callback forwarding to the initiating window, selected server identity, attempt and current local generation. Code travels through a dedicated one-shot trusted completion bridge, never broadcast native events, query keys, app-owned history/storage, logs or echoed callback HTML. Return a constant non-secret callback page with no-store/no-referrer policy and remove the code from the final displayed callback URL; do not promise erasure of the external browser's own history. PKCE verifier/returned key never enter the renderer. Remote selected servers receive code through authenticated encrypted Connect; callback localhost means the desktop, while keyless provider localhost still means the server.
- A deliberate OpenRouter provider/Add-account action enters the waiting screen and opens the default browser exactly once. Mount, restoration, polling and Strict Mode must not start/reopen OAuth. Card heading `Connect OpenRouter`; subheading `Complete sign-in in your browser`; copy `Approve access on OpenRouter. DeliDev will finish connecting automatically.`; spinner/status `Waiting for authorization…`.
- Provide `Open browser again` for the same live attempt, `Cancel`, `Back to providers`, and `Use an API key instead`. Explicit cancel/back/fallback uses the dedicated cancellation outcome before abandoning a live flow; fallback opens the preserved manual form. Do not start another account/manual save while an earlier outcome remains uncertain.
- During exchange/save/connection, show distinct progress and disable navigation that could duplicate or discard the write. Success offers Edit account through the existing metadata editor, Manage account and Done. Validation/discovery remain explicit. Error/expiry/open-failure offers deliberate restart or manual fallback; expired/restarted flows never reuse prior callbacks.
- Closing Settings/window disposes local listener/forwarding authority, clears local waits and rejects late callbacks, focus/navigation and new completion from that opening. Consistent with #1138, Close does not implicitly send business Cancel or undo already accepted server work; accepted saves may finish and be observed later. Reopening starts fresh, with no automatic authentication/replay.
- Keep the approved 760px/20px/12px-radius card, existing colors/font and surrounding Settings. Waiting row uses a subtle bordered background; actions are neutral outlined controls, fallback a blue text action. Footer: `Your credential will be stored securely on the selected server.` and `You can validate your account after connecting.`. No key/name input in the waiting state, search/radios/Continue, ready/verified claim, provider code/URL display or logo.
- Use accessible button names, polite live progress, safe alerts, heading focus on deliberate transitions, original-provider focus on return, native Settings Escape/containment and responsive wrapping/200% zoom. Avoid focus changes from disposable polling/late results.
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
