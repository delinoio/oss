# crates-devhud-native-messaging-host-contract

## Scope

`crates/devhud-native-messaging-host` is the canonical planned Rust workspace path for the signed DevHud Native Messaging host. It is a broker between Chrome and the running desktop app, not a general plugin runtime, and does not exist yet.

## Runtime and Language

Rust binary packaged with macOS, Windows, and Linux desktop installers. It must remain a planned path until its crate skeleton exists; only then may it be added as an explicit root Cargo workspace member. Use `tracing`-compatible structured logging.

## Users and Operators

Desktop DevHud users, Chrome extension users, installer/release operators, and security maintainers.

## Interfaces and Contracts

Register and connect using the stable Native Messaging host name `io.delino.devhud.native_messaging`. The Web Store extension ID is a fixed 32-character release-configured value shared by the extension package, host manifest, and desktop installer; accept only the exact origin `chrome-extension://<DEVHUD_CHROME_EXTENSION_ID>/`. Accept Chrome Native Messaging framing and validate the exact extension ID, Native Messaging origin, one-time pairing nonce, schema version, message size, and timeout. Forward only bounded, sanitized browser-context messages through user-scoped local IPC to `apps/devhud`; never call the API, GitHub, or R2. Maintain the `devhud` app identity and pairing lifecycle across install, logout, and removal.

## Storage

Pairing data is local device state only and is deleted on logout. No screenshots, page DOM, cookies, storage, tokens, PATs, R2 secrets, or Deck results are persisted by the host.

## Security

Use least-privilege host registration, origin and nonce validation, bounded input, timeouts, user-scoped IPC, and secure installer ownership. Native Wayland support and a third-party plugin ABI/SDK are excluded.

## Logging

Use redacted `tracing` diagnostics. Never log browser content, credentials, full sensitive URLs, tokens, or IPC payloads.

## Build and Test

Validate Rust format/clippy/unit tests, Native Messaging framing, origin/ID/nonce rejection, schema and size limits, timeout/disconnect behavior, IPC authorization, installer registration/removal, and signed host artifacts across supported desktop OS/architectures.

## Dependencies and Integrations

Integrates with `apps/devhud-chrome-extension`, `apps/devhud`, and desktop installers. It is independent of `servers/devhud-api`, `protos/devhud/v1`, GitHub, and R2.

## Change Triggers

Update the project index, app/extension contracts, `crates/AGENTS.md`, and root ownership/release rules when host path, framing, pairing, IPC, packaging, or platform support changes.

## References

- [DevHud project index](project-devhud.md)
- [Chrome extension contract](apps-devhud-chrome-extension-contract.md)
- [App contract](apps-devhud-foundation.md)
- [Repository defaults](repository-defaults.md)
