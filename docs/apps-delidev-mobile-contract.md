# DeliDev mobile client contract

## Scope

`apps/delidev-mobile` owns the separate iOS and Android remote client in the feature. Its identity is `io.delino.delidev.mobile`, and its display name is
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

Current authenticated server and Project observations supply initial Plan defaults.
Project inheritance and explicit overrides follow the session-defaults contract.
An explicit draft mode choice wins; an unavailable or stale default blocks
automatic creation until reinspection, while pending original requests retain
their captured mode. Definitive request errors preserve the creation draft.
Granular notification generations expose the existing twelve situations and
write only the selected situation against its captured client revision. Legacy
servers retain their combined-category compatibility; neither shape rewrites
an uncertain protected request.

Every profile pins its explicit HTTPS origin, server identity, client device
identity and original pairing request. Pairing accepts the existing version-1
client grant document only when its HTTPS endpoint matches the explicit origin.
The original token and request are stored before the first pairing call. Receipt
retry reuses them; it cannot obtain a second grant, device or machine.

One unresolved mutation per profile is durably stored before sending, using the
original generated protobuf JSON and exact request ID. Create, Send, Steer,
Stop, Resume, question, approval, Inbox read-state, notification preferences,
notification claim/report and self-revocation use closed operation dispatch.
Pending records retain optional Prepared/Sending/Uncertain provenance beside
unchanged original protobuf JSON. Persist Sending before dispatch. Only the first
Prepared dispatch of Create, Send, Steer, Question, Approval, Inbox read-state or
notification preferences can clear its exact pending record on a versioned
InvalidArgument from the method's pre-acceptance or rolled-back validation path.
Control, notification Claim/Report and Revoke do not use this allowlist.
PermissionDenied, NotFound, Conflict and post-commit observation errors retain
uncertainty. A lost response retains the original request and marks Uncertain;
restored Sending, legacy records without provenance and prior-Uncertain retries
never clear on a later rejection. Protected write failure retains the request
and reports recovery. Exact profile credentials/identity and operation/request/
target checks fence replacements; correction drafts and other profiles stay
unchanged. No RPC, capability or storage-version migration is added.
Foreground return and connection refresh perform reads only. Explicit retry requires inspection of current
server state and confirmation; it never changes a retained request or selection.
Steer operates on an already queued input and its original revision, execution
and native turn. Stop and Resume require current observations and confirmation.
Question and approval responses retain the original interaction revision.
Protected-answer questions remain unavailable outside their original supported
client boundary. Inbox opens the current original entry and interaction before
showing response controls; notification delivery never changes read state.

Conversation queue inputs and interaction requests have separate explicit
50-record pagination chains. Each continuation uses the exact returned cursor
and keeps original resource order, IDs and revisions. Loaded closed requests
remain counted; an unvisited continuation never implies that no request remains.
Refresh reads the reached chain atomically, using its new original cursors.
Failed reads retain the last valid observations, show incomplete coverage and
allow explicit read retry or refresh without replaying a mutation. Duplicate
IDs, repeated/nonadvancing cursors, incorrect session/kind/schema/revision and
oversized pages fail closed. Queue coverage stops at 1,000 inputs; interaction
coverage stops at 10,000 records, with a 16 MiB combined document bound. A cursor
beyond a limit remains explicitly incomplete rather than implying exhaustion.
Reading, incomplete or failed target observations disable response and Steer
controls. Backgrounding, conversation replacement and profile replacement cancel
reads and fence late completions; protected pending requests remain untouched.

Language, System/light/dark theme, safe-area padding, software keyboard resize,
48-pixel targets, native dialogs, keyboard access and opener focus return belong
to the mobile client. English and Korean catalogs have identical keys. Narrow
layouts and 200% effective reflow preserve drafts and readable content. No
remote fonts or external renderer assets are required.

### Application integration

- `apps/delidev-mobile` owns the separate HTTPS remote client and protected internal beta lane. Follow `apps-delidev-mobile-contract.md`; mobile uses Wry and never starts a server, Worker or desktop sidecar. Device credentials and original unresolved requests remain in platform-protected storage.

### apps/delidev-mobile constraints

- Follow `../../docs/apps-delidev-mobile-contract.md` and the DeliDev project,
  shared API-client, sessions, Inbox, protocol and structure contracts.

- This app is a separate HTTPS remote client. Never add a Go/Worker/desktop
  sidecar, CEF, local execution or new business RPC to the platform bridge.

- Keep credentials and original pairing/mutation intent in device-only native
  protected state. Foreground synchronization is read-only. Unknown mutations
  require current original-target inspection and exact explicit retry.

- iOS minimum is 18; Android minimum API 31. Use the pinned Wry runtime and
  verified repository Tauri CLI. Native target build checks are mandatory.

- Keep generated native projects and all `dist` output untracked. Run `pnpm test`
  here for frontend changes and root `cargo test` for Rust changes. Automated
  browser/native builds do not establish real mobile device/account acceptance.

- The internal beta pipeline defaults to dry run. Preserve original candidate
  bytes, versions, source SHA, signer and checksum evidence. Unknown upload
  outcomes require exact authoritative reconciliation. Never publish a public
  track, provision owner accounts or invent credentials as part of fixtures.

- New session inherits current authenticated server/Project Plan defaults until an explicit mode choice. Missing, stale or invalid defaults retain drafts and require reinspection; frozen pending requests retain their original mode. Notification preference writes retain the original client revision and closed situation selection; legacy servers retain the combined-category compatibility shape.

- Persist all selected candidate-bound platform receipts before provider access. Missing recovery receipts remain Unknown. Apple proof requires nested COMPLETE state and complete bounded group pagination; Google staged edit membership is not distribution proof. Mark observation edits writable before any track mutation, commit the original edit, and reconcile exact published bytes without replacement uploads.

- Beta target defaults to both platforms. Explicit iOS-only candidates use schema 2 and require no Android code or credentials; preserve schema-1 both-platform candidates and exact target-bound provenance. Serialize all beta workflow runs without canceling original submissions. Bind candidate and receipt downloads to their independently verified original run IDs.

- Direct iOS Cargo builds must pass the configured minimum system version to the Swift linker; do not rely on its iOS 13 fallback or a warm local build.

- Android beta build hosts install the exact SDK package `platforms;android-37.0` with command-line tools 16111833; the integer-only `android-37` package is absent from the official inventory. This build SDK does not alter the API 31 runtime minimum.

- Apple IPA commits send only `uploaded: true`. Its returned MD5 corroborates locally reverified SHA-256 bytes only for retained candidate/upload/file receipt ownership and complete exact-size files; no handleless MD5 adoption. Save positive original transfer proof before metadata commit. Explicit repaired resume pins the reviewed recovery-code SHA independently while checking the original clean candidate source and immutable bytes; no rebuild, re-sign or replacement upload.

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

Beta provider failures record only provider, HTTP method, numeric status and
closed transport outcome. Durable checkpoints report platform and stage; no
provider URL, response body or raw exception is recorded.

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
when the pinned CLI supports build-only simulator targets. Direct Cargo
builds also pass the configured iOS minimum explicitly to the pinned Tauri Swift
linker, which otherwise falls back to iOS 13 before the app config is applied. `pnpm
build:android:emulator` builds the x86_64 Android APK through the pinned CLI.
Required mobile target build failures are blockers. Root `cargo test` remains
required for Rust changes. Hydrate required LFS assets before root compilation.
All generated native projects and `dist` directories stay untracked; remove
`dist` directories from final worktrees.

Beta candidates pin one source SHA and semantic app version, with explicit iOS
build and, for the default both-platform target, Android version-code inputs.
An explicit `ios` target omits Android inputs and credentials. It produces a
schema-2 manifest with exactly the iOS artifact; the default `both` target retains
the schema-1 manifest. Provenance pins the target and cannot reinterpret a
candidate across targets. All beta runs share one non-canceling concurrency group
to serialize edits against the configured Google principal. Candidate manifests include identity,
architectures, expected signer fingerprints, original artifact byte lengths and
SHA-256 checksums. Conflicting candidate/version reuse is refused. Internal-only
preflight and immutable provider receipts precede any future upload. All selected platform receipts are stored before any provider is contacted. A
definitive preflight failure retains the unsubmitted platform as Ready; missing
receipts during recovery remain Unknown. Apple proof reads the nested processing
state and the complete bounded internal-group build inventory. The IPA endpoint
rejects optional SHA-256 commit attributes and returns an MD5 file checksum.
Keep the candidate's source/signature/SHA-256 verification; corroborate those
bytes only through the original receipt-bound BuildUpload/file ID, exact length,
completed file and matching returned MD5. Never adopt a handleless match using
MD5. Retain original file SHA-256/length and positive complete-transfer proof
before committing `uploaded: true`; explicit recovery may finish that same
metadata commit, but uncertain transfers cannot resend or replace bytes. Google proof
distinguishes a new read-only edit from staged membership in the original
writable edit; only committed internal distribution can complete the receipt.
Unknown upload outcomes require authoritative inspection of the original provider
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

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

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
Neither adapter exposes an external-testing or production target. The Google
account must be active, and a new app requires its first owner-controlled binary
upload and required legal consents through Play Console before Publisher API
submission can operate. An inactive or terminated account cannot be repaired by
workflow retries.

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
  PEM P-256 private key whose role permits the exact app/internal group operations.
  Apple team keys apply to every team app and have no app-specific access limit;
  owner approval must cover that scope before a new key is created. The adapter
  still pins the configured DeliDev app and internal group.
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
Android API 37.0 (`platforms;android-37.0`), command-line tools 16111833,
build-tools 36/NDK 29.0.14206865, JDK 21 and the declared Rust targets.
Account/provisioning setup and hosted signing acceptance are owner work.

## Future Live Procedure

Do not execute this procedure as ordinary validation. Select a trusted workflow
ref whose HEAD is the exact `source_sha`. The workflow rejects a different
workflow/source revision. For an explicitly repaired `resume` only, pin the
reviewed workflow revision with `recovery_sha` while `source_sha` remains the
original candidate revision. Check out and verify both clean revisions
independently. Recovery code reads the original candidate from the original
checkout and cannot package, submit a fresh candidate, rebuild or re-sign.
Receipt provenance accepts only the original or exact selected recovery revision;
receipt content must still bind the original candidate. Enter the same semantic `version`, positive explicit
`ios_build`. The default `both` target also requires canonical positive
`android_code`; select `ios` and leave Android code empty for Apple-only work.
Apple-only execution requires only Apple variables and secrets.

1. Dispatch `dry-run` first. This mode has no store/signing secrets, no provider
   access and no publication. It runs frontend/shared-client fixtures and actual
   simulator/emulator builds. `pnpm beta:dry-run` is also a credential-free local
   submission-state fixture; it does not establish candidate signing.
2. After owner review, dispatch `package` with the same inputs. Both candidates
   come from the same source and version. iOS exports an arm64 IPA with
   `testFlightInternalTestingOnly`; Android produces arm64-v8a/armeabi-v7a AAB.
   The complete immutable artifact retains selected platform bytes, metadata and source-bound
   SHA-256 manifest. Record its GitHub artifact ID. A package run cannot upload.
3. Dispatch `submit` with that exact `candidate_artifact_id`. The workflow checks
   the original repository/run/source/workflow and successful package result.
   Download the candidate and latest receipt from their independently verified
   original run IDs; never search the current submission run for retained artifacts.
   Submission checks the retained candidate and never rebuilds or re-signs it.
   Apple validates the exact app/internal group, original receipt-owned upload/file,
   locally verified candidate SHA-256, returned file checksum and `INTERNAL_ONLY`
   processed build before assignment. Google validates the
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
