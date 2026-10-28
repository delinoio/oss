# Configure services

## Configuration availability

The following names are supplied by the server integration interface. Their presence does not establish live integration acceptance. Use a release's matching configuration template; do not infer additional flags, callback routes, or environment variables.

| Purpose                | Configuration                                                                                                             |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| Internal HTTP listener | `TAIROUTER_LISTEN_ADDR` (default `127.0.0.1:8080`)                                                                        |
| Public HTTPS origin    | `TAIROUTER_PUBLIC_URL`                                                                                                    |
| PostgreSQL connection  | `TAIROUTER_DATABASE_URL`                                                                                                  |
| OIDC                   | `TAIROUTER_OIDC_ISSUER`, `TAIROUTER_OIDC_CLIENT_ID`, `TAIROUTER_OIDC_CLIENT_SECRET`                                       |
| Operator identities    | `TAIROUTER_OPERATORS` (JSON array with `issuer` and `subject`)                                                            |
| Encryption             | `TAIROUTER_ENCRYPTION_KEYS` (JSON mapping key versions to base64-encoded 32-byte keys), `TAIROUTER_ENCRYPTION_ACTIVE_KEY` |
| SMTP                   | `TAIROUTER_SMTP_ADDR`, `TAIROUTER_SMTP_FROM`, `TAIROUTER_SMTP_USERNAME`, `TAIROUTER_SMTP_PASSWORD`                        |
| R2                     | `TAIROUTER_R2_ENDPOINT`, `TAIROUTER_R2_BUCKET`, `TAIROUTER_R2_ACCESS_KEY_ID`, `TAIROUTER_R2_SECRET_ACCESS_KEY`            |

Do not paste real values into support requests. Encryption keys must be generated and stored securely, not copied from an example. Back them up separately from the database.

## OIDC and operators

1. Register an application with the generic OIDC provider selected by the operator.
2. Register the exact public callback: for the example origin, `https://router.example.com/auth/callback`.
3. Configure the issuer, client ID, and client secret using the names above.
4. Ensure accounts have an email and `email_verified=true`.
5. Configure the operator allowlist using exact issuer/subject pairs.

Users are identified by issuer plus subject. Matching email addresses do not merge identities. There is no email/password login. Required session behavior is reauthentication after 24 hours of inactivity or seven days after login, whichever comes first; logout invalidates that session immediately.

Operator additions and removals require deployment configuration changes. Operators manage instance settings and health/aggregates; this role does not grant member impersonation or access to another user's provider secrets or request content.

## SMTP

Configure the SMTP server, sender, and required authentication. Verify delivery to a controlled mailbox before enabling invitations. The exact SMTP transport-security settings must come from the release configuration; no additional TLS environment switch is specified here.

An invitation can fail even if login works. Check recipient acceptance and transient/permanent SMTP errors separately. Read [invitation retries](organizations.md) before resending: a manual resend invalidates the earlier link.

## HTTPS and callbacks

The existing reverse proxy terminates public TLS. The service speaks internal HTTP. Keep PostgreSQL and metrics private. Configure proxy support for streaming and WebSocket upgrades, and preserve the intended public host and scheme. Do not expose the internal HTTP listener directly to the internet.

Conceptually, `https://router.example.com` routes to the private service listener; `/auth/callback` returns to the same public origin. This is an address example, not a complete proxy configuration. A container-to-container deployment needs the listener and proxy network selected by its Compose configuration, rather than a host-loopback address copied blindly.

Register provider OAuth callbacks only when the provider integration supplies the exact callback and client requirements. The OIDC callback is not a substitute for provider OAuth callbacks. Provider callback details remain unavailable until established by the matching integration.

### Caddy example

This example comes from the deployment assets. Replace `router.example.com` with your public hostname and configure the matching public origin. It assumes Caddy can reach the Compose listener on the same host. Validate certificate issuance, streaming, and WebSockets in your environment before admitting users.

```text
router.example.com {
    @internal path /metrics /healthz /readyz
    respond @internal 404
    reverse_proxy 127.0.0.1:8080 {
        flush_interval -1
    }
}
```

Keep the internal routes available only to trusted health checks and monitoring. This example has not been presented as a live TLS acceptance result.

## Required R2 storage

1. Create a private R2 bucket for temporary generated images and hosted video output.
2. Supply its endpoint, bucket, and access credentials through the configuration above.
3. Give the service the object access needed to upload, retrieve, and delete its artifacts.
4. Verify object creation and deletion, and signed download expiry, with non-sensitive evaluation data.

Do not make the bucket public to repair a failed signed download. Images and hosted videos share a quota of **200 files per user within each organization**. Files expire after **24 hours**; oldest-first eviction can remove either media type earlier when the shared count is exceeded.

| Output                            | Per-file limit    | Combined output limit                   |
| --------------------------------- | ----------------- | --------------------------------------- |
| Generated images                  | 50 MiB per image  | 100 MiB decoded image data per response |
| Hosted video helper (`video/mp4`) | 200 MiB per video | 600 MiB per helper turn                 |

The video helper is available only through the supported hosted workflow. It does not add a public `/v1/videos` API. These video limits do not change the image limits. The result supplies an opaque artifact identifier and an authorized signed download URL, not a server-local file path. Indirect media downloads remain subject to destination approval and the original request's send budget.

Both media types use the same organization/user authorization, expiry, and deletion rules. They are temporary output, not a permanent archive. The limits and interface described here do not establish live provider or R2 acceptance.

A signed URL is a bearer capability. Anyone holding it can download until it expires, for at most five minutes and never beyond artifact expiry. Authorization is checked when the URL is issued, not again for each download. Deleting the object makes an existing URL unusable. Do not put signed URLs in public logs or tickets.

## Provider destination policy

Public HTTPS destinations are allowed by default. Private/local HTTP model servers require explicit operator approval. Instance operators manage these rules in **Approved destinations**; organization ownership alone does not grant this permission.

1. Enter the exact origin: scheme, hostname, and port, such as `http://localhost:8080`. Approve only the destination you intend to use.
2. Optionally enter **Allowed networks** as CIDR ranges, one per line or separated by commas. Use ranges for the actual destination network; do not copy example ranges as approvals.
3. Add the entry to the draft, or use **Edit** on an existing rule and apply its changes. Editing retains the rule's identity. Finish or cancel an open destination edit before saving the policy.
4. Select **Review and save changes**. Review every origin and network in the confirmation, including removed rules, then confirm.

An empty network list approves the exact origin without CIDR narrowing. Clearing a previous list broadens access for that origin. A nonempty list restricts the origin to those networks, even for public HTTPS. An address outside the configured networks is blocked; public HTTPS does not override that restriction. All resolved addresses must satisfy the rule, and redirects cannot bypass it. Removing approval blocks future connections that require it.

Duplicate canonical origins and invalid CIDR prefixes are rejected. Correct the entries and review again. If another operator changed the saved policy, your draft and network entries remain available, but the stale save is rejected. Reload the current settings, reconcile your intended changes, and review before resubmitting; do not assume the failed save took effect.

Do not disable destination checks to work around a connection failure. Coordinate changes with users of affected provider connections and hosted media downloads.
