# crates-devhud-native-messaging-host-contract

## Scope

`crates/devhud-native-messaging-host` is the implemented Rust workspace crate for the signed DevHud Native Messaging host. It is a broker between Chrome and the running desktop app, not a general plugin runtime.

## Runtime and Language

Rust binary packaged as a Tauri sidecar with macOS, Windows, and Linux desktop installers and registered per user when the app launches. It is an explicit root Cargo workspace member and uses redacted `tracing` diagnostics. Windows NSIS removal invokes the host's idempotent `unregister` command and aborts before deleting installer-owned files when that command fails or the cleanup executable is missing; a missing per-user registry key is successful idempotent cleanup, while any other registry deletion failure fails unregister. Linux creates the non-secret `~/.local/share/io.delino.devhud/native-messaging-pairing-v1` marker before persisting a pairing secret and removes it only after credential cleanup succeeds. Debian package removal discovers affected users through that marker independently of optional per-user Chrome registration, retains the Chrome manifest as a compatibility fallback, enters each affected account through its active `/run/user/<uid>` session, and invokes `unregister` with that user's home, runtime directory, and D-Bus address before deleting the package-owned system manifest or binaries. An unavailable user session or failed revocation aborts removal, and unregister retains the marker and registration until live-session revocation and pairing-secret deletion succeed so cleanup remains retryable. The same command supports explicit removal on every desktop platform.

## Users and Operators

Desktop DevHud users, Chrome extension users, installer/release operators, and security maintainers.

## Interfaces and Contracts

Apply one absolute five-second deadline to host-side IPC connection establishment plus framed authentication, and a fresh absolute five-second deadline to every forwarded read/write. Recompute the remaining Unix timeout across connect and partial operations, and use cancellable overlapped Windows client I/O. Accept configuration only when its payload fits inside both complete 256 KiB IPC and Chrome response envelopes.

Revalidate browser-context text at the native boundary and reject titles, user agents, or individual accessibility values above 4 KiB of UTF-8 as invalid browser context. Require the submitted sanitized URL string to equal its canonical parsed serialization so dot segments and other noncanonical originals cannot survive validation into persisted drafts.

IPC authentication is mutual: the host proves the fresh challenge and the app returns a distinct secret-bound proof over that challenge and the new session ID. The proof binds a typed authentication purpose: browser sessions retain pairing-nonce/completion checks and cannot revoke pairing, while pairing-revocation sessions omit the pairing nonce and can send only the IPC-only revocation control message. Pairing completion is serialized with secret rotation and written only while the authenticated generation remains current. If the initial authenticated result is lost after completion persists, the host checks the shared completion marker and retries once without the consumed pairing nonce. If an authenticated connection was closed while idle or invalidated by an app generation change, the host reauthenticates and retries the pending request once; after pairing authentication succeeds, that retry also omits the consumed pairing nonce and authenticates against the completed pairing. Framed IPC read/write failures produce `disconnected`, while generation-invalidated sessions use a distinct internal retry signal; both trigger reauthentication. App authorization failures remain `denied`, invalid browser context and malformed app envelopes remain `malformed`, and other logical rejections preserve the healthy authenticated session. When a pairing secret exists, the idempotent unregister command uses the revocation-only scope to make a running app invalidate active generations and delete pairing credentials before unregister reports success, including while first pairing is pending; when the app endpoint is absent, the host performs the credential deletion directly.

Register and connect using the stable Native Messaging host name `io.delino.devhud.native_messaging`. The Web Store extension ID is a fixed 32-character release-configured value shared by the extension package, host manifest, and desktop installer; accept only the exact origin `chrome-extension://<DEVHUD_CHROME_EXTENSION_ID>/`. The test fixture identity is enabled only when `DEVHUD_EXTENSION_TEST_BUILD` is exactly `1`. Accept Chrome Native Messaging framing and validate the exact extension ID, Native Messaging origin, one-time pairing nonce, schema version, a shared maximum of 256 KiB for the UTF-8 JSON body measured before length-prefix framing/parsing, and timeout. The app owns the versioned `v1` IPC envelope and listener; the host connects as a client over a per-user endpoint: `$XDG_RUNTIME_DIR/devhud.sock` on Linux, `~/Library/Application Support/io.delino.devhud/run/devhud.sock` on macOS, and `\\.\pipe\io.delino.devhud.ipc` on Windows. Unix socket connection attempts are nonblocking and deadline-bound, and sockets use mode `0600`; the Windows named pipe ACL permits only the current user and uses overlapped I/O with the same absolute connection/authentication deadline followed by an absolute five-second deadline for each framed read or write. When every current named-pipe instance is busy, wait for another instance using only the remaining connection deadline and retry the open without resetting that deadline. Authenticate the connection with an app-issued pairing secret kept in platform secure storage and a fresh challenge/response before accepting length-prefixed UTF-8 JSON messages containing `version`, `request_id`, `type`, `payload`, and authentication proof. Forward only bounded, sanitized browser-context messages through this IPC; requests have a five-second deadline, unsupported versions and failed authentication are rejected, and logout, account deletion, or removal invalidates the pairing secret. Never call the API, GitHub, or R2. Maintain the `devhud` app identity and pairing lifecycle across install, logout, and removal.

### Rust component integration

- `crates/devhud-native-messaging-host`: implemented Rust Native Messaging host for DevHud and explicit workspace member.

- Keep the canonical path at `crates/devhud-native-messaging-host` and document behavior in `crates-devhud-native-messaging-host-contract.md`.

- The real crate skeleton is an explicit root workspace member. The host is a bounded Chrome-to-desktop broker, not a plugin SDK or API/GitHub/R2 client.

- Preserve Native Messaging origin/extension-ID/nonce/schema/timeout validation, a shared 256 KiB UTF-8 JSON body ceiling measured before length-prefix framing/parsing, user-scoped IPC, redacted `tracing` diagnostics, and the supported desktop OS/architecture matrix. Host connection establishment and authentication share one absolute five-second deadline on every platform. On Linux, create the non-secret per-user removal marker before persisting a pairing secret and remove it only after credential cleanup succeeds so Debian removal never depends on optional Chrome registration for affected-user discovery.

- The host-to-app IPC is an app-owned, versioned v1 length-prefixed JSON protocol over the documented per-user Unix socket or Windows named pipe, authenticated with a platform-secure pairing secret and challenge/response; it is independent of Connect RPC. Keep pairing retries nonce-free after successful pairing authentication, keep revocation unavailable to Chrome-originated message types, and require the secret-bound revocation-only authentication scope to invalidate the live app generation before unregister reports success, including while first pairing is pending. Unregister must delete pairing credentials before removing its per-user registration so failed cleanup remains retryable, and Debian removal must run it through each affected active user session before package-owned files are removed.

- DevHud native-host IPC and registration fixtures must remain callable from the package-local DevHud CI commands. Native binaries, installers, signing, release, and deployment tasks are non-cacheable and CI must not publish or install outside disposable layouts.

- Windows configuration publication must copy the destination DACL/protection to staging through a retained security handle, then use one handle-based rename for the commit. Retain both READ_CONTROL and WRITE_DAC on staging while copying the DACL because the setter also inspects its inheritance state. Close the destination inspection handle after copying its DACL and before the commit so publication cannot block its own rename. Permission failures must log only the operation enum and numeric OS error under debug logging. Retain delete access before copying restrictive permissions so failure/cancellation cleans staging; never use multi-step `ReplaceFileW` for concurrent publication. New Unix configuration outputs must restore mode 0600 after creation regardless of caller umask. Configuration file output must use staging on the destination filesystem, keep Unix staging behind an owner-only directory (0700 without macOS ACL grants) through permission copying and rename, preserve destination access permissions without requiring content-read access to a write-only Unix output, reject replacement links, clean up on handled failures/cancellation, and never duplicate stdout. Concurrent replacement tests must verify complete successful results and cleanup while allowing Windows sharing failures; verify blocked replacement preserves the destination and succeeds after the blocking handle closes. Configuration failures must retain static actionable stderr guidance and redacted classifications/positions when tracing filters disable error events. Failed diagnostic writes must not panic, expose raw fallback errors, or override command/cancellation status. YAML source diagnostics count CR, LF, and CRLF as single line breaks and columns by Unicode scalar value. Keep configuration diagnostics enum-classified and exclude keys, values, content, paths, argv, and dependency/panic text even with RUST_LOG.

- Preserve already-tokenized child quotes/backslashes without assignment unescaping, safe Windows batch argv dispatch, partial port-enumeration errors, process/ownership revalidation before forceful termination, a shared five-second verification wait, and cancellation that leaves opened applications running. Validate clipboard text completely before replacement/output and retain Linux ownership with an installed background tool; never install tools automatically.

- pnport Linux `recvmsg` must permit ordinary IPC with optional control buffers through a task-private zero-control-capacity header. Copy only successful output fields, preserve caller pointers/control bytes and native faults, and restore the original argument before interruption or restart. Actual ancillary data remains unsupported and must never install an untracked descriptor, including during peer-thread execution or graceful cleanup. Keep `recvmmsg` control-buffer reception denied before execution and retain dynamic/static native-parity and descriptor-transfer rejection fixtures.

- pnport Linux arm64 syscall denial must update `NT_ARM_SYSTEM_CALL` after ordinary registers so rejected operations cannot execute during graceful cleanup.

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

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References

- [DevHud project index](project-devhud.md)
- [Chrome extension contract](apps-devhud-chrome-extension-contract.md)
- [App contract](apps-devhud-foundation.md)
- [Repository defaults](repository-defaults.md)
