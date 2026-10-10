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

### Connections presentation

Order the main Local page as Current connection, Tailscale devices, Saved connections and collapsed Advanced controls. Access controls belong to Current connection. Keep the content width at 1040 pixels, use semantic colors, 8-pixel corners and 40-pixel controls, and stack row actions on narrow windows. Provide English and Korean copy, announced read/status failures, keyboard focus and Escape dismissal. The single matching-code input receives focus. A saved-connection window can approve its authenticated target's request but cannot inspect another machine's local Tailscale installation.

Opening the page reads installed availability and visible peers. Refresh reads again; it does not Check peers, enable ingress or mutate registration. Online peers expose explicit Check, followed by supported client/Worker actions. Headless or unsupported peers retain SSH setup. Show the selected current server before desktop or SSH Worker consent. Existing manual client pairing, advanced server controls and independent local Worker remain available.

### Retained delivery and recovery

Persist only the selected request's peer identity and canonical origin, original actor/server, requester ephemeral private key, original offer and approval transcript, profile identity and exact Worker pairing request. Go owns grants and credentials; React receives typed public status and matching codes. Returning to Connections reads original pending operations and reconciles those exact requests. The original target approves the original requested role and selected Worker destination, then encrypts its single-use approval to the requester. Worker delivery returns a separately encrypted selected-server pairing grant to that target; it cannot turn target-side approval into a Worker grant for the target's own server. Retain the original delivery digest before pairing, and fence startup before launching the independently owned Worker. A lost original pairing/start journal fails closed instead of reconstructing an attempt.

An explicit SSH server origin must match the original active Tailscale HTTPS ingress before credentials are staged and before a remote artifact is staged. Capture it in the original durable SSH operation. Omitted origin retains the existing manual endpoint behavior. Existing original installer documents and reconciliation remain authoritative after acceptance.

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
- [Foreground Serve ownership](https://github.com/tailscale/tailscale/blob/main/cmd/tailscale/cli/serve_v2.go)
- [Native peer ownership metadata](https://github.com/tailscale/tailscale/blob/main/ipn/ipnstate/ipnstate.go)
