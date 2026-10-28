# AIPRouter service contract

## Scope

Owns `servers/aiprouter`: the Go service, authorization enforcement, inference adapters, durable and temporary state, and management handlers. Shared mechanisms live in `packages/aiprouter`; this service owns their authorization and lifecycle policy.

## Runtime and Language

Go in the root module `github.com/delinoio/oss`. PostgreSQL is authoritative. Implement anew; do not bundle opencodex as a server engine.

## Users and Operators

Organization owners, administrators, members, and separately configured instance operators. Roles and authority are defined below.

## Interfaces and Contracts

### Organizations, ownership, and roles

#### Tenant and credential ownership

- A tenant is an organization.
- A user can create an organization, join existing organizations by invitation, and belong to multiple organizations.
- Each user registers and exclusively uses their own provider keys/accounts.
- Account pools and routing are also private to that user within that organization.
- The same user must register/configure credentials, pools, and routing separately in each organization.
- Do not provide shared organization credential pools.
- Enforce organization/user isolation in APIs, queries, caches, temporary state, jobs, retries, and metadata access.

#### Organization roles

- **Owner:** can delete the organization, transfer ownership, and appoint, demote, or remove administrators.
- **Administrator:** can invite/remove ordinary members and manage organization settings; cannot appoint, demote, or remove other administrators or alter the owner's role/membership.
- **Member:** uses permitted personal functionality.
- Owners/administrators can view per-member usage, estimated cost, model, and error metadata.
- They cannot view another member's provider secret values, prompts, or response content.
- A demoted administrator remains logged in, but every subsequent management request must check current permissions. An already-open page must not retain authority.

#### Ownership transfer

- The owner nominates an existing member, who must accept before transfer takes effect.
- The previous owner becomes an administrator.
- The last owner cannot simply leave; they must transfer ownership or delete the organization.
- A transfer request is valid for seven days and can be canceled by the existing owner before acceptance.
- Allow one pending transfer per organization. A new request invalidates the previous request.
- Recheck both parties' current membership and authority at acceptance.

#### Instance operator

- Instance operator authority is separate from organization ownership/administration.
- Operators manage instance settings and whole-instance health/aggregates only.
- Do not grant organization-internal inspection, user suspension, impersonation, or secret/content access through this role.
- Define initial and additional operators exclusively through a deployment-configured list of OIDC `issuer+sub` identities. Adding/removing operators requires changing that list.
- Manage request limits, retention settings, allowed destinations, and common price overrides through the web operator console.
- Do not implement operator functions by granting access to individual provider configurations.

### Console route integration

Serve the console document and permit local OIDC return targets for every canonical page in the [console page-route contract](apps-aiprouter-console-contract.md#page-routes). Keep unknown paths and authentication, inference, and management API paths outside the console fallback. Reject retired paths and legacy navigation selectors in console requests and OIDC return targets. Use `/overview` as the default OIDC return target and `/invitation` for invitation email links. Route support does not grant management authority.

Serve `/manifest.webmanifest` as `application/manifest+json` with revalidation (`Cache-Control: no-cache`). Serve installation icons through the existing static asset boundary. These public metadata assets do not expand the console document or OIDC return-target allowlists.

### OIDC, web sessions, and onboarding

- Support only an operator-configured generic OIDC identity provider; no email/password login.
- Allow users authenticated by that provider to register, provided an email is present and `email_verified=true`.
- Identify users by `issuer+sub`. Never merge accounts solely because email addresses match.
- Require reauthentication after 24 hours of inactivity or seven days after login, whichever applies first.
- Logout immediately invalidates the corresponding web session.
- A user without an organization is prompted to create one or accept an invitation; do not automatically create a personal organization.
- Functional onboarding order: choose an organization → connect personal providers → configure models/routing → issue an API key → connect a client.
- Concrete screen layouts, visual design, previews, and design approval are explicitly outside this PRD's decision scope.

### Invitations and SMTP delivery

- Send organization invitations through configured SMTP.
- Acceptance requires the signed-in OIDC account's verified email to match the invited email.
- Invitations are single-use, cancelable, and valid for seven days.
- Enforce at least one minute between resends to the same email and at most 100 send attempts per organization per hour.
- A manual resend invalidates the previous link and pending delivery jobs, then creates a new link with a new seven-day lifetime.

Delivery policy:

- At most three attempts including the initial send.
- Retry after one minute, then five minutes after the next failure.
- Retry only transient errors; fail permanent errors immediately.
- Count every attempt against the organization hourly limit.
- If limited, defer until the next permitted time; never send after invitation expiry.
- Automatic retries reuse the same link. Ambiguous SMTP completion may result in duplicate emails, but invitation acceptance remains single-use.
- Final delivery failure marks the invitation failed and invalidates its link.
- Manual resend can recover from final failure using a new link/lifetime.
- Prevent canceled jobs, superseded links, and concurrent retries from resurrecting old invitations.

### Inference API keys and device authorization

#### Organization-scoped API keys

- Each user-issued key belongs to one user and one organization.
- Allow no expiry, a user-selected expiry, and immediate user revocation.
- Show the raw value once at ordinary issuance; store a hash for subsequent verification.
- Revocation/expiry blocks new requests immediately.
- An already-dispatched upstream response may finish. Stop additional retries, account switching, and token refresh for that request.

#### CLI device authorization

- Issue a dedicated CLI key through a browser device-code approval flow that also works with SSH.
- The CLI displays an approval URL and one-time code; the browser requires login and explicit code/connection approval.
- The code expires ten minutes after its original issuance. The CLI polls every five seconds.
- The approval flow identifies the server address, organization, device name, and key expiry; the user selects the organization and expiry.
- Denial or pre-approval expiry issues no key.
- Create the key only once. If result delivery is interrupted, the same CLI can retrieve the result again until the original ten-minute expiry.
- Authenticate retrieval with a device secret held only by that CLI.
- **Temporary raw-key exception:** retain the encrypted raw key only in the backup-excluded temporary storage area until delivery completes or the original code expires. API verification still uses its hash.
- Delete the temporary raw value immediately after the CLI confirms successful storage; never place it in logs or backups.
- Revoke a key that has not been handed off by expiry.

#### Local key storage and failure

- Store the CLI key in the OS credential store.
- If that store is unavailable, fail without changing client configuration. Do not fall back to a normal credential file.
- Immediately attempt to revoke a server key already issued before local storage failure. If revocation fails, report the outstanding key and explain web revocation.
- When a client requires the remote API key in its own settings, permit that additional storage only after explicit confirmation. Apply owner-only file permissions/Windows ACLs and protect secrets in previews, diagnostics, and backups.

### Inference limits, retries, timeout, and network policy

#### Per-user admission

- Apply limits per user within an organization.
- Default: **120 inference requests/minute and 10 concurrent requests**.
- Operator ranges: **1–6,000 requests/minute and 1–100 concurrent requests**.
- Unlimited settings are not permitted.
- Count incoming client inference requests separately from provider retry/send budgets.
- On frequency or concurrency exhaustion, return HTTP 429 with `Retry-After` immediately; do not add a server admission queue.
- Do not impose an additional instance-wide concurrency cap. Use per-user limits and operator-managed infrastructure capacity.

#### Reference retry and fallback behavior

Adopt the fixed reference's state-specific provider-error, account-rotation, and combination-routing policies. Explicit AIPRouter cancellation, membership removal, key revocation, and DB-outage rules take precedence.

Do not flatten all paths into a uniform three-attempt rule. The inspected reference has shared physical-send budgets, non-replayable ambiguous sends, API-key versus OAuth distinctions, cooldowns, and destination-specific retry policies.

Sources:

- [Upstream resend guards and shared budgets](https://github.com/lidge-jun/opencodex/blob/88aaa754566d119b6b8fc898f8fb47602387d82e/src/lib/upstream-retry.ts).
- [Provider key failover and 429 policies](https://github.com/lidge-jun/opencodex/blob/88aaa754566d119b6b8fc898f8fb47602387d82e/src/providers/key-failover.ts).
- [Fallback policy](https://github.com/lidge-jun/opencodex/blob/88aaa754566d119b6b8fc898f8fb47602387d82e/src/routing/fallback-policy.ts).

#### Timeout behavior

Retain the reference's provider/path-specific timeout behavior, including the default absence of a stall timeout for local upstreams.

Verified examples, not an exhaustive replacement for the reference inventory:

- Public upstream default stall timeout: 300 seconds.
- Local upstream stall timeout: disabled when not explicitly configured.
- Responses JSON body read: 180-second total and 30-second inactivity deadlines.
- Buffered SSE-to-JSON Responses path: 15-minute total deadline.

Sources: [stall resolution](https://github.com/lidge-jun/opencodex/blob/88aaa754566d119b6b8fc898f8fb47602387d82e/src/stall-timeout.ts), [JSON body lifetime](https://github.com/lidge-jun/opencodex/blob/88aaa754566d119b6b8fc898f8fb47602387d82e/src/server/responses/core-lifetime.ts), [buffered Responses](https://github.com/lidge-jun/opencodex/blob/88aaa754566d119b6b8fc898f8fb47602387d82e/src/server/responses/buffered-sse-json.ts).

#### Destinations and cancellation

- Permit public HTTPS provider destinations by default.
- Permit private-network/local HTTP destinations only when approved by the operator.
- Match operator destination rules by canonical scheme, hostname, and port. An empty CIDR list keeps the exact-origin approval behavior. A nonempty CIDR list restricts that origin to the listed networks, including when the origin uses public HTTPS; it must not fall back to the public-HTTPS default after an address fails the rule. Reject duplicate canonical origins and invalid prefixes. Validate every resolved address, normalize IPv4-mapped IPv6 consistently, and pin connections to the validated addresses. Redirects must not bypass the rule.
- Test URL parsing, DNS resolution, and redirects against destination-policy bypass.
- On client disconnect/cancellation, immediately attempt upstream cancellation and stop additional retries.
- Record confirmed usage only; distinguish unknown usage from zero.

#### PostgreSQL outage

- Reject new inference requests with HTTP 503.
- Do not continue admitting requests using cached authorization.
- Allow only an already-dispatched upstream response to finish; stop additional sends.
- Report usage-write failures through logs and metrics rather than recording success.
- Retry idempotent release of known-completed admissions after storage recovers. Bind admissions to a registered server boot and fence dispatch when that boot is drained or terminated. Never reclaim an active admission merely because time elapsed. Recover admissions from a terminated process only through explicit drain/reconciliation after the operator confirms its upstream work has stopped; use a one-use drain proof and reject stale dispatches.

Management uses Connect RPC and generated Protobuf contracts; inference transports preserve the pinned wire interfaces rather than being replaced with Connect RPC. See [protocol ownership](protos-aiprouter-management-contract.md).

## Storage

### PostgreSQL data, encryption, retention, and deletion

#### Storage and encryption

- Do not permanently retain raw prompts or responses.
- Store necessary protocol/continuation state temporarily in encrypted PostgreSQL storage, isolated by organization and user.
- Support continuation across server instances and process restarts.
- Put temporary state in a separate PostgreSQL storage area that can be excluded from ordinary backups. Loss of continuation state during disaster recovery is accepted.
- Encrypt stored provider credentials.
- Operators inject versioned encryption master keys separately from the database.
- Rotate during maintenance: decrypt with the previous key, re-encrypt with the new key, verify completion, then remove the previous active key.
- Operators retain any previous keys needed to restore old backups.

#### Continuation limits

Apply these inspected reference limits **per user within each organization**:

- Responses continuation: 24-hour TTL, at most 1,000 entries, 1 GiB total retained storage; also cap an optional resident memory cache at 64 MiB.
- Tool-call reasoning replay: one-hour TTL, at most 64 entries and 256 KiB.
- Bridged web-search result replay: one-hour TTL, at most 64 entries and 512 KiB.
- Retained compaction reasoning: 24-hour TTL, at most 256 entries and 512 MiB total, with a 32 MiB limit per entry. Store overflow in the `compaction-reasoning` namespace in encrypted temporary PostgreSQL storage. Bind reads, writes, and merges to the selected account and credential version as well as the organization, user, key, and membership. Credential replacement or deletion invalidates reuse.
- Compaction handoff routes: 24-hour idle TTL and at most 2,048 entries per user within an organization, across that user's keys. Each encrypted record contains a route payload of at most 4 KiB and binds the key, lane, selected account, credential version, and route/pool configuration versions. Limit the model identifier to 512 UTF-16 code units. Validate current ownership, credential usability, and configuration at publication and lookup, and validate the same configuration at the physical send gate. Persist required successful compaction receipts before clearing a handoff; a receipt-write failure must preserve the handoff and return an explicit error.

Readable compaction reasoning retention requires explicit opt-in. Keep retained reasoning outside the summarizer and preserve portable continuation frames. Default inline limits are 20% of the authenticated compacting model's context window and 100,000 tokens, using the smaller limit; an unknown context window uses the token limit alone. Overflow references must not expose filesystem paths. Missing, expired, evicted, or unavailable required retained state produces an explicit error. A required archive write that exceeds its bounds or fails must stop compaction before the upstream send. Do not substitute permanent filesystem archives or R2 objects for this temporary SQL policy.

The reference's memory-only replay caches move to encrypted temporary PostgreSQL storage under this explicit AIPRouter decision. Inventory other provider-specific state against the fixed reference rather than claiming these three stores are the whole system.

Evict oldest state when count/byte limits are exceeded. Return an explicit continuation error for missing/expired/evicted state; never silently start a new conversation without its context.

Sources: [Responses state](https://github.com/lidge-jun/opencodex/blob/88aaa754566d119b6b8fc898f8fb47602387d82e/src/responses/state.ts), [reasoning replay](https://github.com/lidge-jun/opencodex/blob/88aaa754566d119b6b8fc898f8fb47602387d82e/src/responses/reasoning-replay-cache.ts), [search replay](https://github.com/lidge-jun/opencodex/blob/88aaa754566d119b6b8fc898f8fb47602387d82e/src/responses/bridge-search-replay-cache.ts).

#### Metadata retention

- Per-request usage, estimated cost, model, and error metadata: default **30 days**, operator range **1–90 days**.
- Daily usage/cost aggregates: default **365 days**, range **30–730 days**.
- Organization and instance-setting audit records: default **90 days**, range **30–365 days**.
- No indefinite retention for these records.
- Deny reads/reuse immediately at expiry, even before physical cleanup.
- Run expiry cleanup every minute.

#### Leaving/removal

- Immediately invalidate that user's organization-scoped keys and block new requests.
- Immediately delete that organization's personal provider credentials, configuration, and temporary conversation state. Rejoining requires reconfiguration.
- Keep usage metadata for its configured retention period.
- Allow only already-dispatched upstream responses to finish.
- Stop additional retry, account switching, and token refresh, and prevent completed work from recreating deleted data.

#### Organization/account deletion

- Organization deletion is owner-only and immediately deletes its credentials, settings, temporary state, usage records, and organization audit records.
- Block new requests; allow only already-dispatched upstream responses to finish without recreating deleted records.
- Provide self-service user account deletion after the user transfers or deletes owned organizations.
- Remove all memberships and personal identifying information.
- Anonymize the user's remaining usage records and actor identity in surviving audit records.
- Expiry of existing backups remains governed by the operator's backup policy.

### Generated media artifacts and R2

Use operator-configured **Cloudflare R2 and signed download URLs**, with organization/user authorization when issuing a URL.

The reference [artifact implementation](https://github.com/lidge-jun/opencodex/blob/88aaa754566d119b6b8fc898f8fb47602387d82e/src/images/artifacts.ts) defaults to retaining 200 files and pruning oldest files by modification time. It has no time-based artifact TTL. AIPRouter explicitly adds a TTL and applies the limits per organization/user:

- At most **200 generated image files per user within an organization**.
- Retain the reference's **50 MiB per-image** and **100 MiB decoded image total per response** limits.
- Delete oldest files when the count is exceeded.
- Add a **24-hour TTL**; count-based eviction may remove a file earlier.
- Download signed URL lifetime: at most **five minutes**, shortened to the artifact expiry when earlier.
- A holder of an issued signed URL can use it until expiry; authorization is checked at issuance. Earlier object deletion makes the URL unusable.
- Apply organization/user ownership, expiry, and deletion to artifact metadata and objects. Do not describe a deleted or expired artifact as retrievable.
- Generated images are temporary output, not an exception permitting indefinite raw-response storage.
- Document R2 setup and the signed-URL access boundary in the public installation/configuration guidance.

The pinned hosted video helper also uses this temporary R2 boundary. It does not introduce a public `/v1/videos` API. Accept `video/mp4` output with a **200 MiB per-video** limit and a **600 MiB total per helper turn** limit. Keep the image limits above unchanged. Images and videos share the **200-file organization/user quota**, oldest-first eviction, **24-hour TTL**, and signed URL lifetime above. Apply the same authorization, deletion, and publication fences to both media types. Each indirect download must pass the destination policy and the original request's physical-send budget. Return an opaque artifact identifier and an authorized signed download URL; never return a server-local path.

### Usage, pricing, and audits

#### Usage and estimated cost

- No service billing, charging, subscriptions, invoices, or payment integration.
- Display provider usage and estimated costs.
- If usage is absent or price unknown, show unknown rather than zero.
- Aggregates include known values and missing-record counts.
- Adopt the fixed reference's price sources/update mechanism: bundled price snapshots updated and regenerated for distribution, rather than adding an unrelated periodic live pricing API.
- Instance operators alone can set common model price overrides; individual users cannot change prices.
- Fix the applied price, provenance, and estimated cost when processing each request.
- Subsequent price changes affect new requests only; do not reprice historical requests or daily aggregates.
- This intentionally replaces the reference's display-time repricing behavior.
- Preserve applicable provider/model, service-tier, and cache-token pricing semantics from the reference.

Sources: [metadata generation](https://github.com/lidge-jun/opencodex/blob/88aaa754566d119b6b8fc898f8fb47602387d82e/scripts/generate-model-metadata.ts), [cost calculation](https://github.com/lidge-jun/opencodex/blob/88aaa754566d119b6b8fc898f8fb47602387d82e/src/usage/cost.ts).

#### Audits

Persist and expose audit records for permission changes, invitations, API-key issuance/revocation, provider-connection changes, and instance-setting changes.

- Organization records: visible to that organization's owner/administrators.
- Instance-setting records: visible only to instance operators.
- No secret values or raw prompts/responses.
- Apply the retention, organization deletion, and account anonymization policies above.

## Security

Every lookup, write, cache entry, retry, job, and metadata access must enforce both organization and user ownership. Current membership, authority, key validity, and DB availability govern subsequent actions, including after asynchronous completion. Device handoff, removal, deletion, expiry, and restore rules must hold under concurrency. Do not allow stale work to recreate deleted private state. The exact lifecycle requirements above also bind shared libraries and generated-client consumers.

## Logging

Use structured logs and request identifiers. Never log provider secrets, API keys, device secrets, raw prompts, or raw responses. Distinguish unknown usage, write failures, cancellation, limits, and upstream/gateway errors. See the [observability contract](servers-aiprouter-operations-contract.md).

## Build and Test

Follow the release gates and required scenarios in [operations](servers-aiprouter-operations-contract.md). Run root `go test ./...` for Go changes. Passing package tests does not establish complete runtime or live acceptance.

## Dependencies and Integrations

- [protos-aiprouter-management-contract](protos-aiprouter-management-contract.md)
- [packages-aiprouter-libraries-contract](packages-aiprouter-libraries-contract.md)
- [cmds-aiprouter-client-contract](cmds-aiprouter-client-contract.md)
- [apps-aiprouter-console-contract](apps-aiprouter-console-contract.md)
- [servers-aiprouter-operations-contract](servers-aiprouter-operations-contract.md)

## Change Triggers

Update this contract, the project index, and affected producer/consumer contracts together when interfaces, ownership, or lifecycle rules change. Follow the [instruction-update policy](README.md#instruction-update-policy); behavior belongs in contracts, not instruction files. Record implementation status, validation results, and unresolved limits in GitHub records or CI artifacts.

## References

- [Project index](project-aiprouter.md)
- [Repository defaults](repository-defaults.md)
- [Workflow](repository-workflow-contract.md)
- [Environment](repository-environment-contract.md)
- [Licensing](repository-license-contract.md)
- [Dependency security](repository-dependency-security-contract.md)

## Google OAuth deployment configuration

Google Antigravity OAuth login, completion and refresh require both `AIPROUTER_GOOGLE_OAUTH_CLIENT_ID` and `AIPROUTER_GOOGLE_OAUTH_CLIENT_SECRET` from the service environment. No upstream installed-application credentials are embedded. Missing values refuse these exchanges before network access; other provider paths remain available. Keep populated values outside version control. The deployment must supply an authorized OAuth application compatible with the provider, scopes and callback; source fixtures do not establish live acceptance.

Admission counting uses separate bounded scans for requests in the last minute and all unreleased admissions. Released historical IDs remain available for idempotent usage recording without participating in active-admission scans.

Reported one-hour cache creation is a subset of total cache writes. Preserve its presence through wire parsing, streaming, helper-send accumulation and persisted pricing estimates so long-duration premiums and operator overrides apply to the reported subset.

Context management requires explicit capability opt-in for the current user/organization/key. A successful personal OpenAI OAuth Responses send records an encrypted candidate for its allowlisted session metadata and exact account credential version. Client registration confirms that candidate. Confirmed bindings expire after 24 hours, are limited to 256 per key and are excluded from backups. Account ambiguity, stale credentials, missing proof or missing opt-in fails closed with 409. Disable removes bindings; every context physical send rechecks capability, key, membership, receipt, expiry and exact account/version under the dispatch gate. No external history or notes content is stored.

Pool resolution and dispatch-time candidate rebuilding skip individual accounts with an active credential refresh or a reauthentication requirement. Store and database failures remain request failures; an unavailable account does not suppress other eligible pool members.
