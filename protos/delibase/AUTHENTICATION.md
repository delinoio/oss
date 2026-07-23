# delibase authentication metadata and scopes

Logto access tokens and client secrets never appear in protobuf messages. They
travel only in Connect HTTP metadata and are validated by delibase for the Logto
audience `https://delibase.deli.dev`. Invitation bearer tokens are the sole
protobuf-body credential exception; clients and servers must treat request
payloads containing them as sensitive and must never log or persist the raw
token.

## Human and public RPCs

Human RPCs use the standard `Authorization: Bearer <user-access-token>` header.
Enabled `CatalogService` reads are public and require no scope. All other human
RPCs require the following user scope; role, organization membership, and team
access checks still apply independently.

| RPC group | Required user scope |
| --- | --- |
| `AccountService.GetAccountState`, `GetAccountDeletionImpact` | `delibase:account:read` |
| `AccountService.CompleteOnboarding`, `DeleteAccount` | `delibase:account:write` |
| `OrganizationService` reads | `delibase:organizations:read` |
| `OrganizationService` mutations | `delibase:organizations:write` |
| `TeamService` reads | `delibase:teams:read` |
| `TeamService` mutations | `delibase:teams:write` |
| `BillingService.GetBillingSummary`, `ListLedgerEntries`, `ListUsageRecords` | `delibase:billing:read` |
| `BillingService` checkout, portal, and overage-limit mutations | `delibase:billing:write` |

Invitation preview and acceptance require an authenticated user token even
though the invitation URL also contains a bearer token. Invitation bearer tokens
are request data, are stored by the server only as hashes, and are never logged.

## Usage RPCs

Usage mutation calls are server-to-server. The standard `Authorization` header
carries the mini-app backend's Logto M2M token. The forwarded end-user Logto
access token is carried in exactly this dedicated header:

```text
x-delibase-forwarded-user-token: <user-access-token>
```

The header value is credential material. Clients and servers must redact it from
logs, traces, errors, persisted requests, idempotency payloads, and diagnostics.
It must never be placed in a protobuf field. The forwarded token requires the
user scope `delibase:usage:execute`. The M2M token requires the operation scope:

| Usage RPC | Required M2M scope |
| --- | --- |
| `UsageService.ReserveUsage` | `delibase:usage:reserve` |
| `UsageService.CommitUsage` | `delibase:usage:commit` |
| `UsageService.ReleaseUsage` | `delibase:usage:release` |

Possession of both tokens and scopes is necessary but not sufficient. Delibase
also validates issuer, audience, expiry, the authenticated service's catalog
meter allowlist, organization membership, and the forwarded user's direct or
inherited access to the requested team. The browser must never call
`UsageService` or hold an M2M credential.
