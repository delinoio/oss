# apps-devhud-chrome-extension-contract

## Scope

`apps/devhud-chrome-extension` is the planned signed Chrome Manifest V3 extension that supplies optional, permission-scoped browser context to desktop RealQA. It is not implemented.

## Runtime and Language

Manifest V3 TypeScript/JavaScript extension with English/Korean user-facing UI and fixed DevHud identifiers. It is distributed through the Chrome Web Store and as a reproducible validation ZIP.

## Users and Operators

Desktop RealQA users, Chrome Web Store reviewers, and release operators.

## Interfaces and Contracts

Use the stable Native Messaging host name `io.delino.devhud.native_messaging`. The Web Store extension ID is a fixed 32-character release-configured value shared by the extension package, host manifest, and desktop installer; the host origin must be exactly `chrome-extension://<DEVHUD_CHROME_EXTENSION_ID>/`. Request optional host permissions only after a user gesture and only for configured URL-mapping origins; never request `<all_urls>`. Use `activeTab` where applicable. Maintain a persistent Native Messaging connection to `crates/devhud-native-messaging-host`, which connects to the app-owned per-user IPC endpoint using its authenticated versioned `v1` envelope. The host uses `$XDG_RUNTIME_DIR/devhud.sock` on Linux, `~/Library/Application Support/io.delino.devhud/run/devhud.sock` on macOS, or the current-user Windows named pipe `\\.\pipe\io.delino.devhud\ipc`; the app owns the message schema and rejects failed challenge/response, unsupported versions, UTF-8 JSON bodies larger than 256 KiB before framing/parsing, and expired requests. Validate the exact extension ID, Native Messaging origin, one-time pairing nonce, schema, shared pre-framing message-size ceiling, and timeout.

Capture only a normalized URL, title, viewport, user agent, selected bounds, accessibility attributes, and sanitized `outerHTML` capped at 128 KiB. Do not capture a DOM-derived CSS selector: bounds are the only selection locator because selector IDs, classes, and attribute values may contain secrets. Accept only `http` and `https` page URLs; remove user-info credentials, query strings, and fragments, lowercase the host, omit default ports, and replace every non-empty persisted path segment with `<redacted>`. Do not add URL-mapping exceptions for query parameters. Build the DOM fragment from an explicit element allowlist (`a`, `article`, `aside`, `blockquote`, `code`, `dd`, `details`, `div`, `dl`, `dt`, `em`, `figcaption`, `figure`, `footer`, `h1`-`h6`, `header`, `hr`, `img`, `li`, `main`, `nav`, `ol`, `p`, `pre`, `section`, `summary`, `table`, `tbody`, `td`, `tfoot`, `th`, `thead`, `tr`, `ul`) and an explicit attribute allowlist limited to the approved accessibility/text attributes (`alt`, `aria-describedby`, `aria-hidden`, `aria-label`, `aria-labelledby`, `role`, `title`). Drop all other elements and attributes, including `meta`, `link`, scripts, styles, event handlers, `data-*`, form controls, and hidden/password fields. Redact every URL-valued attribute (`href`, `src`, `cite`, `poster`, `ping`, `srcset`, and form URL attributes) rather than preserving query strings, fragments, signed URLs, or credentials. Never collect cookies, storage, console/network data, or page-wide DOM. Missing, denied, revoked, disconnected, unsupported-incognito, malformed, or absent extension must degrade to capture without browser context and manual repository selection. Chrome incognito support is excluded.

## Storage

Store only non-secret extension configuration and pairing state required by the app; pairing data remains local and is removed on logout. Do not persist page captures beyond the app’s draft/submission contract.

## Security

Origin permissions are least-privilege and user initiated. Pairing is one-time and timeout/schema bounded; every participant enforces the same 256 KiB UTF-8 JSON body limit before framing/parsing. Browser context is editable/removable, and top-level URLs always use the normalized origin plus the redacted path structure defined below with credentials, query strings, and fragments removed.

The path component is redacted before persistence or submission: replace every non-empty segment with the literal `<redacted>` placeholder while preserving slash structure. This prevents reset tokens, invite secrets, capability URLs, and other path credentials from entering drafts, issues, or agent input; literal path values are never retained.

## Logging

Use redacted structured diagnostics. Never log page content, credentials, cookies, storage, tokens, or complete URLs with sensitive query/fragment data.

## Build and Test

Validate manifest permissions, extension ID/origin pairing, malformed/replayed/oversized messages including the shared 256 KiB pre-framing ceiling, no-active-tab and sensitive-form behavior, absent/denied/revoked permissions, unsupported incognito behavior, element/attribute allowlist enforcement, selector omission, path-segment redaction, URL-value redaction (including signed URLs and `data-*` secrets), sanitized DOM cap, reproducible ZIP parity, and Web Store packaging.

## Dependencies and Integrations

Integrates with the Native Messaging host, DevHud local IPC, configured URL mappings, and desktop RealQA only. It never calls `devhud-api` or GitHub directly.

## Change Triggers

Update the project index, app/native-host contracts, `apps/AGENTS.md`, and root/domain ownership rules when extension permissions, message schema, pairing, packaging, or privacy boundaries change.

## References

- [DevHud project index](project-devhud.md)
- [Native Messaging host contract](crates-devhud-native-messaging-host-contract.md)
- [App contract](apps-devhud-foundation.md)
- [Repository defaults](repository-defaults.md)
