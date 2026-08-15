# apps-devhud-chrome-extension-contract

## Scope

`apps/devhud-chrome-extension` is the planned signed Chrome Manifest V3 extension that supplies optional, permission-scoped browser context to desktop RealQA. It is not implemented.

## Runtime and Language

Manifest V3 TypeScript/JavaScript extension with English/Korean user-facing UI and fixed DevHud identifiers. It is distributed through the Chrome Web Store and as a reproducible validation ZIP.

## Users and Operators

Desktop RealQA users, Chrome Web Store reviewers, and release operators.

## Interfaces and Contracts

Request optional host permissions only after a user gesture and only for configured URL-mapping origins; never request `<all_urls>`. Use `activeTab` where applicable. Maintain a persistent Native Messaging connection to `crates/devhud-native-messaging-host`, which forwards schema-versioned messages through user-scoped local IPC to `apps/devhud`. Validate extension ID, Native Messaging origin, one-time pairing nonce, schema, message size, and timeout.

Capture only privacy-stripped URL, title, viewport, user agent, selected selector/bounds, accessibility attributes, and sanitized `outerHTML` capped at 128 KiB. Remove scripts, styles, handlers, form/password/hidden credential data; never collect cookies, storage, console/network data, or page-wide DOM. Missing, denied, revoked, disconnected, unsupported-incognito, or absent extension must degrade to capture without browser context and manual repository selection. Chrome incognito support is excluded.

## Storage

Store only non-secret extension configuration and pairing state required by the app; pairing data remains local and is removed on logout. Do not persist page captures beyond the app’s draft/submission contract.

## Security

Origin permissions are least-privilege and user initiated. Pairing is one-time and timeout/size/schema bounded. Browser context is editable/removable and query/fragment stripping follows URL-mapping privacy policy.

## Logging

Use redacted structured diagnostics. Never log page content, credentials, cookies, storage, tokens, or complete URLs with sensitive query/fragment data.

## Build and Test

Validate manifest permissions, extension ID/origin pairing, malformed/replayed/oversized messages, no-active-tab and sensitive-form behavior, absent/denied/revoked permissions, unsupported incognito behavior, sanitized DOM cap, reproducible ZIP parity, and Web Store packaging.

## Dependencies and Integrations

Integrates with the Native Messaging host, DevHud local IPC, configured URL mappings, and desktop RealQA only. It never calls `devhud-api` or GitHub directly.

## Change Triggers

Update the project index, app/native-host contracts, `apps/AGENTS.md`, and root/domain ownership rules when extension permissions, message schema, pairing, packaging, or privacy boundaries change.

## References

- [DevHud project index](project-devhud.md)
- [Native Messaging host contract](crates-devhud-native-messaging-host-contract.md)
- [App contract](apps-devhud-foundation.md)
- [Repository defaults](repository-defaults.md)
