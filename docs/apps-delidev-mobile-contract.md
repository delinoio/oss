# DeliDev mobile client contract

## Scope

`apps/delidev-mobile` owns the separate iOS and Android remote client in issue
#2099. Its identity is `io.delino.delidev.mobile`, and its display name is
DeliDev. It consumes the existing server and Worker system; it does not include
or supervise a Go server, Worker, desktop sidecar, CEF or model harness. Desktop
and CLI lifetimes remain independent.

## Runtime and Language

React and TypeScript run in an isolated Tauri Wry webview, with Rsbuild assets.
iOS uses WKWebView on iOS 18 or later. Android uses Android System WebView on
API 31 or later. Rust owns only the platform bridge. Swift and Kotlin own
protected state, foreground notification permission and OS submission. Use the
repository-pinned Tauri Git revision and verified CLI wrapper. Do not fall back
to a desktop runtime or a locally built unverified CLI.

## Users and Operators

Paired clients connect to a server selected by the user. Operators configure
projects, Agents and Runners on that server. Mobile selects those saved entities;
it never creates account, native execution or local-machine authority.
Repository owners configure internal beta accounts, signing identities,
protected workflow credentials and tester groups. This feature does not
provision those accounts or credentials.

## Interfaces and Contracts

Sessions, Inbox and Settings are the three bottom tabs. Opening a session shows
the conversation as the fourth screen. New session keeps Project, Agent and
Runner selections separate, supports Worktree and General Chat, and accepts
text in Execute or Plan mode. Local execution and image attachments are absent.
Existing generated Connect RPCs retain Go validation, revisions, authorization,
receipt replay and immutable execution ownership.

Every profile pins its explicit HTTPS origin, server identity, client device
identity and original pairing request. Pairing accepts the existing version-1
client grant document only when its HTTPS endpoint matches the explicit origin.
The original token and request are stored before the first pairing call. Receipt
retry reuses them; it cannot obtain a second grant, device or machine.

One unresolved mutation per profile is durably stored before sending, using the
original generated protobuf JSON and exact request ID. Create, Send, Steer,
Stop, Resume, question, approval, Inbox read-state, notification preferences,
notification claim/report and self-revocation use closed operation dispatch.
A lost response retains the original request. Foreground return and connection
refresh perform reads only. Explicit retry requires inspection of current
server state and confirmation; it never changes a retained request or selection.
Steer operates on an already queued input and its original revision, execution
and native turn. Stop and Resume require current observations and confirmation.
Question and approval responses retain the original interaction revision.
Protected-answer questions remain unavailable outside their original supported
client boundary. Inbox opens the current original entry and interaction before
showing response controls; notification delivery never changes read state.

Language, System/light/dark theme, safe-area padding, software keyboard resize,
48-pixel targets, native dialogs, keyboard access and opener focus return belong
to the mobile client. English and Korean catalogs have identical keys. Narrow
layouts and 200% effective reflow preserve drafts and readable content. No
remote fonts or external renderer assets are required.

## Storage

A bounded version-1 protected state document contains profiles, credentials,
original unresolved requests and local preferences. iOS stores it in Keychain
with `WhenUnlockedThisDeviceOnly`, with no synchronizable item. Android stores
AES-GCM ciphertext in `noBackupFilesDir`, using an Android Keystore AES key and
atomic replacement. Android app backup and cleartext traffic are disabled.
No token, pairing code, pending request or private content enters WebStorage,
query keys, exports or logs. Corrupt or unavailable protected state fails closed.
An explicit confirmed local forget removes only that profile; remote revocation
is a separate confirmed server operation. App close never stops remote sessions.

## Security

The webview loads bundled content only. Navigation to remote documents and
popups is not granted. Connect calls use HTTPS, omit browser credentials, reject
redirects, and acquire the selected profile's bearer only through the owning
transport closure. Profiles cannot share transport or cache identity.
Background transitions abort observation streams and clear query caches. Resume
verifies original server identity and protocol before obtaining a new coherent
event cursor. A bounded Settings snapshot supplies the cursor; session and
history views remain explicit paginated reads. Events invalidate observations;
they do not replay mutations. Old-profile reads and subscriptions are canceled.

Foreground notification submission requires the selected protected profile and
active platform state. A fresh existing server claim grants at most one OS
submission. Replayed claims grant none. An uncertain report is protected before
OS submission, and original reports remain receipt-safe. OS submission does not
prove that a person saw an alert or accepted a native request.

## Logging

Platform failures log only the operation and sanitized outcome. Product
connection diagnostics include opaque profile/server identifiers and status.
Never log origins, authorization headers, pairing codes, prompts, messages,
native request content, platform exception strings or protected file paths.

## Build and Test

Run `pnpm test` in the app. It builds the shared client, typechecks, runs unit,
component and isolated real HTTPS Go server fixtures, tests generated native
policy and beta candidate recovery, then builds production assets. Run
`node scripts/layout.mjs` with the existing Playwright module and browser channel
when required by the automated layout environment. Browser fixtures do not
establish actual mobile device, VoiceOver/TalkBack or native keyboard acceptance.

`pnpm build:ios:sim` builds Rust for `aarch64-apple-ios-sim` and the generated
Xcode application target without signing or launching a simulator. The pinned
cargo-mobile2 CLI incorrectly requires an installed runtime for a build-only
simulator archive; the owned build script uses the installed SDK through direct
xcodebuild after compiling and copying the exact Rust library. Remove this path
when the pinned CLI supports build-only simulator targets. `pnpm
build:android:emulator` builds the x86_64 Android APK through the pinned CLI.
Required mobile target build failures are blockers. Root `cargo test` remains
required for Rust changes. Hydrate required LFS assets before root compilation.
All generated native projects and `dist` directories stay untracked; remove
`dist` directories from final worktrees.

Beta candidates pin one source SHA and semantic app version, with explicit iOS
build and Android version-code inputs. Candidate manifests include identity,
architectures, expected signer fingerprints, original artifact byte lengths and
SHA-256 checksums. Conflicting candidate/version reuse is refused. Internal-only
preflight and immutable provider receipts precede any future upload. Unknown
upload outcomes require authoritative inspection of the original provider
operation; they cannot regenerate, re-sign or upload replacement artifacts.
The default dry run has no credential or provider access. Account setup, actual
signing/provisioning, store uploads and real device/account acceptance remain
owner-assigned and are not validation substitutes.

## Dependencies and Integrations

The app uses the generated `@delinoio/delidev-api-client`, Connect Query and
React Query. The shared client owns transport and read-only synchronization;
mobile owns protected credentials, foreground lifetime and explicit mutation
intent. No new business RPC, capability allocation or database migration is
added. The existing desktop and Go server interfaces remain compatible.

## Change Triggers

Update this contract, the app's AGENTS file, the DeliDev project index and the
shared client contract when mobile ownership, credential state, recovery,
platform minimums, native build policy or distribution authority changes.
Validation evidence belongs in PRs and CI artifacts, not tracked evidence files.

## References

- [DeliDev project](project-delidev.md)
- [Desktop ownership](apps-delidev-desktop-contract.md)
- [Shared client](packages-delidev-api-client-contract.md)
- [Sessions](cmds-delidev-sessions-contract.md)
- [Inbox](cmds-delidev-inbox-contract.md)
- [Repository defaults](repository-defaults.md)
- [Apple internal TestFlight groups](https://developer.apple.com/help/app-store-connect/test-a-beta-version/add-internal-testers)
- [Apple original build upload operations](https://developer.apple.com/documentation/appstoreconnectapi/build-uploads)
- [Google Play bundle hashes](https://developers.google.com/android-publisher/api-ref/rest/v3/edits.bundles)

## Server Setup

The server must already expose its configured HTTPS listener or trusted TLS
termination. Mobile never changes this listener or installs a VPN/tunnel. Append
`tauri://localhost` (iOS) and `http://tauri.localhost` (Android) to the existing
exact allowed-origin configuration while preserving other selected clients.
These origins permit browser CORS only; the original paired bearer remains
required for every protected RPC. Do not use wildcard origins or a TLS bypass.
VPN reachability remains user-managed. Add the complete existing client pairing
document to the named profile; its endpoint must match the selected HTTPS origin.

## Internal Beta Owner Setup

Create separate Apple and Google app records for `io.delino.delidev.mobile`.
Apple uses an internal TestFlight group whose `isInternalGroup` is true and whose
public link is disabled. Only App Store Connect team members belong to this
lane. Google uses the `internal` track and owner-selected internal testers.
Neither adapter exposes an external-testing or production target.

Protect the `delidev-mobile-beta` GitHub environment with trusted source branches
and required owner review. Keep the following non-secret variables there:

- `DELIDEV_MOBILE_APPLE_TEAM`: original ten-character Apple team identifier.
- `DELIDEV_MOBILE_APPLE_APP_ID`: numeric App Store Connect app identifier.
- `DELIDEV_MOBILE_APPLE_INTERNAL_GROUP`: original internal beta group identifier.
- `DELIDEV_MOBILE_GOOGLE_PRINCIPAL`: exact permitted service-account email.
- `DELIDEV_MOBILE_APPLE_SIGNER` and `DELIDEV_MOBILE_ANDROID_SIGNER`: lowercase
  SHA-256 fingerprints of the expected distribution/upload leaf certificates.

Configure only environment-owned secrets:

- `DELIDEV_MOBILE_IOS_P12` and `DELIDEV_MOBILE_IOS_PROFILE`: base64 distribution
  certificate/private-key archive and app-only distribution provisioning profile.
  `DELIDEV_MOBILE_IOS_P12_PASSWORD` unlocks the archive. The profile must bind the
  original team/app, with `get-task-allow` false. No DevHud/widget profile is used.
- `DELIDEV_MOBILE_ANDROID_KEYSTORE`: base64 original upload keystore;
  `DELIDEV_MOBILE_ANDROID_KEY_ALIAS`, `DELIDEV_MOBILE_ANDROID_KEY_PASSWORD` and
  `DELIDEV_MOBILE_ANDROID_STORE_PASSWORD` select its original signing key.
- `DELIDEV_MOBILE_APPLE_ISSUER`, `DELIDEV_MOBILE_APPLE_KEY_ID` and
  `DELIDEV_MOBILE_APPLE_PRIVATE_KEY`: the App Store Connect API issuer, key ID and
  PEM P-256 private key with access to the exact app/internal group.
- `DELIDEV_MOBILE_GOOGLE_SERVICE_ACCOUNT`: the protected service-account JSON
  with Android Publisher scope, exact configured principal and official OAuth
  token endpoint. Grant only the app/internal-testing access required by the lane.

The signing process uses a temporary keychain, restores the previous keychain
list and removes only its newly installed app profile and temporary credentials.
The Android signer and iOS leaf fingerprint, app identity, version, explicit
build/code, minimum platform and exact native architectures are checked against
actual signed artifacts. Bundletool 1.18.3 uses its recorded official SHA-256;
the app does not import DevHud release authority. Native/signing/upload tasks
have no shared caches. Hosted runners need Xcode with the iOS SDK, XcodeGen,
Android API 37/build-tools 36/NDK 29.0.14206865, JDK 21 and the declared Rust targets.
Account/provisioning setup and hosted signing acceptance are owner work.

## Future Live Procedure

Do not execute this procedure as ordinary validation. Select a trusted workflow
ref whose HEAD is the exact `source_sha`. The workflow rejects a different
workflow/source revision. Enter the same semantic `version`, positive explicit
`ios_build` and canonical positive `android_code` for both platforms.

1. Dispatch `dry-run` first. This mode has no store/signing secrets, no provider
   access and no publication. It runs frontend/shared-client fixtures and actual
   simulator/emulator builds. `pnpm beta:dry-run` is also a credential-free local
   submission-state fixture; it does not establish candidate signing.
2. After owner review, dispatch `package` with the same inputs. Both candidates
   come from the same source and version. iOS exports an arm64 IPA with
   `testFlightInternalTestingOnly`; Android produces arm64-v8a/armeabi-v7a AAB.
   The complete immutable artifact retains both bytes, metadata and source-bound
   SHA-256 manifest. Record its GitHub artifact ID. A package run cannot upload.
3. Dispatch `submit` with that exact `candidate_artifact_id`. The workflow checks
   the original repository/run/source/workflow and successful package result.
   Submission checks the retained candidate and never rebuilds or re-signs it.
   Apple validates the exact app/internal group, original BuildUpload/file SHA-256
   and `INTERNAL_ONLY` processed build before assignment. Google validates the
   exact principal, original bundle version/hash and `internal` release only.
4. Preserve the receipt artifact even when submission fails. After an uncertain
   response or Apple processing, dispatch `resume` against the original candidate
   and latest `receipt_artifact_id`. Original provider handles are retained before
   later byte operations. Resume first inspects the original provider state and
   accepts only the exact identity/version/hash; absent, ambiguous or conflicting
   proof fails without replacement upload. If a workflow interruption loses its
   receipt artifact, explicit resume uses the original candidate to seek exact
   authoritative version/hash proof and holds when this cannot be established.

Candidate reuse with different bytes, source, signer or version is rejected.
Apple processing completion and internal group membership are separate from
accepted upload bytes. Google edit/track commit and exact internal release are
separate from bundle upload. Neither provider receipt establishes tester
installation, human notification observation or actual beta availability.
Retain the original candidate and receipts beyond the configured artifact
retention before later recovery; expired artifacts must not be silently rebuilt.
