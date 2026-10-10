# DeliDev Tailscale connections

## Scope
Go owns installed-CLI discovery, explicit HTTPS peer checks, approved pairing and the original foreground Serve ingress. Desktop Connections owns disposable presentation. Existing Device, saved-connection, SSH InstallationService and independent Worker controllers retain their authority.

## Runtime and Language
Use Go, authenticated Connect RPC and the installed Tailscale CLI. Native desktop infrastructure supplies validated original process access only. React uses typed observations and explicit commands.

## Users and Operators
The main Local desktop may discover visible peers without installation, login, pairing, registration or service startup. Access defaults to Off. Explicit Enable requires consent for tailnet visibility and original server lifetime. Shared, tagged and unknown peers receive no automatic trust.

## Interfaces and Contracts
System `TAILSCALE_CONNECTION_APPROVAL_V1` owns capability 86. The closed Tailscale service provides discovery, access status/control, explicit peer checks and original approval request/status/list/decision/cancel boundaries. Old peers retain manual pairing and SSH setup. Peer checks use only canonical HTTPS hostname:8443 with normal TLS verification, no redirects, inherited proxy, port scan or fallback.

Foreground Serve uses HTTPS port 8443 and a separate ephemeral loopback ingress for this original server. Never forward the private desktop runtime endpoint. Refuse occupied or foreign Serve configuration, missing HTTPS prerequisites and unavailable permission without installation, elevation, policy changes or Funnel. Keep exact child/configuration ownership; Disable, Stop and Quit fence ingress and join owned work. Persist opt-in intent while re-admitting current identity and original server on restart. Stop remains authoritative within the desktop process.

Approval binds the original request, both ephemeral keys, target server/origin and requested client or Worker role. Both parties verify a matching code. Only an authenticated original owner/client may approve. Encrypt the single-use pairing grant to the requester key; names and Tailscale headers grant no authority. Denied, canceled or expired requests create no registration. Uncertain outcomes reconcile the exact retained request without replacement grants or attempts.

Desktop Worker registration names the selected current server before consent and starts a separately owned outbound Worker. Headless targets reuse explicit SSH fingerprint confirmation, write-only authentication, signed installation and original-operation recovery. Only DeliDev Worker installation is allowed; no remote desktop/server, harness, service or arbitrary command is introduced.

## Storage
Retain bounded private UUID-v7 access/approval intents and original retry ownership. Never persist a complete peer inventory. Reuse pairing receipts and saved profile identity; pin canonical HTTPS hostname:8443. Changed tailnet identity requires new explicit pairing, never silent profile rewriting. Add no unrelated or empty migration.

## Security
Separate peer ownership, online status and observed DeliDev support. Discovery cannot authorize work. Protect Local generation/locator, original grants, requester keys, device credentials, independent Workers and cleanup uncertainty. Accept only bounded complete CLI/protocol observations; partial inventories fail explicitly. Settings departure cancels discovery and fences late presentation while accepted mutations retain original owners.

## Logging
Use structured closed phase/error/operation classifications. Exclude names, addresses, user identities, peer inventories, raw CLI output, credentials and pairing material.

## Build and Test
Use isolated fake CLI, HTTPS, approval, ingress and SSH fixtures for bounded cancellation, strict validation, foreign ownership, transcript changes, expiry, denial, replay, uncertain delivery and original cleanup. Focused local tests and static checks may run; full suites, race, build and packaging belong to CI under the active task constraint. Real two-device and packaged-platform acceptance is separate and must never be inferred from fixtures.

## Dependencies and Integrations
Use existing Go security/process/Device/storage primitives, Connections profiles, SSH InstallationService, Worker controllers, localized semantic desktop controls and generated Connect clients. Tailscale CLI status and foreground Serve semantics follow the official Tailscale documentation.

## Change Triggers
Update desktop, connections, SSH, protocol, lifecycle and project ownership contracts together when this boundary changes. Instructions route to owning contracts; keep feature behavior here.

## References
- [Project](project-delidev.md)
- [Desktop](apps-delidev-desktop-contract.md)
- [Connections](cmds-delidev-connections-contract.md)
- [SSH setup](cmds-delidev-ssh-setup-contract.md)
- [Protocol](protos-delidev-v1-contract.md)
- [Defaults](repository-defaults.md)
- [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve)
- [Tailscale CLI](https://tailscale.com/docs/reference/tailscale-cli)
