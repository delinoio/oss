# DeliDev protected credential storage

## Scope and ownership

Issue #964 and [the CLI contract](cmds-delidev-contract.md) require authoritative credentials on the server, outside SQLite, transcripts, snapshots, ordinary RPC responses, logs, Worker environments and harness configuration. `cmds/delidev-cli/internal/credentials` implements the protected storage primitive. Account login/connect/disconnect, provider validation, execution revocation, proxy use and deletion coordination remain separate work; this primitive alone does not connect an account or grant execution readiness. No CLI/RPC secret read operation is exposed.

Only an authenticated server lifecycle operation may use the primitive. Callers own account revision checks, request receipt coordination and authorization revalidation. They must retain staged references after an uncertain database commit, compare them with authoritative state during reconciliation, and never speculate that a failed response means no credential was saved. A SQLite backup or non-secret configuration export is not a credential backup.

The vault holds an exclusive private directory lock and pins its server UUID-v7 identity. A missing identity pin cannot rebind a populated vault. References comprise an owner UUID-v7, mutation request UUID-v7 and closed purpose (`account-api`, `account-login`, `network-proxy`, `worker-ssh`). Aliases, emails, provider URLs and user-selected filesystem paths are not native credential names. A replacement receives a fresh mutation ID. Private directory/file ownership, permissions and non-symlink checks apply to every access. Running independent copies of the same server identity against the same native references is outside the single-authority contract.

GitHub PATs require their own direct OS credential-store lifecycle under the issue contract. The envelope primitive does not implement PAT integration, credential import, platform unlock UI, backup restoration, or provider login.

## Envelope and persistence

Each reference accepts 1–65,536 bytes. Its native OS record contains exactly 64 bytes: a random 256-bit wrapping root and a 256-bit keyed content commitment. Domain-separated HMAC-SHA256 derivation produces independent encryption and commitment keys. AES-256-GCM protects the payload with a fresh 96-bit nonce. Authenticated context binds the format/service version, server scope, owner, mutation and purpose. The private JSON record contains only that context, state and ciphertext. Neither a raw secret nor an unkeyed digest is persisted. The keyed commitment can bind internal receipt metadata; it is not an execution credential.

| State | Permitted behavior |
| --- | --- |
| `staged` | Durable non-secret intent precedes the first native write. A retry reconciles the exact native record and content commitment before publishing ciphertext. Reads report recovery required. |
| `sealed` | Reads require the exact native material and valid authenticated ciphertext. Identical writes reuse the bytes and commitment; different content conflicts. Missing material never causes automatic key regeneration. |
| `deleting` | The private record has an irreversible deletion marker and no ciphertext. Reads and writes cannot use the reference. Native deletion may still require an available/unlocked OS store; retry uses the same reference. |
| `deleted` | Native removal has completed. Repeated deletion succeeds; no later write can reuse the reference. |

Native write acceptance and file publication are separate crash boundaries. Cancellation or an OS error may leave a staged intent and native key; these survive for exact retry. Deletion records its marker before removing native material and reports completion only after the final marker is durable. If a saved native key disappears, sealed reads/writes fail explicitly instead of silently replacing an account credential. Malformed records, altered authenticated context, wrong keys, ambiguous native matches and invalid material lengths also fail closed.

Owner reference enumeration reads only this private vault's metadata, including staged/deleting/deleted references. It never lists a user's OS credentials. Interrupted atomic-write scratch files are not promoted by filename guesses. Deletion markers must participate in the future restore/deletion coordinator; restoring old state must never authorize a removed credential.

Returned secret bytes belong to their caller and must be cleared promptly after bounded use. Buffer clearing reduces memory retention; it does not promise an OS sandbox, locked memory or protection against unrestricted same-user access to the server process.

## Platform adapters

- **macOS:** native `Security.framework`/`CoreFoundation.framework` bindings through pinned purego, with exact service/account matching and no credential-bearing child command. The server process disables optional file-keychain UI through `SecKeychainSetUserInteractionAllowed(false)` in addition to the per-query authentication-failure option. File-keychain calls can otherwise request UI despite that option. Calls retain/release Core Foundation values synchronously, preserve completed writes through cancellation races, and map native statuses to typed errors. Users unlock/authorize their store outside the server; the adapter neither reads nor changes a login keychain password. See [Apple keychain APIs](https://developer.apple.com/documentation/security/keychains) and [interaction policy](https://developer.apple.com/documentation/security/seckeychainsetuserinteractionallowed%28_%3A%29).
- **Windows:** exact generic Credential Manager records, persistent for the current user on the machine. The vault lock and existence check prevent ordinary retry replacement. Only fixed-size wrapping material reaches `CredWrite`; larger OAuth documents remain encrypted in private files. No credential enumeration or interactive credential UI is used. See [CredWrite](https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credwritew).
- **Linux:** Secret Service over an already running local Unix user bus, with bounded connection/call deadlines, kernel-verified peer UID and EXTERNAL authentication. TCP/autolaunch/multiple/ambiguous bus addresses are rejected. The adapter uses the existing default collection, exact application/reference attributes and a private bus connection per operation. It never creates a collection, launches a bus/keyring, calls an unlock prompt, or falls back to disk keys. The standard `plain` Secret Service session transfers wrapping material over this local bus; the account payload itself never enters D-Bus. A locked collection/item returns confirmation required. Closing the private bus invalidates any pending prompt and closes its session. MIME type is presentation metadata: GNOME Keyring returns `text/plain` for binary values, so retrieval validates the native session, plain-session parameters and exact binary size while retaining the original byte array. See the [Secret Service specification](https://specifications.freedesktop.org/secret-service/latest-single/) and [GNOME Keyring native serialization](https://github.com/GNOME/gnome-keyring/blob/main/daemon/dbus/gkd-secret-secret.c).

Unavailable services return a typed unavailable error. Authentication UI requirements return confirmation required, without hanging on an implicit prompt. A missing sealed key or invalid envelope returns recovery required. Logs contain only operation, opaque owner/reference, purpose and stable error code; no raw native errors, payloads, provider identities or filesystem paths are logged.

## Verification and remaining evidence

Ordinary deterministic tests inject a test-only in-memory native backend, never a production fallback. Real private filesystem tests cover restart/retry, concurrent conflicting writes, uncertain native commits, cancellation after a native write, bounds, altered envelopes, missing keys, scope mismatch, symlink/shared-permission rejection, deletion while locked and delayed writes after deletion. Raw secret values and untrusted error text must not appear in files/logs.

Native macOS tests create a new password-protected temporary keychain and direct every query to it. They never query the default/login keychain. They cover native create/read/duplicate/delete, locked-store refusal, explicit temporary-keychain unlock and larger envelope payloads. Windows tests target only fresh UUID credential names and delete those temporary entries; native Windows execution remains a required evidence item until run on Windows.

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

Cross-compilation does not establish native behavior. Passing a temporary keychain/Secret Service test does not establish account login, upstream key validation, provider inference, proxy authorization or full OS service lifecycle acceptance. The [evidence ledger](cmds-delidev-evidence.md) records those boundaries separately.
