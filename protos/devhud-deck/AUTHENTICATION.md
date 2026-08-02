# Deck authentication metadata

Credentials are Connect HTTP metadata, never protobuf fields. Human calls use:

```text
authorization: Bearer <deck-audience-user-access-token>
x-devhud-deck-forwarded-delibase-token: <delibase-audience-user-access-token>
```

The Deck token audience is exactly `https://deck.deli.dev`. The forwarded
token is memory-only, uses the delibase audience, and must have the same subject
as the Deck token. Both values are sensitive: native transports and servers
must redact them from logs, traces, errors, caches, persistence, diagnostics,
and idempotency payloads.

The bounded server foundation additionally requires the forwarded user token's
`delibase:account:read`, `delibase:organizations:read`, and
`delibase:teams:read` scopes before resolving current account, organization,
and billing-team membership. Only `DeckViewService.RefreshView` additionally
requires `delibase:usage:execute` because only that procedure performs live
Deck refresh reserve/commit/release; `DeckIntegrationService` and other Deck
procedures do not require the usage-mutation scope. The validated forwarded
token remains only in the active server request context for those live calls
and is never detached or persisted. These authentication scopes do not replace
the server-authoritative membership and role checks.

`RegisterDevice` returns its opaque, single-registration cleanup grant only in
this sensitive response metadata:

```text
x-devhud-deck-device-revocation-grant: <opaque-grant>
```

An unauthenticated cleanup retry may send that same key only to
`DeckDeviceService.UnregisterDevice`. The grant is not accepted by any other
procedure and never appears in a protobuf message. It may persist only in the
OS-vault cleanup tombstone through the registration lease.

`DeckViewService.DeleteFeatureData` in `delibase_lifecycle` mode instead uses a
Deck-audience delibase M2M bearer in `authorization`. It must not include the
forwarded-user metadata. Deck pins both that token's `sub` and `client_id` to
the configured lifecycle client in addition to validating issuer, audience,
expiry, and scope. Owner-request deletion uses the normal two human tokens.

Deck defines no reservation-finalization grant or background-usage metadata.

## User scopes

Role, ownership, DeliDev membership, GitHub repository authorization, and
billing access checks remain server-authoritative in addition to these scopes.

| RPC group | Deck user scope |
| --- | --- |
| View and pull-request reads, refresh quote | `deck:views:read` |
| View writes, refresh, PR mutation, owner deletion | `deck:views:write` |
| Integration reads | `deck:integrations:read` |
| Integration start/disconnect | `deck:integrations:write` |
| Device and notification procedures | `deck:devices:write` |

The DeliDev settings client is restricted to `DeckIntegrationService` even when
its user token carries a broader scope.
