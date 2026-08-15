### Instructions for `servers/`

- Follow root `AGENTS.md` and the owning project/domain contracts.
- Keep service contracts, ownership, security, retention, and deployment prerequisites in `docs/` before runtime implementation.
- Write Go code and comments in English; use `log/slog` structured logging and never log secrets or sensitive payloads.

### Scope in This Domain

- `servers/devhud-api`: planned stateless Go DevHud API.

### DevHud Rules

- `servers/devhud-api` uses fixed development port `46307`, PostgreSQL, Cloudflare R2 signed uploads, external Logto, Connect RPC, and a non-root OCI artifact.
- It must not proxy GitHub or upload bodies, broker credentials, poll Decks, receive GitHub webhooks, or persist Deck results.
- CreateUpload must atomically reserve the rolling signed-URL issuance quota before issuing a signed URL; failed issuance rolls back the reservation, and FinalizeUpload validates the reservation without charging it again. FinalizeUpload must revalidate ownership/block state and the exact staging object and server-issued upload-group binding, verify size/checksum/image safety, atomically recheck or reserve all other applicable upload quotas, reject replay, and clean invalid staging objects. Deletion and quarantine must replace or remove origin bytes before invalidating public CDN copies; the operation is not effective while the original remains retrievable from a CDN cache. Account purge must remove or irreversibly pseudonymize upload metadata.
- RestoreAccount and final purge must serialize through an atomic account-state transition or row lock: purge claims the account before destructive work, and a successful restore cannot follow irreversible deletion. A dedicated idempotent `devhud-api-sweeper` deployment owns staging expiry and post-recovery purge, coordinated across instances with a database lease or advisory lock.
- CORS must use the exact DevHud development and pinned Tauri origins, with explicit Connect preflight methods and headers, exposed `x-devhud-correlation-id`, and no wildcard origin/header policy. The R2 staging bucket separately allows only those origins, `PUT`/`OPTIONS`, `Content-Type`, and `x-amz-checksum-sha256`, with `ETag` exposure.
- `GetBootstrap` is unauthenticated; settings, uploads, diagnostics, and account methods use authenticated user/ownership policies; user upload listing is owner-filtered, user upload deletion verifies ownership, user upload listing is bounded and paginated, and AdminService requires `devhud-admin`.
- Bootstrap publishes the native callback, the `desktop`/`ios`/`android`/`admin` public client IDs, and the exact deployment-configured admin redirect; the admin SPA uses PKCE with state/nonce validation. The API deployment serves the signed updater manifest route, and the separate sweeper deployment ships as a signed/provenanced OCI image. RestoreAccount clears deletion state only and never an administrative block.
- Uploads are scoped to a server-owned submission UUID spanning all upload groups, capped at 10 finalized images; signed checksums are 32 raw bytes with standard Base64 conversion only for the R2 header, versioned staging objects are required for conditional promotion, and PNG IHDR dimensions must be rejected above 4096×4096 or 16,777,216 pixels before decoding.
- Keep `docs/servers-devhud-api-contract.md`, `docs/protos-devhud-v1-contract.md`, and `docs/project-devhud.md` synchronized with every interface or operational change.
