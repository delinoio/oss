# crates-devhud-native-messaging-host-contract

## Scope

`crates/devhud-native-messaging-host` is the implemented Rust workspace crate for the signed DevHud Native Messaging host. It is a broker between Chrome and the running desktop app, not a general plugin runtime.

## Runtime and Language

Rust binary packaged as a Tauri sidecar with macOS, Windows, and Linux desktop installers and registered per user when the app launches. It is an explicit root Cargo workspace member and uses redacted `tracing` diagnostics. Windows NSIS removal invokes the host's idempotent `unregister` command and aborts before deleting installer-owned files when that command fails or the cleanup executable is missing; a missing per-user registry key is successful idempotent cleanup, while any other registry deletion failure fails unregister. The same command supports explicit removal on every desktop platform.

## Users and Operators

Desktop DevHud users, Chrome extension users, installer/release operators, and security maintainers.

## Interfaces and Contracts

Apply one absolute five-second deadline to host-side IPC connection establishment plus framed authentication, and a fresh absolute five-second deadline to every forwarded read/write. Recompute the remaining Unix timeout across connect and partial operations, and use cancellable overlapped Windows client I/O. Accept configuration only when its payload fits inside both complete 256 KiB IPC and Chrome response envelopes.

Revalidate browser-context text at the native boundary and reject titles, user agents, or individual accessibility values above 4 KiB of UTF-8 as invalid browser context. Require the submitted sanitized URL string to equal its canonical parsed serialization so dot segments and other noncanonical originals cannot survive validation into persisted drafts.

IPC authentication is mutual: the host proves the fresh challenge and the app returns a distinct secret-bound proof over that challenge and the new session ID. The proof binds a typed authentication purpose: browser sessions retain pairing-nonce/completion checks and cannot revoke pairing, while pairing-revocation sessions omit the pairing nonce and can send only the IPC-only revocation control message. Pairing completion is serialized with secret rotation and written only while the authenticated generation remains current. If the initial authenticated result is lost after completion persists, the host checks the shared completion marker and retries once without the consumed pairing nonce. If an authenticated connection was closed while idle or invalidated by an app generation change, the host reauthenticates and retries the pending request once; after pairing authentication succeeds, that retry also omits the consumed pairing nonce and authenticates against the completed pairing. Framed IPC read/write failures produce `disconnected`, while generation-invalidated sessions use a distinct internal retry signal; both trigger reauthentication. App authorization failures remain `denied`, invalid browser context and malformed app envelopes remain `malformed`, and other logical rejections preserve the healthy authenticated session. When a pairing secret exists, the idempotent unregister command uses the revocation-only scope to make a running app invalidate active generations and delete pairing credentials before unregister reports success, including while first pairing is pending; when the app endpoint is absent, the host performs the credential deletion directly.

Register and connect using the stable Native Messaging host name `io.delino.devhud.native_messaging`. The Web Store extension ID is a fixed 32-character release-configured value shared by the extension package, host manifest, and desktop installer; accept only the exact origin `chrome-extension://<DEVHUD_CHROME_EXTENSION_ID>/`. The test fixture identity is enabled only when `DEVHUD_EXTENSION_TEST_BUILD` is exactly `1`. Accept Chrome Native Messaging framing and validate the exact extension ID, Native Messaging origin, one-time pairing nonce, schema version, a shared maximum of 256 KiB for the UTF-8 JSON body measured before length-prefix framing/parsing, and timeout. The app owns the versioned `v1` IPC envelope and listener; the host connects as a client over a per-user endpoint: `$XDG_RUNTIME_DIR/devhud.sock` on Linux, `~/Library/Application Support/io.delino.devhud/run/devhud.sock` on macOS, and `\\.\pipe\io.delino.devhud.ipc` on Windows. Unix socket connection attempts are nonblocking and deadline-bound, and sockets use mode `0600`; the Windows named pipe ACL permits only the current user and uses overlapped I/O with the same absolute connection/authentication deadline followed by an absolute five-second deadline for each framed read or write. When every current named-pipe instance is busy, wait for another instance using only the remaining connection deadline and retry the open without resetting that deadline. Authenticate the connection with an app-issued pairing secret kept in platform secure storage and a fresh challenge/response before accepting length-prefixed UTF-8 JSON messages containing `version`, `request_id`, `type`, `payload`, and authentication proof. Forward only bounded, sanitized browser-context messages through this IPC; requests have a five-second deadline, unsupported versions and failed authentication are rejected, and logout, account deletion, or removal invalidates the pairing secret. Never call the API, GitHub, or R2. Maintain the `devhud` app identity and pairing lifecycle across install, logout, and removal.

## Storage

Pairing data is local device state only and is deleted on logout or account deletion. The app invalidates the in-memory pairing nonce, cached context, and active session generation before secure-storage deletion, so a storage cleanup error cannot preserve a live authenticated session. Changed valid configuration clears cached context; a changed renderer identity-scope UUID also clears context and advances the active session generation even when configuration is identical, and rejected replacement configuration clears the prior authorization snapshot. No screenshots, page DOM, cookies, storage, tokens, PATs, R2 secrets, or Deck results are persisted by the host.

## Security

Use least-privilege host registration, origin and nonce validation, bounded input, timeouts, user-scoped IPC, and secure installer ownership. Native Wayland support and a third-party plugin ABI/SDK are excluded.

## Logging

Use redacted `tracing` diagnostics. Never log browser content, credentials, full sensitive URLs, tokens, or IPC payloads.

## Build and Test

Validate Rust format/clippy/unit tests, Native Messaging framing, origin/ID/nonce rejection, schema and the identical 256 KiB pre-framing size limit, timeout/disconnect behavior, IPC authorization, installer registration/removal, and signed host artifacts across supported desktop OS/architectures.

## Dependencies and Integrations

Integrates with `apps/devhud-chrome-extension`, `apps/devhud`, and desktop installers. It is independent of `servers/devhud-api`, `protos/devhud/v1`, GitHub, and R2.

## Change Triggers

Update the project index, app/extension contracts, `crates/AGENTS.md`, and root ownership/release rules when host path, framing, pairing, IPC, packaging, or platform support changes.

## References

- [DevHud project index](project-devhud.md)
- [Chrome extension contract](apps-devhud-chrome-extension-contract.md)
- [App contract](apps-devhud-foundation.md)
- [Repository defaults](repository-defaults.md)
