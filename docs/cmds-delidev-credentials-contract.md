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

Subscription `AccountLogin` read failures omit the private generation/reference ID from vault logs, retaining only safe operation, owner, purpose and closed error classification. API reads keep their original reference diagnostics. This redaction changes no native access, exact-reference selection, ownership or mutation behavior.

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

The attempt remains bound to its original paired client. The resident server
may accept the same retained attempt from a replacement client only after that
client authenticates and the original paired client is confirmed revoked.
Observation preserves the in-flight result; a new Keychain check still requires
an explicit Retry after a confirmed failure.

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

### Ordinary session gh authority

Issue #1857's bounded tool exception follows the [harness contract](cmds-delidev-harness-contract.md#ordinary-execution-github-cli-context). Only the executing Worker's in-memory gh directory selector reaches ordinary native session tool environments. gh owns all file/OS-store credential access and explicit configuration writes. DeliDev never reads, copies, logs, transfers or deletes those credentials/configuration, inherits `GH_TOKEN`/`GITHUB_TOKEN`, or substitutes server integration credentials. Native provider isolation and every protected execution/proxy exclusion remain unchanged; excluded auxiliary, inspection and Sidechat flows retain their isolation.

## Access-only server quota references — issue #1854

System 50 quota reads retain the exact current sealed AccountLogin reference independently of an Execute writer. Go validates its original account/user commitment and narrows native input to access token, account and plan; ID and refresh tokens remain in Go and never reach the quota native process or managed files. Admission/reference capture use short account serialization, never a lock across native/network work. Worker completion may rotate the current generation but must protect the quota operation's captured generation until independent cleanup. An uncertain quota owner fences protected-reference deletion and restore without marking an original execution lease failed. Authentication renewal and writeback remain exclusively owned by the original lifecycle/execution controller. Follow the [V2 quota contract](cmds-delidev-subscription-contract.md#server-owned-chatgpt-quota-v2--issue-1854).

### Subscription vault initialization

Server subscription maintenance acquires the shared account gate before lazy
vault initialization and before reading the exact original `AccountLogin`
generation. API account operations use the same gate and retained vault owner.
Cancellation before acquisition admits no vault or native work; failed opening
publishes no owner and permits a later original-authority retry. Release the gate
before native login/logout or waiting on its process. Original operation claims,
independent cleanup and joined shutdown keep their existing ownership.

## cmds/delidev-cli/internal/apiproxy constraints

- Keep native bodies, task arguments, buffered frames and credentials out of logs, persistence and public diagnostics. Cancellation, malformed/truncated streams and rejected calls discard private buffers without a direct fallback or provider retry.

## cmds/delidev-cli/internal/cli constraints

- Activity is retired. Keep it out of dispatch, help and client wiring; reject unknown commands before connection, credential input or state creation. Preserve other CLI commands and generated compatibility clients.

- Saved Worker network commands derive the exact private root and original credential from the saved connection. Ciphertext may enter bounded stdin independently of its authenticated digest, never alongside token/pairing input. Import output contains only public original IDs, exact decimal generation and ciphertext digest; omit vault references and reject future/malformed status shapes.

- The private resident `runtime.credentials` operation must use its original in-process server/vault and exact runtime/server/client/attempt ownership. A successful explicit local-registration recovery may rebind the retained attempt to the replacement client only after the original client is confirmed revoked; preserve the attempt/result and never restart a Keychain check automatically. Accept only closed Begin/Status/Retry/Skip payloads; reject extra arguments/scopes and foreign generations. Never create a second vault or add a public credential-check endpoint. Follow the startup Keychain section of the desktop/credential contracts.

- Short desktop Worker preparation/admission loads the private same-server runtime locator and proves the original generation before authentication. Preserve immutable client/Worker credentials and local-pairing proof, persist no-fallback following before admission, and use the existing Local Worker transport for reconnect and update polling/reporting. Missing, foreign or failed runtime proof never permits ordinary discovery or stale-address fallback.

## cmds/delidev-cli/internal/desktopruntime constraints

- This package owns private runtime publication/retirement, challenge proof and generation-bound authenticated encryption of the transport-only bearer copy. Restore it before existing server authentication; keep stable credential formats and public Connect unchanged. Keep stable device authority outside it, validate private files/canonical loopback origins and retire only the original generation. Preserve selected outbound transports; proof failure grants no direct/stale fallback. No public RPC or database migration is owned here.

## cmds/delidev-cli/internal/domain constraints

- Repository clone inputs accept only credential-free HTTPS, ssh:// and SCP-style SSH, with bounded portable folder names and no encoded path separators. Keep helper transports, local paths, controls and URL passwords/tokens outside that contract. When GitHub repository metadata accompanies a remote URL, it must match the parsed GitHub owner/name. GitHub source identities normalize only the GitHub owner/repository namespace case; generic hosts retain security-significant path and transport distinctions. GitHub picker entries use validated numeric/node/owner/name identity and constructed GitHub.com URLs; metadata observations never provide Git credentials or execution authority.

- Model discovery must apply raw/Base64 credential reflection checks to the retained decimal spelling of numeric context limits as well as retained strings; reject the complete catalog before publication on a match.

- Repository remote_url is required on explicit save/import and accepts credential-free HTTPS, SSH or SCP syntax. Empty checkouts are valid. Keep historical omitted URLs readable without migration, extraction or rewriting; pinned preparation source kinds remain closed enums.

## cmds/delidev-cli/internal/harness/codex constraints

- Ordinary native closure uses a three-second stdin EOF window with fenced writes and joined original input/process handles before forced cancellation. Preserve immediate failure/cancellation termination, independent credential scans, symlink refusal and all existing recovery fences. Native temporary helper destructor cleanup never authorizes an account recovery or altered completion receipt.

- Owned API proxies use only canonical authenticated ephemeral loopback URLs. Reconstruct proxy environment, pin and verify disabled system discovery plus all native shell exclusions before start/resume, and retain execution/local credentials only in transient protected forms. Keep upstream credentials in Go; native frame reflection fails before durable publication. Normal and title API profiles share this route ownership without granting subscription routing.

## cmds/delidev-cli/internal/harness/opencode constraints

- Keep structured logs to stable owner/action/phase/error metadata. Never log prompt/output, raw native content, paths, credentials or checkpoint bytes. Keep validation results in PR/issue/CI records, not repository evidence documents.

- Include the original private HTTP password in SSE and content-snapshot guards. Exact owned GET `/config` and `/provider` responses remain private credential-verification inputs; this exception grants no content publication or other route authority.

## cmds/delidev-cli/internal/nativeproxy constraints

- Keep upstream credentials in bounded Go memory and local credentials in the private native environment only. Do not persist, log or serialize either. Exact bypass selection belongs exclusively to the Go profile; no retries, ambient discovery or failure fallback.

- Use isolated controlled proxies and temporary state in tests; never real accounts, system trust mutations or user credential stores.

## cmds/delidev-cli/internal/outbound constraints

- Proxy credentials stay out of URLs, origin headers and raw diagnostics. Keep finite reflection protection bounded and describe its limits.

- Literal and encoded credential forms shorter than eight bytes require complete token boundaries; retain a complete candidate until the next byte or EOF establishes its right boundary. Preserve the preceding emitted byte across reads and treat independent headers as complete values.

- Check HTTP field names against case-folded protected forms because the HTTP transport canonicalizes them. Credential-bearing routes expose no response trailers or trailer announcements; keep the transport's original response private so EOF cannot repopulate a caller-visible trailer map.

- Fixtures use isolated loopback servers and test credentials; never user accounts or real network infrastructure.

- Proxy credential body guards incrementally decode valid JSON string escapes in names/values and JSON SSE data. Select SSE framing from its response media type, with bounded initial-field detection for typeless streams. Decode only joined data fields, ignore metadata for JSON decoding and reset decoder state at every record boundary; literal protection still covers all wire bytes. Retain only bounded decoded matching suffixes and incomplete escapes with their original wire offsets; preserve allowed wire bytes and short-token boundaries. Rejection closes the original response, and cancellation joins without releasing retained protected prefixes.

## cmds/delidev-cli/internal/providers constraints

- Preserve bounded API model IDs containing `~`, including OpenRouter latest aliases, without resolution or rewriting. Keep malformed/duplicate identity and credential-reflection rejection atomic. Credential/model-catalog failure diagnostics use closed stage/reason enums, never parser prose or response/model content; these log-only fields do not change RPCs or durable receipts.

## cmds/delidev-cli/internal/server constraints

- Revoked desktop eligibility must bind the entire credential, including token and pairing identity, to the private commitment retained after fresh authenticated local pairing or a prior immutable completed recovery receipt. Synchronize fresh proof before publishing the active credential or retiring the pending pairing journal; interruptions must retry that exact accepted pairing. Never reconstruct missing original proof from revoked metadata or ordinary reuse; legacy revoked scopes without proof remain recovery-required.

- Require and validate the original local desktop pairing journal and its request-bound grant before revoked eligibility or recovery intent. Both original credential and pairing commitments are mandatory on journal load; validate archived pairing ownership on retries and never reconstruct absent original pairing evidence.

- Enforce the optional desktop `--expected-endpoint` guard against the authenticated owner client before any recovery journal, archive or pairing mutation. The guard never selects another endpoint, and a mismatch preserves the original credential and all recovery state.

- Retain an omitted proxy credential only when mode, canonical host and port are unchanged. An authority edit clears the new profile's credential association unless fresh write-only input explicitly replaces it; preserve independently pinned old routes and their immutable generations until profile deletion. Name/bypass edits alone retain the same authority.

- Before a network credential write, synchronize one server-bound private publication intent with original actor/input and immutable reference. Exact retry preserves that generation. A new request requires authoritative receipt comparison and, for a proved unpublished generation, durable cleanup denial plus confirmed native removal before another write. Unknown receipt/cleanup proof blocks replacement; clearing an accepted intent must never delete its published credential.

- SSH setup uses InstallationService and joined ssh_setup_runtime maintenance. Exact host confirmation precedes protected credential consumption; external once-only claims survive database rollback. Cancel preserves uncertain remote authority, and reconciliation inspects only the original native operation. Never infer readiness from SSH exit or upload alone. Follow the SSH and signed-updates contracts.

- Protocol 2 saves schema-4 inline source routes atomically, deriving each source from every selected Account. Native IDs and metadata grant no credential/native/execution authority. Endpoint autocomplete is an owner/client read that pins original Account/Provider/connection revisions, borrows and clears only protected credentials, joins cancellation, and performs no validation probe, catalog save, resource write, event or receipt. Preserve System 42 / Worker 22 independently of startup 43/23.

## cmds/delidev-cli/internal/store constraints

- Private OpenCode process replacement requires an independently retained original closed checkpoint, fresh process/runtime/credential ownership and one durable predecessor-bound resume claim before native staging or launch. The supported profile is Build/Plan non-VCS text/reasoning plus the positively observed closed inline tool/interaction profiles defined below; do not adopt other tool, auxiliary or child state through it; single-root Git history additionally requires the positive snapshot profile below. Copy exact SQLite database/WAL/SHM bytes into the new private runtime without mutating the predecessor or importing old configuration/account files. Revalidate effective settings, sole original session, idle/pending inventories and all original ordered message/part digests before permitting one fresh input. Preserve full bounded lineage, forbid old request/message/part reuse and require explicit intent after Stop/failure. Temporary read authority ends on every path; history contradictions latch failure and cleanup uncertainty takes precedence. Public first-assignment journals still reject this private resume claim, and version-1 reports cannot grant continuation.

- Automatic reset-credit consent (#2123) is protected account JSON, not a migration. Managed restore explicitly clears standing consent while retaining original episode/operation and credential references under quarantine for independent cleanup. Portable transfer removes subscription authority. Never promote retained history, old-client omission or restore into automatic spending.

## cmds/delidev-cli/internal/tokenprices constraints

- Never log content, credentials, endpoints or user state. Fixture tests use isolated private temporary state and injected transports.

## cmds/delidev-cli/internal/worker constraints

- Explicit fixed Local Workers retain immutable pairing credentials while their transport follows the same server ID through the private desktop runtime locator. Probe each execution generation before bearer release; after opting in, missing runtime authority cannot fall back to an obsolete pairing address. Saved/remote Worker addresses and selected outbound route policy remain unchanged. Desktop shutdown never stops an independently executing Worker.

- Keep Worker request authorization endpoint-allowlisted. Only include `WatchAuxiliaryWork` for paired Worker credentials when its handler rechecks the current machine, instance and negotiated title capability; Worker credentials never gain owner product operations.

- GitHub PATs use the direct OS credential service under `cmds-delidev-credentials-contract.md`, separate from account wrapping material. Persist only scoped generation intents, keyed commitments and irreversible tombstones; never persist PAT payloads or unkeyed hashes. Match exact staged retries, refuse regeneration of a missing sealed entry, record denial before native deletion, and never let an old mutation affect a replacement generation. The protected server owner key is copied only for the store lifetime and cleared after joined closure. Public integration authorization, cancellation and receipts must be composed separately.

- Executable discovery is Worker-owned and generation-bound. An invalid explicit path never falls back to PATH; version probes use private per-harness runtimes, bounded streams/timeouts, no inherited account credentials and disabled automatic updates. A detected version does not prove a native protocol or selected-account capability. Ordinary tests must not probe user-installed harnesses. Reconstruct remote discovery diagnostics before persistence and reject stale discovery generations without overwriting newer selections.

- Follow `cmds-delidev-credentials-contract.md` for protected server secrets. Keep wrapping material in the native OS store, payloads authenticated-encrypted, and immutable request references/deletion markers durable across uncertainty. Never silently unlock, enumerate user credentials, replace a missing sealed key, or use a plaintext fallback. Native tests must use temporary keychains/UUID entries or an explicitly disposable Secret Service container; never lock a user's shared collection. A stored secret does not establish account readiness.

- Public first dispatch scans bounded eligible pages and must join server shutdown before state/credential closure. Atomically validate the selected configuration, current Worker lease, exact protocol-verified installation, original ready manifest and matching validated API connection before committing the first snapshot, FIFO claim, route and immutable job. No native/HTTP/credential work or interpretation of remote filesystem paths belongs inside that transaction. Failed checks roll back the entire prospective claim, then may publish a stable blocking reason without overwriting concurrent controls. Never requeue prior/uncertain executions or automatically resume paused/restored/recovered sessions. Explicit first Resume uses the same checks; later-turn Resume uses the separate exact-predecessor gate. Creation/control receipts join the current execution job without repeating selection or cancellation. Protocol/account checks authorize a bounded native attempt, never fabricated selected-model success.

- Every continuation owns fresh job/execution/process/runtime/outbox/credential identities. Reuse only the validated original private `CODEX_HOME` after the exact predecessor lease/checkpoint and native history/defaults checks; never rebuild missing evidence, adopt a new thread or resend prior input. Publication/relay/completion/control must target current selection, reject old authority and preserve exact prior receipt semantics. Check every retained terminal inbox source before binding a new native turn. Goal-absence resume metadata is content-free and grants no execution authority; populated/foreign goals remain unsupported until their own adapter. Keep stable typed publication-failure diagnostics without raw native content.

- Public completed-execution recovery is a separate actor/revision/execution-bound RPC and owning-Worker job. Freeze original assignment/server/device/instance/checkpoint comparison facts without prompts, responses or credentials. Recheck all terminal/input/Steer/interaction facts at result commit, retain original report identity, reconcile original job/session atomically and always preserve pause plus independent terminal outcome. Missing events, pending publication or unresolved responses cannot become completion proof. Replacement Worker inspection never restores old-instance authority, resends input or grants Resume; current execution authorization belongs to explicit Resume.

- OpenCode discovery owns a fresh authenticated loopback server with an ephemeral secret and exact nonzero selected port; never attach to a user TUI, remap after a conflict or inherit credentials/configuration. Keep its protocol runtime separate from version-probe side effects. Require the owned official SDK startup record, actual absent/wrong/original credential checks, exact healthy/version facts, empty global config and bounded static operation advertisements. HTTP uses only the original authority and closed global/static GETs without proxy/redirect/retry. Project initialization and its managed settings/plugin dependencies require separate execution authority; OpenAPI advertisements cannot grant account/session readiness. Join output drains and owned cleanup before deleting state, and keep secrets, native endpoints and diagnostic bodies out of logs/public metadata.

- OpenCode private API initialization must compare complete effective configuration before and after provider inspection, and require exactly the selected configured provider/model, scoped relay credential, native package and explicit capability/rejection profile. Native provider options contain credentials despite their public-info name: keep bodies private and log only bounded phase/status/code facts. Reject overrides, extra providers/models, ambient credential sources and changed native selection before any session claim. Native connected flags and zero default prices establish neither credential readiness nor actual cost; this read-only profile does not grant public execution or replace managed-policy/workspace/account validation.

- Install managed execution credential cleanup at the auth-file write boundary, before publisher/registration failure paths, including predecessor runtimes. Remove and synchronize only owned authentication after confirmed native closure or before any native ownership; unconfirmed startup/closure retains its recovery lease.

- Retained managed native history must pass the bounded cleanup scan for both raw tokens and padded/unpadded standard/URL Base64 copies before confirming credential removal. Retain original history and uncertain ownership when that scan fails.

## cmds/delidev-cli/internal/workernetwork constraints

- Keep recipient private keys and derivative contents in the existing OS-key-wrapped credential Vault. Ordinary metadata contains public recipient/reference, scope, generation and ciphertext digest only. No plaintext fallback, key regeneration after uncertain publication, ambient route discovery, older-profile fallback or offline readiness.

- Tests inject isolated protected stores and temporary state. Never read user credentials or access real proxy infrastructure.

## cmds/delidev-cli/internal/workspace constraints

- Repository Clone owns a separate original-job-bound private staging claim and has a ten-minute execution deadline. Capture the empty staging checkout's native identity before Git so completion cannot adopt its replacement. Use credential-free HTTPS/SSH URLs and the computer's existing Git credentials, with an explicit `origin` remote and no PAT, hooks or recursive submodules. Publish the validated checkout without replacing any destination. Cleanup requires the original claim, parent/staging native identities and joined process termination; uncertainty retains files for recovery. Published checkouts become user-owned Local folders and remain intact after registration failure or configuration deletion. This exception to snapshot-only scratch creation grants no snapshot/session deletion ownership.
