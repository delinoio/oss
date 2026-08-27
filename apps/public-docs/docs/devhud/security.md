# DevHud Security

## Secure operation

Production API connections require HTTPS termination at a trusted deployment. Bootstrap is fetched from the first-party HTTPS service and supplies validated identity and capability configuration. Logto must use the configured issuer, audience, client key, exact callback, state, nonce, and PKCE flow. Never paste tokens, PATs, R2 secrets, or signed URLs into support requests.

Platform secure storage protects credentials. The Chrome extension uses least-privilege, gesture-triggered origin permissions and authenticated pairing. Desktop shortcuts and capture permissions remain controlled by macOS, Windows, or X11; native Wayland is unsupported.

## Updates and key rotation

Desktop updates use a signed stable manifest and verify the downloaded artifact before installation. The trust root is pinned; a new signing key is accepted only through signed successor metadata. Invalid signatures and unauthorized rollback are rejected. Mobile updates come from Apple or Google. A release is not described as generally available until every coordinated surface is public and verified.

## Self-hosting

Self-hosting must provide HTTPS, a correctly configured Logto tenant, API and database services, public asset storage, retention cleanup, and release/signing controls. A custom API origin changes authentication and settings authority only; it does not authorize official assets or bypass client security checks. Follow the internal operator contracts for deployment-specific requirements.

## Reporting

For a suspected vulnerability, contact the maintainers through the [Delino OSS security contact](https://github.com/delinoio/oss/security) and provide a minimal reproducible description. Do not publish credentials or private captures. See [Privacy](privacy).
