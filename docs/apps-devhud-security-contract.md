# apps-devhud-security-contract

## Scope

This is the canonical cross-surface threat and security contract for the implemented DevHud desktop/mobile app, Deck widgets, Chrome extension and Native Messaging host, RealQA/local-agent flows, API/sweeper, administrator, diagnostics, uploads, and updater. Domain contracts may add narrower invariants but may not weaken this document.

## Trust Boundaries

DevHud treats renderer content, captured browser context, repositories, local-agent prompts/output, Git responses, signed upload URLs, updater manifests, extension messages, deep links, and every server request as untrusted. Platform secure storage, authenticated server ownership/authorization checks, pinned release material, and the compiled closed command registries are the only authority roots.

The production desktop host uses the bundled CEF runtime with its sandbox enabled. Release validation fails when the sandbox binary, ownership/mode contract, pinned revision, or bundled frontend resources are absent. Production never loads frontend code, fonts, styles, or scripts from a network origin. Its nonce/hash-free dynamic CSP permits only packaged resources and the exact validated API, GitHub, identity, and current signed-upload origins; development-only loopback/WebSocket allowances never enter a release artifact.

The renderer cannot navigate, open a popup, trigger a download, address the filesystem, execute a process, or invoke an undeclared IPC command. Top-level navigation, new-window requests, downloads, permissions, external links, deep links, and protocol callbacks are independently validated against closed allowlists. Tauri capabilities are window-specific and least-privilege; the main window receives only the commands/events it uses, auxiliary windows receive no ambient main-window authority, and no generic shell, filesystem, HTTP, process, clipboard, or plugin capability is exposed.

Native Wayland is unsupported. Linux desktop capture and global shortcuts require X11/XWayland. The product contains no analytics/telemetry SDK, remote kill switch, remotely supplied feature flag, third-party mini-app/plugin ABI, GitHub brokerage endpoint, GitHub webhook receiver, Deck server polling/store, or native arbitrary-network bridge. Bootstrap capabilities are static compatibility declarations only.

## Network Authority

Non-loopback API, Logto, GitHub, public-image, BYO R2, and updater traffic uses HTTPS. Development HTTP is accepted only for canonical loopback hosts and ports. URL validation rejects credentials, backslashes in authority, queries/fragments where an origin is required, ambiguous ports, repeated encoding attacks, redirects to a different authority, and non-HTTP(S) schemes.

API and private R2 CORS use exact committed origin lists, exact methods, and exact headers; wildcard origin, method, or credential behavior is forbidden. The API never receives GitHub PATs, GitHub requests, Deck results, BYO R2 credentials, or upload bodies. GitHub and BYO R2 calls go directly from the native/client boundary. Bootstrap protocol schema version 2 supplies the exact official-upload origin; the native uploader rejects any signed URL or redirect outside it. A BYO R2 endpoint is derived solely from an exact lowercase 32-hex Cloudflare account ID and is not caller-controlled.

Updater authority is restricted to the compiled channel/platform manifest path and pinned HTTPS origin. Release manifests and artifacts require the configured signing key and are verified before staging or replacement. The renderer cannot choose an updater URL, channel outside the compiled set, public key, artifact destination, command, or installer argument. Mobile updates remain store-managed.

## Identity, Authorization, and Revision Integrity

Logto uses Authorization Code with PKCE, exact issuer/audience/client/redirect validation, state and nonce checks, and secure token storage. Every non-Bootstrap user RPC authenticates and transactionally revalidates block/deletion state. Every administrator RPC additionally requires the exact `devhud-admin` role and uses admin-safe projections; mutation reasons and expected-state compare-and-set checks are validated before persistence. Restore clears only the deletion block and never an independent administrative block.

Synchronized Settings schema v7 contains non-secret synchronized fields only. Shortcut bindings and repository prompts are device-local; R2 endpoints, credentials, executable paths, health, consent, caches, and process observations are excluded. Each stored canonical snapshot is bound to a raw 32-byte SHA-256 digest. Creation uses revision zero with an empty expected digest; replacement atomically compares both revision and digest, increments exactly once, and returns the current digest-bound snapshot on conflict.

Native Messaging requires the installed extension identity, user-scoped native endpoint, paired secret, versioned handshake, authenticated request envelope, nonce/request ID, bounded deadline, replay rejection, and closed RPC method set. Malformed, duplicate, unauthenticated, oversized, expired, or out-of-order messages fail without invoking application IPC.

## Local Data and Process Isolation

Logto sessions, GitHub PATs, R2 secrets, draft keys, updater signing material, Native Messaging pairing secrets, and widget PAT copies use platform secure storage or platform-isolated signing infrastructure. RealQA drafts and image/editor state are authenticated-encrypted at rest with a non-exported key. Cache/draft manifests use atomic revision publication and never make a partially written revision authoritative. Backup/transfer exclusions cover mobile secrets, widget state, and WebView data containing local diagnostics.

Every local-agent adapter is exact-version-pinned, disabled by default, read-only, network-tool-disabled, and credential-free. Agents receive neither `GH_TOKEN`, `GH_CONFIG_DIR`, the selected PAT, issue-input file, fixed writer command, user working copy, private remote URL, nor an inherited environment. DevHud uses private managed/disposable full-history clones, disabled credential helpers/LFS/submodules, and an ephemeral askpass subprocess. Draft output returns to the existing DevHud client writer. In Direct mode, only after strict marker-readiness validation does the native host resolve the selected PAT, create the private issue-input file outside the agent workspace, and run the fixed argv-based `gh api` writer. No shell interpolation is used. Cancellation and timeout terminate the child process tree; environment, stdin, stdout/stderr, prompts, paths, and credentials never enter diagnostic events.

Widget sharing is selected-Deck-only. Ordinary shared widget state may contain only the selected bounded Deck configuration/result cache; only its explicitly selected PAT is copied into a distinct widget credential service. Transaction markers and opaque credential revisions block reads and stale result publication across interrupted replacement, restore, deletion, API-origin purge, Deck removal, or disablement. Android configuration validates action, provider-bound widget ID, and caller; iOS shared state is coordinated/atomic and backup-excluded.

## Upload, Diagnostics, and Retention

Official upload creation/finalization is owner-scoped and transactionally enforces URL, image-count, rolling-byte, stored-byte, type, checksum, ETag, generation, PNG header/dimension, and immutable submission/group quotas. Signing failure rolls back the reservation. Finalization is replay-safe and never double-charges. Leases preserve visible state across promotion/removal ambiguity, and the sweeper reconciles before cleanup. Deletion/quarantine replaces the origin with the marker before CDN purge/revalidation and completion.

Redaction is recursive and fail-closed before diagnostic/report/audit persistence or export. Tokens, credentials, authorization/cookies, shortcut keys, DOM/form values, images, issue bodies, agent prompts/output, private Git remotes, arbitrary absolute/home/UNC paths, child environments, signing material, request/response bodies, and encoded variants are forbidden in logs, traces, metrics, crash rows, request rows, audit rows, reports, command lines, and generated release artifacts. Logs contain only stable enum operation/error categories, bounded numeric metadata, and UUID-v7 correlations. Product analytics are absent; crash submission is opt-in for one exact user-previewed digest-bound payload.

Logout removes the local session, credentials, authenticated settings/bootstrap caches, Deck/widget state, clones, drafts, permissions, pairings, and diagnostic events while preserving only the selected API origin and completed-onboarding marker. Account deletion blocks immediately, purges device credentials/state, retains encrypted unsubmitted drafts only for the recovery contract, permits restore for 30 days, and then removes synchronized rows and official objects. Pending destructive cleanup is restart-safe and retryable; failure cannot be reported as success. Request logs and accepted crash reports retain at most 30 days, local diagnostics at most seven days, account recovery at most 30 days, drafts at most 30 days after their last save, and pseudonymized security/admin audits at most 180 days.

## Required Adversarial Proof

Validation must cover, at minimum:

- malicious navigation, popup, download, external-link, deep-link, CSP, capability, undeclared IPC, arbitrary-path, and native-Wayland attempts;
- malformed/oversized/replayed/expired Native Messaging and RPC envelopes, authentication/role/block/delete/restore confusion, and server-side revision/digest confusion;
- SSRF, redirect, userinfo, backslash, port, scheme, origin, URL-mapping, signed-upload-origin, API CORS, and R2 CORS confusion;
- upload quota equality/crossing/races, duplicate finalization, signer rollback, object-generation/ETag/checksum mismatch, promotion ambiguity, deletion ordering, and sweeper recovery;
- browser sanitizer, DOM/selector removal, diagnostic marker/credential/path injection, encoded locators, recursive redaction, report caps, and export destination secrecy;
- local-agent marker/schema injection, child-process environment/argv/stdin/stdout exfiltration, private-remote/askpass isolation, cancellation races, and definite-versus-ambiguous writer boundaries;
- secure-store corruption and interrupted restore/purge, encrypted-draft revision recovery, widget cross-Deck/account/profile leakage and stale credential-revision publication;
- updater wrong-origin/channel/platform/signature/key/artifact attempts and generated-artifact scans for credentials, paths, unbundled resources, debug allowances, analytics, plugins, kill switches, brokerage, polling, and signing material.

## Build and Test

Changes to this boundary require generated protobuf freshness, Go format/vet/unit and PostgreSQL integration validation, Rust format/clippy/unit tests for the desktop/native hosts, Node/TypeScript lint and unit/integration tests for app/admin/extension/client packages, mobile generated-artifact validation, updater/pin verification, CORS artifact checks, recursive secret/path scanners, and a production artifact smoke test on each supported host when that host is available. A missing platform host may skip only its OS smoke; it cannot waive portable contract and artifact tests.

## Change Triggers

Update this contract, `docs/project-devhud.md`, the affected domain contract, applicable `AGENTS.md`, generated schemas/artifacts, migrations, and adversarial tests whenever a trust boundary, authority, storage class, retention rule, renderer/native command, origin, credential flow, updater path, or diagnostic field changes.

## References

- [DevHud project index](project-devhud.md)
- [DevHud app foundation](apps-devhud-foundation.md)
- [Updater contract](apps-devhud-updater-contract.md)
- [Chrome extension contract](apps-devhud-chrome-extension-contract.md)
- [Native Messaging host contract](crates-devhud-native-messaging-host-contract.md)
- [API contract](servers-devhud-api-contract.md)
- [Protocol contract](protos-devhud-v1-contract.md)
