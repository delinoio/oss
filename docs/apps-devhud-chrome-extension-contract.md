# apps-devhud-chrome-extension-contract

## Scope

`apps/devhud-chrome-extension` is the planned signed Chrome Manifest V3 extension that supplies optional, permission-scoped browser context to desktop RealQA. It is not implemented.

## Runtime and Language

Manifest V3 TypeScript/JavaScript extension with English/Korean user-facing UI and fixed DevHud identifiers. It is distributed through the Chrome Web Store and as a reproducible validation ZIP.

## Users and Operators

Desktop RealQA users, Chrome Web Store reviewers, and release operators.

## Interfaces and Contracts

Use the stable Native Messaging host name `io.delino.devhud.native_messaging`. The Web Store extension ID is a fixed 32-character release-configured value shared by the extension package, host manifest, and desktop installer; the host origin must be exactly `chrome-extension://<DEVHUD_CHROME_EXTENSION_ID>/`. Request optional host permissions only after a user gesture and only for configured URL-mapping origins; never request `<all_urls>`. Use `activeTab` where applicable. Maintain a persistent Native Messaging connection to `crates/devhud-native-messaging-host`, which connects to the app-owned per-user IPC endpoint using its authenticated versioned `v1` envelope. The host uses `$XDG_RUNTIME_DIR/devhud.sock` on Linux, `~/Library/Application Support/io.delino.devhud/run/devhud.sock` on macOS, or the current-user Windows named pipe `\\.\pipe\io.delino.devhud\ipc`; the app owns the message schema and rejects failed challenge/response, unsupported versions, oversized messages, and expired requests. Validate the exact extension ID, Native Messaging origin, one-time pairing nonce, schema, message size, and timeout.

Capture only privacy-stripped URL, title, viewport, user agent, selected selector/bounds, accessibility attributes, and sanitized `outerHTML` capped at 128 KiB. Build the DOM fragment from an explicit element allowlist (`a`, `article`, `aside`, `blockquote`, `code`, `dd`, `details`, `div`, `dl`, `dt`, `em`, `figcaption`, `figure`, `footer`, `h1`-`h6`, `header`, `hr`, `img`, `li`, `main`, `nav`, `ol`, `p`, `pre`, `section`, `summary`, `table`, `tbody`, `td`, `tfoot`, `th`, `thead`, `tr`, `ul`) and an explicit attribute allowlist limited to the approved accessibility/text attributes (`alt`, `aria-describedby`, `aria-hidden`, `aria-label`, `aria-labelledby`, `role`, `title`). Drop all other elements and attributes, including `meta`, `link`, scripts, styles, event handlers, `data-*`, form controls, and hidden/password fields. Redact every URL-valued attribute (`href`, `src`, `cite`, `poster`, `ping`, `srcset`, and form URL attributes) rather than preserving query strings, fragments, signed URLs, or credentials. Never collect cookies, storage, console/network data, or page-wide DOM. Missing, denied, revoked, disconnected, unsupported-incognito, or absent extension must degrade to capture without browser context and manual repository selection. Chrome incognito support is excluded.

## Storage

Store only non-secret extension configuration and pairing state required by the app; pairing data remains local and is removed on logout. Do not persist page captures beyond the app’s draft/submission contract.

## Security

Origin permissions are least-privilege and user initiated. Pairing is one-time and timeout/size/schema bounded. Browser context is editable/removable and query/fragment stripping follows URL-mapping privacy policy.

## Logging

Use redacted structured diagnostics. Never log page content, credentials, cookies, storage, tokens, or complete URLs with sensitive query/fragment data.

## Build and Test

Validate manifest permissions, extension ID/origin pairing, malformed/replayed/oversized messages, no-active-tab and sensitive-form behavior, absent/denied/revoked permissions, unsupported incognito behavior, element/attribute allowlist enforcement, URL-value redaction (including signed URLs and `data-*` secrets), sanitized DOM cap, reproducible ZIP parity, and Web Store packaging.

## Dependencies and Integrations

Integrates with the Native Messaging host, DevHud local IPC, configured URL mappings, and desktop RealQA only. It never calls `devhud-api` or GitHub directly.

## Change Triggers

Update the project index, app/native-host contracts, `apps/AGENTS.md`, and root/domain ownership rules when extension permissions, message schema, pairing, packaging, or privacy boundaries change.

## References

- [DevHud project index](project-devhud.md)
- [Native Messaging host contract](crates-devhud-native-messaging-host-contract.md)
- [App contract](apps-devhud-foundation.md)
- [Repository defaults](repository-defaults.md)
