# DevHud Security

## Secure operation

Production API connections require HTTPS. For installations using a custom API origin, the renderer fetches Bootstrap from that selected origin for identity and capability configuration. Official-upload authority is fetched independently from DevHud's first-party service. Logto must use the configured issuer, audience, client key, exact callback, state, nonce, and PKCE flow. Never paste tokens, PATs, R2 secrets, or signed URLs into support requests.

Platform secure storage protects credentials. The Chrome extension uses least-privilege, gesture-triggered origin permissions and authenticated pairing. Desktop shortcuts and capture permissions remain controlled by macOS, Windows, or X11; native Wayland is unsupported.

## Updates and key rotation

Desktop updates use a signed stable manifest and verify the downloaded artifact before installation. The trust root is pinned; a new signing key is accepted only through signed successor metadata. Invalid signatures and unauthorized rollback are rejected. Mobile updates come from Apple or Google. A release is not described as generally available until every coordinated surface is public and verified.

## Custom API origins

Installations may select a custom API origin for authentication and settings. That origin does not authorize official assets or bypass client security checks.

## Reporting

For a suspected vulnerability, contact the maintainers through the [Delino OSS security contact](https://github.com/delinoio/oss/security) and provide a minimal reproducible description. Do not publish credentials or private captures. See [Privacy](privacy).
