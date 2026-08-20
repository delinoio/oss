# apps-devhud-chrome-extension-contract

## Scope

`apps/devhud-chrome-extension` is the implemented signed Chrome Manifest V3 extension that supplies optional, permission-scoped browser context to desktop RealQA. It is a bounded context picker, not a page observer or general browser automation surface.

## Runtime and Language

Manifest V3 TypeScript/JavaScript extension with English/Korean user-facing UI and fixed DevHud identifiers. The popup marks the document with the resolved Chrome UI language so assistive technology uses the matching pronunciation rules, and each configured-origin permission action has a localized accessible name containing its concrete origin. After successful pairing, it immediately refreshes configured origins so their permission actions are available without reopening the popup. It is distributed through the Chrome Web Store and as a reproducible validation ZIP.

Release builds require `DEVHUD_CHROME_EXTENSION_ID` and `DEVHUD_CHROME_EXTENSION_PUBLIC_KEY`; the build verifies that the public key derives the configured 32-character ID. `DEVHUD_EXTENSION_TEST_BUILD=1` selects the committed deterministic fixture identity for tests only through a platform-neutral Node launcher. Every build removes prior TypeScript output before compilation. Sorted inputs, fixed timezone-independent DOS ZIP calendar fields, and pinned dependencies make identical inputs byte-identical without retaining deleted or renamed modules from a reused workspace.

## Users and Operators

Desktop RealQA users, Chrome Web Store reviewers, and release operators.

## Interfaces and Contracts

Configured Chrome origins are published only when their scheme, host, and normalized port are covered by the associated mapping matcher. Configuration must leave enough of the shared 256 KiB ceiling for both the app IPC and Chrome Native Messaging response envelopes.

Before every page injection, verify that Chrome still grants the configured origin's scheme-and-host match pattern; Chrome host permission patterns cannot encode a port, so separately require the tab's exact configured origin, including its normalized port, before injection and again during mapping/application authorization. `activeTab` alone is insufficient. The service worker reconciles optional HTTP(S) grants after every successful configuration refresh, including capture refreshes while the popup is closed, and removes grants for origins that are no longer configured; it does not interpret a failed, disconnected, malformed, or uninitialized refresh as an empty authoritative configuration. After interactive selection, refresh the current configuration before selecting the mapping from the complete captured URL so SPA navigation and concurrent settings changes cannot use a stale mapping, then discard that transient URL rather than sending it to the host. Interactive selection keeps pointerdown, mousedown, pointerup, mouseup, and click inside an isolated full-viewport iframe presented through a modal top-layer dialog so existing page dialogs, popovers, fullscreen elements, and inertness cannot bypass the shield, then closes the shield before coordinate-based hit testing identifies the underlying element without activating it. Candidate discovery walks at most 10,000 page elements and degrades to capture without browser context and manual repository selection when the bound is exceeded. The same surface is a localized focusable dialog: it starts on the previously focused eligible element or the first eligible element, cycles the allowlisted visible candidates in DOM order with `Tab`/`Shift+Tab`, confirms with `Enter` or `Space`, announces and visibly highlights the current candidate, cancels with `Escape`, and restores prior focus. Abandoned interactive selections cancel and remove the selection surface and listeners after 30 seconds.

Use the stable Native Messaging host name `io.delino.devhud.native_messaging`. The Web Store extension ID is a fixed 32-character release-configured value shared by the extension package, host manifest, and desktop installer; the host origin must be exactly `chrome-extension://<DEVHUD_CHROME_EXTENSION_ID>/`. Request optional host permissions only after a user gesture and only as the Chrome-valid scheme-and-host match pattern derived from a configured URL-mapping origin; never request `<all_urls>`. Use `activeTab` where applicable. The app publishes unique origin groups containing only mapping IDs and normalized matchers, ordered by mapping priority, literal specificity, update time, and ID. Match the complete tab URL within its exact origin group before path redaction so same-origin paths select the correct repository. A changed valid app-side configuration or identity-scope UUID clears cached browser context, while a rejected or oversized replacement also clears the prior native authorization snapshot. Maintain a persistent Native Messaging connection to `crates/devhud-native-messaging-host`, which connects to the app-owned per-user IPC endpoint using its authenticated versioned `v1` envelope. The host uses `$XDG_RUNTIME_DIR/devhud.sock` on Linux, `~/Library/Application Support/io.delino.devhud/run/devhud.sock` on macOS, or the current-user Windows named pipe `\\.\pipe\io.delino.devhud.ipc`; the app owns the message schema and rejects failed challenge/response, unsupported versions, UTF-8 JSON bodies larger than 256 KiB before framing/parsing, and expired requests. Before posting, the extension measures both the complete Chrome Native Messaging request and its projected signed IPC envelope, including the host-added timestamp and proof; oversized capture markup is omitted first, and any request that remains oversized is rejected locally. Validate the exact extension ID, Native Messaging origin, one-time pairing nonce, schema, shared pre-framing message-size ceiling, and timeout.

Capture only a normalized URL, title and user agent capped individually at 4 KiB of UTF-8, viewport, selected bounds, accessibility values capped individually at 4 KiB of UTF-8, and sanitized `outerHTML` capped at 128 KiB. The native protocol boundary revalidates every per-field cap before accepting context. UTF-8 truncation of untrusted strings must bound processing and temporary allocation by the configured byte cap instead of encoding the complete input first. Selection bounds and accessibility attributes are collected only when the selected element itself is allowlisted and visible; excluded form controls and hidden elements contribute no metadata. Exclude non-rendered descendants and subtrees hidden by DOM state or ancestor CSS, including collapsed visibility, zero opacity, skipped `content-visibility` content, zero-area layout boxes, and content fully clipped by the viewport or an overflow ancestor. Do not capture a DOM-derived CSS selector: bounds are the only selection locator because selector IDs, classes, and attribute values may contain secrets. Accept only `http` and `https` page URLs; remove user-info credentials, query strings, and fragments, lowercase the host, omit default ports, and replace every non-empty persisted path segment with `<redacted>`. Do not add URL-mapping exceptions for query parameters. Build the DOM fragment from an explicit element allowlist (`a`, `article`, `aside`, `blockquote`, `code`, `dd`, `details`, `div`, `dl`, `dt`, `em`, `figcaption`, `figure`, `footer`, `h1`-`h6`, `header`, `hr`, `img`, `li`, `main`, `nav`, `ol`, `p`, `pre`, `section`, `summary`, `table`, `tbody`, `td`, `tfoot`, `th`, `thead`, `tr`, `ul`) and an explicit attribute allowlist limited to the approved accessibility/text attributes (`alt`, `aria-describedby`, `aria-hidden`, `aria-label`, `aria-labelledby`, `role`, `title`). Drop all other elements and attributes, including `meta`, `link`, scripts, styles, event handlers, `data-*`, form controls, and hidden/password fields. Redact every URL-valued attribute (`href`, `src`, `cite`, `poster`, `ping`, `srcset`, and form URL attributes) rather than preserving query strings, fragments, signed URLs, or credentials. Never collect cookies, storage, console/network data, or page-wide DOM. Missing, denied, revoked, disconnected, unsupported-incognito, malformed, or absent extension must degrade to capture without browser context and manual repository selection. Chrome incognito support is excluded.

## Storage

Store only non-secret extension configuration and pairing state required by the app; pairing data remains local and is removed on logout or account deletion. In-memory pairing state, cached context, and session generations are invalidated before secure-storage cleanup is attempted, so a cleanup error cannot preserve an authenticated live session. Logout deletes persistent pairing credentials only after draft deletion succeeds. Pairing completion and captured-context commits are serialized with secret rotation and accepted only for the authenticated generation. Do not persist page captures beyond the app’s draft/submission contract.

## Security

Origin permissions are least-privilege and user initiated. Pairing is one-time and timeout/schema bounded; every participant enforces the same 256 KiB UTF-8 JSON body limit before framing/parsing. Browser context is fully inspectable as sanitized text and removable through a revision-checked draft mutation, and top-level URLs always use the normalized origin plus the redacted path structure defined below with credentials, query strings, and fragments removed.

The path component is redacted before persistence or submission: replace every non-empty segment with the literal `<redacted>` placeholder while preserving slash structure. This prevents reset tokens, invite secrets, capability URLs, and other path credentials from entering drafts, issues, or agent input; literal path values are never retained.

## Logging

Use redacted structured diagnostics. Never log page content, credentials, cookies, storage, tokens, or complete URLs with sensitive query/fragment data.

## Build and Test

Validate manifest permissions, unique localized origin permission names, stale-origin grant removal only after successful popup and capture configuration refreshes, extension ID/origin pairing, malformed/replayed/oversized messages including the shared 256 KiB pre-framing ceiling, no-active-tab and sensitive-form behavior, absent/denied/revoked permissions, unsupported incognito behavior, pointer-sequence isolation and localized keyboard completion during selection, element/attribute allowlist enforcement, selector omission, path-segment redaction, URL-value redaction (including signed URLs and `data-*` secrets), sanitized DOM cap, reproducible ZIP parity, and Web Store packaging.

## Dependencies and Integrations

Integrates with the Native Messaging host, DevHud local IPC, configured URL mappings, and desktop RealQA only. It never calls `devhud-api` or GitHub directly.

## Change Triggers

Update the project index, app/native-host contracts, `apps/AGENTS.md`, and root/domain ownership rules when extension permissions, message schema, pairing, packaging, or privacy boundaries change.

## References

- [DevHud project index](project-devhud.md)
- [Native Messaging host contract](crates-devhud-native-messaging-host-contract.md)
- [App contract](apps-devhud-foundation.md)
- [Repository defaults](repository-defaults.md)
