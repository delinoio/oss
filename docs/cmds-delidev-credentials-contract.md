# DeliDev protected credential storage

## Shared credentials across API format generations

Capability 9 uses the account's server-owned connection `credential_id`, falling
back to the original connection ID for existing accounts. Every retained/current
generation points to that same account-owned `account-api` reference. Resolve a
connection only after proving its immutable execution or inspection authority;
clients never receive protected material or manage references. Format saving does
not read, write, enumerate or delete protected keys. OAuth metadata and serialized
refresh use the original reference ID, preserving once-only exchange and receipts.
Disconnect clears all generations before the existing joined removal/enumeration
procedure; pending cleanup blocks changes and replacement. Follow the
[account generation contract](cmds-delidev-accounts-contract.md#connected-api-format-changes).


OAuth seals only `Ref{Owner:reservedAccountID, ID:originalConnectID, Purpose:account-api}` after a durable staging claim. Keep account-less references and cleanup evidence across uncertain Put/SQLite outcomes. Explicit original local completion may read that reference without another exchange; missing/tombstoned material cannot be replaced. Cancel records denial and cleanup before removal, retains disconnected metadata and never claims upstream revocation. Follow the [OAuth contract](cmds-delidev-account-oauth-contract.md).

## Scope and ownership

Issue #964 and [the CLI contract](cmds-delidev-contract.md) require authoritative credentials on the server, outside SQLite, transcripts, snapshots, ordinary RPC responses, logs, Worker environments and harness configuration. `cmds/delidev-cli/internal/credentials` implements the protected storage primitive. API connect/disconnect and cleanup now use this primitive through the [account lifecycle contract](cmds-delidev-accounts-contract.md). Subscription login, provider validation, execution revocation, proxy use and full deletion/restore coordination remain separate work; this primitive alone does not grant execution readiness. No CLI/RPC secret read operation is exposed.

Only an authenticated server lifecycle operation may use the primitive. Callers own account revision checks, request receipt coordination and authorization revalidation. They must retain staged references after an uncertain database commit, compare them with authoritative state during reconciliation, and never speculate that a failed response means no credential was saved. A SQLite backup or non-secret configuration export is not a credential backup.

Network profile writes additionally retain one server-bound private publication
intent before calling this primitive, under the [network contract](cmds-delidev-network-contract.md).
It binds original actor/input and immutable owner/request reference without
credential bytes. Exact retry may resume that generation. A distinct request
must compare the original authoritative SQLite receipt; a proved unpublished
generation enters durable cleanup denial and must be removed before another
native write. Unknown receipt or native cleanup proof blocks replacement. An
accepted receipt permits clearing only the publication intent, preserving the
credential for its published profile and pinned routes. The private intent is
not a credential backup or a public profile.

A network credential association belongs to the profile's exact mode, canonical
host and port. Omitted write-only input retains it only while that authority is
unchanged; authority edits clear the new association unless explicit input creates
a fresh generation. Name/bypass edits may retain it. Already pinned routes keep
their original authority and immutable generation until coordinated profile
deletion; an authority edit alone cannot delete a credential needed by them.

The vault holds an exclusive private directory lock and pins its server UUID-v7 identity. A missing identity pin cannot rebind a populated vault. Under the exclusive lock, first-open recovery may discard only bounded private regular `.pending-<decimal>` atomic-write scratch files at an otherwise empty vault root; it validates every entry before removal and synchronizes cleanup. Owner directories, links, unknown names or oversized files retain recovery-required state. References comprise an owner UUID-v7, mutation request UUID-v7 and closed purpose (`account-api`, `account-login`, `network-proxy`, `worker-ssh`). Aliases, emails, provider URLs and user-selected filesystem paths are not native credential names. A replacement receives a fresh mutation ID. Private directory/file ownership, permissions and non-symlink checks apply to every access. Running independent copies of the same server identity against the same native references is outside the single-authority contract.

GitHub PATs now have the separate direct native storage primitive below. The account envelope primitive does not store PAT payloads. Public PAT configuration/validation, credential import, backup restoration and provider login require their separate lifecycle composition. macOS authentication UI follows the native policy below.

The [doctor inspection boundary](cmds-delidev-diagnostics-contract.md) may open only an existing private vault with its original lock and server pin. It holds a non-creating exclusive lock, performs no scratch reconciliation or scope initialization, rejects writes/deletes and closes after inspection. Reading an exact sealed reference proves decryptability only; it never establishes provider readiness or enumerates unrelated native credentials.

## Envelope and persistence

Each reference accepts 1–65,536 bytes. Its native OS record contains exactly 64 bytes: a random 256-bit wrapping root and a 256-bit keyed content commitment. Domain-separated HMAC-SHA256 derivation produces independent encryption and commitment keys. AES-256-GCM protects the payload with a fresh 96-bit nonce. Authenticated context binds the format/service version, server scope, owner, mutation and purpose. The private JSON record contains only that context, state and ciphertext. Neither a raw secret nor an unkeyed digest is persisted. The keyed commitment can bind internal receipt metadata; it is not an execution credential.

| State | Permitted behavior |
| --- | --- |
| `staged` | Durable non-secret intent precedes the first native write. A retry reconciles the exact native record and content commitment before publishing ciphertext. Reads report recovery required. |
| `sealed` | Reads require the exact native material and valid authenticated ciphertext. Identical writes reuse the bytes and commitment; different content conflicts. Missing material never causes automatic key regeneration. |
| `deleting` | The private record has an irreversible deletion marker and no ciphertext. Reads and writes cannot use the reference. Native deletion may still require an available/unlocked OS store; retry uses the same reference. |
| `deleted` | Native removal has completed. Repeated deletion succeeds; no later write can reuse the reference. |

Native write acceptance and file publication are separate crash boundaries. Cancellation or an OS error may leave a staged intent and native key; these survive for exact retry. Deletion records its marker before removing native material and reports completion only after the final marker is durable. If a saved native key disappears, sealed reads/writes fail explicitly instead of silently replacing an account credential. Malformed records, altered authenticated context, wrong keys, ambiguous native matches and invalid material lengths also fail closed.

Owner reference enumeration reads only this private vault's metadata, including staged/deleting/deleted references. A separate unremoved-reference query excludes completed tombstones for account deletion and cleanup guards. It never lists a user's OS credentials. Interrupted atomic-write scratch files are not promoted by filename guesses. Deletion markers must participate in the future restore/deletion coordinator; restoring old state must never authorize a removed credential.

Returned secret bytes belong to their caller and must be cleared promptly after bounded use. Buffer clearing reduces memory retention; it does not promise an OS sandbox, locked memory or protection against unrestricted same-user access to the server process.

## Platform adapters

- **macOS:** native `Security.framework`/`CoreFoundation.framework` bindings through pinned purego, with exact service/account matching and no credential-bearing child command. The server process enables OS authentication UI once during framework initialization through `SecKeychainSetUserInteractionAllowed(true)`. Queries omit the authentication-UI attribute and use the OS default allow policy. File-keychain interaction is process-wide; no request temporarily toggles it. This policy applies to account wrapping keys and GitHub PATs, including background execution, schedules, cleanup and Doctor reads. Calls retain/release Core Foundation values synchronously, preserve completed writes through cancellation races, and map native statuses to typed errors. When authentication is required, macOS presents its own prompt in the server user's session. Approval continues the same original native call; cancellation, denial or an unavailable interactive session retains typed failure and the original retry/reconciliation state. DeliDev does not collect, read or change a login keychain password and adds no automatic authentication retry. Executable verification remains independent and precedes native access. See [Apple keychain APIs](https://developer.apple.com/documentation/security/keychains) and [interaction policy](https://developer.apple.com/documentation/security/seckeychainsetuserinteractionallowed%28_%3A%29).
- **Windows:** exact generic Credential Manager records, persistent for the current user on the machine. The vault lock and existence check prevent ordinary retry replacement. Only fixed-size wrapping material reaches `CredWrite`; larger OAuth documents remain encrypted in private files. No credential enumeration or interactive credential UI is used. See [CredWrite](https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credwritew).
- **Linux:** Secret Service over an already running local Unix user bus, with bounded connection/call deadlines, kernel-verified peer UID and EXTERNAL authentication. TCP/autolaunch/multiple/ambiguous bus addresses are rejected. The adapter uses the existing default collection, exact application/reference attributes and a private bus connection per operation. It never creates a collection, launches a bus/keyring, calls an unlock prompt, or falls back to disk keys. The standard `plain` Secret Service session transfers wrapping material over this local bus; the account payload itself never enters D-Bus. A locked collection/item returns confirmation required. Closing the private bus invalidates any pending prompt and closes its session. MIME type is presentation metadata: GNOME Keyring returns `text/plain` for binary values, so retrieval validates the native session, plain-session parameters and exact binary size while retaining the original byte array. See the [Secret Service specification](https://specifications.freedesktop.org/secret-service/latest-single/) and [GNOME Keyring native serialization](https://github.com/GNOME/gnome-keyring/blob/main/daemon/dbus/gkd-secret-secret.c).

Unavailable services return a typed unavailable error. On macOS, canceled, denied or disallowed authentication returns confirmation required with platform-specific guidance; native calls remain synchronous and retain their original objects until completion. Cancellation cannot forcibly dismiss an OS-owned prompt or prove that a native mutation did not complete. Linux and Windows retain their noninteractive policy and typed errors. No platform falls back to plaintext storage. A missing sealed key or invalid envelope returns recovery required. Logs contain only operation, opaque owner/reference, purpose and stable error code; no raw native errors, payloads, provider identities or filesystem paths are logged.

## Desktop startup access confirmation

Each fresh macOS desktop process presents a startup notice after its local
connection is authenticated and automatically checks existing connected API
credentials through its app-owned resident server. The closed private
`runtime.credentials` operation shares that server's vault; it introduces no
public RPC, capability, credential format or database migration. Borrowed
CLI/service servers and saved remote connections grant no local vault access.
Other platforms retain their existing noninteractive policy.

The native connector owns one original attempt across renderer remounts and
local product windows. Its server/runtime generation and original paired client
are checked before admission; server reads recheck client revocation. Paginated
account inventory selects only current connected credential-bearing API
accounts without pending removal, resolves each immutable selected provider
profile and reads its exact current protected reference. Disconnected,
subscription and keyless accounts do not open the vault. Resolve the shared
`CredentialReferenceID` for both the protected key and OAuth metadata so a
key-preserving API-format change retains the same reference. Validate OAuth
metadata against its immutable managed OAuth profile, then read the current
private token directly even when its refresh checkpoint is claimed,
recovery-required or denied. This check does not resume or mutate that
checkpoint, refresh, exchange or clean up another generation. Every returned
secret buffer is cleared, including on failure. No provider request,
account-health write, revision or receipt change is permitted. Access success
proves decryptability only, not provider readiness.

Repeated observation reuses the original attempt, including unknown replies.
Terminal failures require an explicit Retry that names the exact failed attempt
shown in the invoking window; native code rejects a stale failure if another
window has advanced the attempt. The new attempt remains bound to its original
predecessor; status and restart do not retry authentication.
Continue without checking cancels later reads and retains a skipped observation.
A synchronous OS-owned prompt may remain open until its original native call
completes. Server shutdown cancels and joins this work before closing its vault;
the existing desktop 35-second original-child shutdown boundary remains intact.
Logs contain only original operation IDs, closed phases and sanitized error codes.

## Verification and remaining evidence

macOS checks current code with `SecCodeCopySelf` and `SecCodeCheckValidity` before each native credential read/write and fresh OAuth admission/exchange. `errSecCSStaticCodeChanged` (-67034) returns recovery required with closed cause `credential_executable_changed`, independently of keychain authentication. Other code-verification failures also require recovery with `credential_executable_invalid`. A rejected fresh OAuth Start retains the existing `oauth_start_not_admitted` cause only after its admission transaction rolls back; its runtime diagnosis remains in structured logs. This check reads/unlocks no keychain. Original OAuth replay, status, cancellation and native deletion retain their independent authority; original references and once-only exchange receipts are preserved. Logs contain only the fixed executable-change reason or OAuth phase/error code. Repair follows [local development signing](apps-delidev-desktop-contract.md#local-development-signing-and-recovery), using original-item authorization and explicit server replacement, never key regeneration or another exchange.

The macOS process fixture replaces a running ad-hoc executable and checks its separate code-change error. It also compiles two distinct executables, signs both with a synthetic certificate imported only into its named temporary keychain, and checks original wrapping-material access across builds. The fixture passes that identity directly to the native signing API, with the development identifier/runtime flag, then uses strict codesign verification; CLI identity discovery requires the user's search list and remains separate registration evidence. It removes the complete owned keychain and changes no default keychain, search list or global trust. Real existing-item authorization, provider approval/inference and installed-platform lifetime remain separate acceptance evidence.

Ordinary deterministic tests inject a test-only in-memory native backend, never a production fallback. Real private filesystem tests cover restart/retry, concurrent conflicting writes, uncertain native commits, cancellation after a native write, bounds, altered envelopes, missing keys, scope mismatch, symlink/shared-permission rejection, deletion while locked and delayed writes after deletion. Raw secret values and untrusted error text must not appear in files/logs.

Automatic native macOS tests run in separate test processes that disable interaction once after framework initialization, create a new password-protected temporary keychain and direct every query to it. The suppression is test-only and cannot be selected by a production environment variable. Unit tests verify production authentication admission, exact query matching and native error classifications without accessing any keychain. They never query the default/login keychain. They cover native create/read/duplicate/delete, locked-store refusal, explicit temporary-keychain unlock and larger envelope payloads. Manual interactive acceptance additionally requires an isolated temporary Keychain to verify prompt display, approval completing the original request, cancellation followed by retry of the same reference, and Doctor reading the original sealed reference. Automated fixtures do not establish interactive or installed-app acceptance. Windows tests target only fresh UUID credential names and delete those temporary entries; native Windows execution remains a required evidence item until run on Windows.

The opt-in `TestMacInteractiveTemporaryKeychain` fixture creates and
deletes only its new isolated keychain. Run it in an interactive macOS session
with `DELIDEV_MAC_INTERACTIVE_FIXTURE=1 go test ./cmds/delidev-cli/internal/credentials -run '^TestMacInteractiveTemporaryKeychain$' -count=1 -v`.
Cancel the first prompt, then approve the explicit retry with the fixture's
public test password `delidev-isolated-test-password`. This selector exists only
in test source and changes no production policy. An unavailable prompt or a
confirmation-required result is a failed interactive acceptance, not an approval
proof. The retained parent removes the owned keychain after the child exits.

Linux native tests require an explicitly disposable Secret Service session. The fixture under `cmds/delidev-cli/internal/credentials/testdata/secret-service` creates a non-root container user, private home/runtime, private D-Bus session and temporary GNOME Keyring. The fixed test password protects only that discarded fixture. The final locked-state test intentionally leaves test wrapping material in the locked collection; container removal discards the entire fixture. Never opt into this test against an ordinary user's shared collection.

Example from the repository root, choosing the Docker daemon's native `amd64` or `arm64` architecture:

```sh
delidev_fixture_dir=$(mktemp -d)
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c ./cmds/delidev-cli/internal/credentials -o "$delidev_fixture_dir/credentials.test"
cp cmds/delidev-cli/internal/credentials/testdata/secret-service/Dockerfile "$delidev_fixture_dir/"
cp cmds/delidev-cli/internal/credentials/testdata/secret-service/run.sh "$delidev_fixture_dir/"
docker build -t delidev-secret-service-test:local "$delidev_fixture_dir"
docker run --rm --network none delidev-secret-service-test:local
rm -rf "$delidev_fixture_dir"
```

Cross-compilation does not establish native behavior. Passing a temporary keychain/Secret Service test does not establish account login, upstream key validation, provider inference, proxy authorization or full OS service lifecycle acceptance. Record those boundaries separately in pull requests, issues and CI logs/artifacts.

## Direct GitHub PAT storage

`PATStore` uses a distinct `io.delino.delidev.github.pat.v1` native service on macOS Keychain, Windows Credential Manager and Linux Secret Service. It writes the exact 1–512 visible ASCII token bytes directly into that OS service. The existing account wrapping service retains its exact 64-byte format and namespace; neither profile can select the other's entries. PAT names bind only server UUID, profile UUID and immutable generation UUID. This primitive reads no default GitHub/system credential and exposes no public secret-reading operation.

The private metadata directory reuses the server-scope pin, exclusive lock, private ownership checks and joined close boundary, without using account envelope serialization. Each generation has only `staged`, `sealed`, `deleting` or `deleted` metadata. A domain-separated HMAC under the caller's existing protected server owner key binds the exact initial token to its staged intent; the key, token and unkeyed token digest never enter metadata. The store copies that key into its own lifetime and clears it after joined closure. Callers clear returned token buffers after bounded use and retain authorization/revision/mutation receipt ownership outside this primitive.

Before the first native write, persist the original staged intent. Native acknowledgment loss reconciles only the exact generation and original bytes. A sealed generation whose native entry disappears returns recovery required and is never recreated by retry. Deletion persists a denial tombstone before touching the OS store, so locked/unavailable native cleanup leaves a retryable marker that already refuses Get/Put. Completed tombstones cannot be reused, and replayed deletion cannot remove a newer generation. Metadata-only cleanup enumeration scans this profile's owned records in bounded batches with cancellation and rejects more than 256 unresolved generations without returning a partial list. No native credential enumeration is used.

Both profiles share exact-match, redacted-error and platform session constraints. macOS allows OS-owned authentication prompts as specified above; Linux and Windows retain their existing noninteractive behavior. Public profile metadata, current GitHub identity/access validation, immediate cancellation of active integration requests, replacement/delete coordination, RPC/CLI and desktop settings still require the server integration layer; primitive success alone proves none of those capabilities.


The direct PAT primitive is now composed by [IntegrationService and its CLI/desktop clients](cmds-delidev-integrations-contract.md). That layer supplies actor-bound reference receipts, durable denial before native replacement/deletion, exact pending-generation recovery and joined identity-inspection cancellation. Native storage success remains separate from GitHub identity and repository feature authorization.
