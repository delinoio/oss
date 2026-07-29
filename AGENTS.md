### Instructions

- Use the `@docs/` directory as the source of truth for project contracts and implementation documents.
- All repository-wide rules must be defined in the appropriate AGENTS.md.
- List files in `docs/` before starting each task, and keep `docs/` up-to-date.
- After completing each task, update the relevant `AGENTS.md` and `docs/` files in the same change when policies, structure, or contracts changed.
- For documentation authoring and editing tasks, do not arbitrarily omit, delete, or simplify requested or source-backed content; if content, scope, or intent is ambiguous, ask the user before deciding what to remove, merge, or reinterpret; if the documentation change affects repository or domain policy boundaries, update or create the relevant `AGENTS.md` file in the same change when needed.
- Public documentation surfaces must not document repository-internal implementation details. Keep internal source-of-truth contracts, architecture notes, repo-local paths, and operational internals in `docs/`; curate public docs under `apps/*-docs` and `apps/public-docs` around user-facing behavior, supported workflows, stable public interfaces, and maintainer-facing paths only when those paths are explicitly part of the public contract.
- Write all code and comments in English.
- When introducing a workaround, leave sufficient comments that explain why it exists, its scope, and the conditions for removing it.
- Prefer enum types over strings whenever possible.
- If you modified Rust code, run `cargo test` from the root directory before finishing your task.
- If you modified frontend code, run `pnpm test` from the frontend directory before finishing your task.
- Commit your work as frequent as possible using git. Do NOT use `--no-verify` flag.
- Run `git commit` only after `git add`; once files are staged, commit without unnecessary delay so staged changes are preserved in history.
- Committing may require workspace binaries (for example, git hooks). If required binaries are missing, run `pnpm install` at the repository root and retry the commit.
- After addressing pull request review comments and pushing updates, mark the corresponding review threads as resolved.
- When no explicit scope is specified and you are currently working within a pull request scope, interpret instructions within the current pull request scope.
- Do not guess; rather search for the web.
- Debug by logging. You should write enough logging code.
- Write sufficient logs for debugging and operational troubleshooting.
- Prefer structured logging libraries for business and system logs (Go: `log/slog`, Rust: `tracing`).
- Prioritize Connect RPC-based communication for business flows over Tauri-specific bindings.
- Prefer React Query for frontend server-state management when it is available.
- When using React Query with Connect RPC, use `@connectrpc/connect-query` from `https://github.com/connectrpc/connect-query-es`.
- When accessing `github.com`, use the GitHub CLI (`gh`) instead of browser-based workflows when possible.
- Run GitHub CLI (`gh`) commands outside sandbox restrictions by default; use the required approval flow when escalation is needed.
- When writing shell commands or scripts, treat backticks and command substitution carefully, prefer `$(...)` over legacy backticks, and apply strict escaping for all dynamic values.
- If an operation is blocked by sandbox restrictions, retry it without sandbox restrictions using the required approval flow.

### Monorepo Structure Map

- `docs/`: Source of truth for project contracts and repository documentation.
- `apps/`: User-facing apps (React Native and documentation web surfaces).
- `crates/`: Rust crates and Rust-based tooling.
- `protos/`: Shared Connect RPC proto contracts used by multi-runtime projects.
- `cmds/`: Go command tools for workflow orchestration.
- `servers/`: Backend services and APIs.
- `packaging/`: Package-manager template assets for release automation.
- `.agents/skills/`: Workspace-local Codex skills and reusable agent workflows.

### Canonical Directory Map

- `docs/README.md`: Canonical docs catalog and naming rules.
- `docs/repository-defaults.md`: Repository-wide default technology choices.
- `docs/project-template.md`: Required structure for `project-<id>` index docs.
- `docs/domain-template.md`: Required structure for domain-level contract docs.
- `docs/project-<id>.md`: Canonical project index docs (ownership + domain-doc index + cross-domain invariants).
- `docs/<domain>-<project-or-component>-<contract>.md`: Canonical domain contract docs (`apps`, `cmds`, `servers`, `crates`, `protos`, `packages`).
- `docs/project-binpm.md`: binpm binary package manager project index.
- `docs/apps-binpm-docs-foundation.md`: binpm Rspress documentation app, route, validation, canonical production URL, and Cloudflare Pages deployment contract.
- `docs/project-cargo-mono.md`: Cargo subcommand project index.
- `docs/project-nodeup.md`: Node.js version manager project index.
- `docs/project-with-watch.md`: Command rerun watcher CLI project index.
- `docs/project-derun.md`: Derun CLI project index.
- `docs/project-ttl.md`: TTL compiler project index.
- `docs/project-mpapp.md`: Expo mobile app project index.
- `docs/project-thenv.md`: Thenv multi-component project index.
- `docs/project-public-docs.md`: Public docs app project index.
- `docs/project-serde-feather.md`: Serde Feather multi-crate project index.
- `docs/project-rustia.md`: Rustia multi-crate project index.
- `docs/crates-binpm-foundation.md`: binpm Rust CLI, release asset source selection, global cache, and local tooling contract.
- `docs/crates-with-watch-foundation.md`: with-watch CLI and watcher foundation contract.
- `docs/crates-rustia-core-foundation.md`: Rustia core runtime LLM data contract.
- `docs/crates-rustia-llm-foundation.md`: Rustia aisdk tool adapter contract.
- `docs/crates-rustia-macros-foundation.md`: Rustia macros derive contract.
- `docs/cmds-ttl-language-contract.md`: TTL language syntax/type/invalidation/code-generation contract.
- `docs/apps-nodeup-docs-foundation.md`: Nodeup Rspress documentation app, route, validation, and Cloudflare Pages deployment contract.
- `docs/project-devhud.md`: DevHud signed-out base plus bounded Deck/RealQA ownership contract.
- `docs/apps-devhud-foundation.md`: DevHud React/TypeScript/Rsbuild plus Tauri desktop CEF, mobile webview, Deck/RealQA client/native boundaries, security, diagnostics, CI, support, and release foundation contract.
- `docs/servers-devhud-deck-foundation.md`: Planned client-initiated Deck Go/PostgreSQL/GitHub.com service contract.
- `docs/protos-devhud-deck-api-contract.md`: Planned `devhud.deck.v1` Connect contract.
- `docs/servers-devhud-realqa-foundation.md`: Implemented inactive RealQA Go/PostgreSQL/sqlc preset/tracker/auth/deletion and internal GitHub.com provider foundation plus planned R2 submission and public-image contract.
- `docs/protos-devhud-realqa-api-contract.md`: Implemented private `devhud.realqa.v1` source and generated Connect package contract.
### Project Identifier Contract

Treat project IDs as stable enum-style values:

```ts
enum ProjectId {
  Binpm = "binpm",
  CargoMono = "cargo-mono",
  Nodeup = "nodeup",
  WithWatch = "with-watch",
  Derun = "derun",
  Ttl = "ttl",
  Mpapp = "mpapp",
  Thenv = "thenv",
  SerdeFeather = "serde-feather",
  Rustia = "rustia",
  PublicDocs = "public-docs",
  DeliDev = "delidev",
  Delibase = "delibase",
  DevHud = "devhud",
}
```

### Project Domain Ownership

- `nodeup` -> `crates/nodeup`, `apps/nodeup-docs`
- `binpm` -> `crates/binpm`, `apps/binpm-docs`
- `with-watch` -> `crates/with-watch`
- `cargo-mono` -> `crates/cargo-mono`
- `derun` -> `cmds/derun`
- `ttl` -> `cmds/ttlc`
- `mpapp` -> `apps/mpapp`
- `thenv` -> `cmds/thenv`, `servers/thenv`
- `serde-feather` -> `crates/serde-feather`, `crates/serde-feather-macros`
- `rustia` -> `crates/rustia`, `crates/rustia-llm`, `crates/rustia-macros`
- `public-docs` -> `apps/public-docs`
- `delidev` -> `apps/delidev-app`
- `delibase` -> `servers/delibase`, `protos/delibase`
- `devhud` -> `apps/devhud`, `servers/devhud-deck`, `protos/devhud-deck`, `servers/devhud-realqa`, `protos/devhud-realqa`
- `servers/internal` -> repository-shared Go infrastructure currently consumed by `delibase`; planned DevHud servers require the documented explicit compatibility review before consuming provider-agnostic subsets, and no consuming project owns it.

### DeliDev and delibase Contract Boundary

- Canonical future origins are `https://deli.dev` for DeliDev and `https://delibase.deli.dev` for delibase. Documentation must not imply either service is activated or deployed by issue #722.
- `apps/delidev-app` owns the React/TypeScript/Rsbuild PWA, stable routes, browser-safe configuration, static Pages artifact, sensitive-cache exclusions, the planned Deck connection-management UI in the existing `/account` and `/o/:orgSlug/settings` sections, and the planned Deck mobile `/auth/devhud/callback` plus exact-path Apple/Android association artifacts. Its Deck client may consume only `DeckIntegrationService`. A one-use `StartGitHubConnection` target remains memory-only and may navigate the top-level same tab only through a Deck-specific helper that validates the exact HTTPS GitHub.com OAuth/configured-App path, configured client ID/App slug/callback, and credential/fragment/port/path restrictions; it grants no generic navigation. Callback and authorization query credentials must bypass the SPA fallback, service worker, history-state payloads, logs, analytics, and caches, fixture association identities are test-only, and release identities are externally injected.
- DeliDev billing and usage must preserve server-authoritative role boundaries: Members receive only shared available credit plus server-filtered personal/effectively accessible-team usage, while Owners/Admins may receive subscription/payment state, current-period and overage context, organization-wide attribution, the complete opaque-cursor ledger, and billing mutations. Hosted payment navigation must remain on credential-free HTTPS `polar.sh` pages; the browser must not handle cards, calculate charges, or issue `UsageService` mutations. Billing, ledger, usage, checkout, portal, and overage data are memory-only and network-only, mutations are disabled offline, subscription-checkout retries retain their account-and-organization-scoped route-surviving key and normalized return URL until hosted navigation succeeds, and component-local portal retries retain their pending key and normalized return URL across ambiguous failures until the session response succeeds.
- `servers/delibase` owns Go/PostgreSQL/sqlc persistence, organization/team/invitation policy, immutable organization/team-membership and provider identities, historical subscription and billing-period snapshots, append-only billing and reservation invariants, provider integrations, server configuration, and signed tagged GHCR artifacts. Retained refunds preserve their organization-scoped provider-order binding and monotonic provider-event/reversal/chargeback state; refund reversal increases must transactionally match retained paid-cycle totals. Retained paid cycles immutably snapshot their provider-order, subscription, billing-period, and original period-bound identities, increase reversals only to match retained refunds, and block deletion of their referenced subscriptions and billing periods. Outstanding refund shortfall carries into a renewed current billing period after its paid grant, and append-only reconciliation adjustments remove that carried shortfall when reordered grants clear the organization-level debt. Rollover credit remains usable after the last billing period ends; credit-only usage may retain no billing-period snapshot when no period contains its commit, while every overage settlement requires the containing current period. Polar usage delivery reports only nonzero locally settled overage, with provider meter units denominated as chargeable USD micro-units; every such usage record requires exactly one durable Polar outbox event at commit whose immutable payload matches the usage organization, record, event-name snapshot, commit timestamp, and overage micro-units.
- Delibase subscription checkout creation is serialized per organization across all caller idempotency keys. A durable provider-idempotency attempt lets the same request recover an ambiguous or post-provider-success failure while blocking distinct keys; successful checkout, audit, and idempotency finalization is atomic, and a definitive provider rejection clears the attempt. A successful hosted checkout blocks another checkout until provider expiry. Billing summaries prefer an active subscription, otherwise report an unexpired checkout as checkout-pending, and select inactive subscription history by provider-event recency.
- Authoritative Polar refunds and chargebacks continue against retained billing history after organization deletion, and billing-portal session creation uses a distinct typed audit event from subscription checkout creation. Accepting account or organization deletion must atomically enqueue one immutable lifecycle outbox call for Deck and one for RealQA, keyed by the delibase deletion-job UUID and delivered with separate feature-scoped M2M identities to the typed `DeleteFeatureData` lifecycle mode. Delibase configuration owns each feature's exact outbound origin/audience and distinct Logto client ID/secret; once lifecycle enqueue/dispatch exists, startup fails before deletion acceptance or work claiming unless both complete identity sets validate. Delivery is idempotent, treats absent feature data as success, and follows the retained outbox/dead-letter retry contract.
- `protos/delibase/v1` owns the versioned `delibase.v1` source contract and generation boundary for exactly six Connect services: `AccountService`, `OrganizationService`, `TeamService`, `CatalogService`, `BillingService`, and `UsageService`.
- `protos/delibase/gen/go`, `protos/delibase/gen/ts`, and the workspace package `@delinoio/delibase-connect` are reproducible derived views of that source. Each implemented shared contract owns component-local generation/check entrypoints, outputs, and descriptor; root `pnpm generate:proto` and `pnpm check:proto` aggregate delibase, Deck, and RealQA in fixed order without cross-component cleanup. Delibase generation still builds the package's loadable `dist` exports, and its compatibility check always uses the immutable `delibase.v1` descriptor baseline.
- Inbound authentication accepts Logto user or M2M bearer access tokens, never raw client secrets. Authenticated invitation preview and acceptance use the invitation bearer token without requiring a pre-existing local account, organization membership, or team access; acceptance creates the active local account with a privacy-safe non-empty placeholder display name when needed, and each account may consume an invitation only once.
- Team RPCs require organization membership before resolving a requested team identity so non-members cannot probe team existence.
- Idempotency keys are scoped to the authenticated user subject and operation for human RPCs, or the authenticated service identity and operation for M2M RPCs. Live-user usage-operation request digests additionally bind the forwarded user subject so an M2M key cannot replay a response across end users. Bounded background reserve/commit/release digests instead bind the durable authorization, authenticated service identity, `REALQA_STORAGE` purpose, feature-resource UUID, `UTC_DAY` period, and units; commit/release also bind the reservation ID. Resource-deletion digests bind authorization, service, purpose, resource, and expected revision. These operations are M2M-only and never require or store a forwarded user bearer.
- Bounded background authorization remains inside the existing `BillingService` and `UsageService`, preserving exactly six `delibase.v1` services. UUID v7 grants bind the authorizer, personal/organization owner, organization/team where applicable, exact service and meter, purpose/resource/period, maximum units, closed lifecycle status, revision, and timestamps. Public meter metadata exposes stable authorization service targets without Logto client identifiers or credentials. Reserve accepts only the current or immediately preceding UTC day according to server time; commit/release use the reservation's stored period. Resource-deletion notification transitions `ACTIVE` to `RESOURCE_DELETED` only at the current expected revision and returns an existing `REVOKED`, `ACCESS_LOST`, `RESOURCE_DELETED`, or `OWNER_DELETED` status/current revision without transition to the exact bound service whose expected revision predates closure; future/unknown revisions and substitutions fail closed. Deck has no v1 purpose. Human authorization create/revoke and M2M authorized reserve/commit/release/resource-deletion notification have distinct idempotency operations, opaque list pagination, and stable closed substitution/access/status/limit/replay errors.
- Invitation acceptance and revocation use distinct stable `IdempotentOperation` values; invitation creation does not carry idempotency fields.
- `servers/internal` owns reusable Go authentication, Connect, request/trace, redaction, HTTP, logging, and UUID v7 infrastructure only; it must not own delibase business rules.
- Changes to routes, RPCs, roles, team hierarchy, invitations, billing, usage, authentication, generated clients, shared packages, validation commands, Pages scope, or GHCR scope require synchronized project/domain docs and the relevant `AGENTS.md` files.

### DevHud and DeliDev Contract Boundary

- Deck device writes carry request-only shortcut configurations with closed modifier/key enums and widget configuration only; effective shortcut conflict state/revisions plus widget snapshots/revisions are server-authored. Initial device registration omits an expected revision, while lease renewal requires the current device revision and authenticated `GetDevice` reloads current server-authored state after restart/stale conflict. List results expose individual/team reviewer identities, current assignees and labels for removal actions, explicit open/closed/merged lifecycle state independently from draft state, supported mutations, available merge methods, and the synchronized PR revision required by mutations; bounded mutation-candidate search supplies permission-filtered user/team/label operands for advertised add actions, installation rows include displayable stable GitHub account identity, and `UpdateView` may change notification preferences while owner/kind remain immutable.
- DevHud's signed-out base shell stays usable without DeliDev, Logto, Deck, RealQA, or any online service. Existing closed-registry, exact CEF pin, bundled-asset, sandbox, capability, diagnostics/export, backup-exclusion, and device-local reset guarantees remain mandatory.
- `apps/devhud` is the only full DevHud feature client/native path and contains the implemented unpublished exact-origin Chrome MV3/desktop Native Messaging capture bridge. The implemented private Deck and RealQA wire contracts belong at `protos/devhud-deck` and `protos/devhud-realqa`; the bounded Deck server foundation and implemented inactive RealQA preset/tracker/auth/deletion/internal GitHub.com provider foundation belong only at `servers/devhud-deck` and `servers/devhud-realqa`. RealQA repository issue schemas preserve additive issue type, Issue Form input/textarea prefill, textarea render-language, and dropdown multiplicity metadata. DeliDev owns only the planned `DeckIntegrationService` connection-management UI in its existing settings sections and the static verified-link callback/association artifacts described above; it must not consume the RealQA package or Deck views, pull-request data, mutations, devices, notifications, or widgets. Never move other DevHud feature implementation into DeliDev/delibase paths or add another DevHud ownership path without synchronized contracts.
- The only account exception is DeliDev identity through Logto Authorization Code with PKCE. Desktop uses a one-shot random-port loopback callback at the stable registered `/auth/callback` path; Deck mobile uses the exact verified `https://deli.dev/auth/devhud/callback`. Persist only the authorized features' refresh grants/device-session key, the non-secret auth cleanup-required tombstone written before vault deletion, and, while an active or cleanup-pending Deck registration exists, its single-purpose `UnregisterDevice` revocation grant in the OS vault; access/ID and forwarded delibase-audience tokens remain memory-only. The auth tombstone must survive failed deletion and block retained-grant trust after restart until cleanup succeeds. One account is active per OS user/device. Startup treats only request-level transport unavailability as prior-session-offline and requires reauthentication for invalid or revoked refresh grants. Desktop reads the public Logto issuer/app ID from its launch environment; mobile embeds them during Rust compilation and never relies on a runtime shell environment.
- Future exact origins are `https://deck.deli.dev`, `https://realqa.deli.dev`, and `https://assets.realqa.deli.dev`. They authorize bounded feature contracts only and do not claim DNS, deployment, R2 infrastructure, production secrets, registration, publication, or activation. Feature clients must not load remote UI or arbitrary assets; provider GitHub/delibase/R2 administration remains server-side.
- Deck is authorized only for authenticated desktop/mobile/tray/shortcut/notification/native-widget workflows plus connection management from the existing DeliDev settings sections. RealQA is authorized only for authenticated desktop capture/editor/offline-draft workflows and its exact-origin signed Chrome MV3 native host; no mobile RealQA. Deck and RealQA require separate minimal-permission GitHub Apps, support GitHub.com only, and keep production-facing catalog records disabled. Deck's future disabled delibase meter is `(devhud, deck_github_pull_request_refresh)`, unit `provider_refresh`, precision zero, 50 USD micros/unit, minimum reservation TTL 86,400 seconds, and an allowlist containing only Deck; one dispatched provider refresh is one unit. Non-dispatching `GetRefreshPreflight` returns the server-derived price plus an opaque short-lived token bound to the prospective `RefreshView` identity and origin; creation of every new automatic/widget/manual/view-open/shortcut attempt requires the unexpired token and current authoritative catalog/TTL validation, while only manual UI displays the price warning and an authenticated exact identity-and-digest replay is looked up first and returns or resumes after token expiry or catalog change. RealQA's future disabled meters are `(devhud, realqa_image_transfer)`, precision-zero `encoded_mib` at 500 USD micros/unit, and `(devhud, realqa_image_storage)`, precision-zero `mib_day` at 2 USD micros/unit, both allowlisted only to RealQA. Transfer reserves aggregate ceiling-MiB units from all accepted declared upload lengths and commits the same calculation over the successfully verified subset, excluding server-sanitized output growth; storage authorization maximum and daily settlement use the exact checked formulas in the RealQA server contract, and zero numerators skip delibase billing mutations. Divergent mappings fail before submission/settlement. Deck and RealQA each validate a dedicated outbound exact delibase origin/audience/service UUID/client ID/secret set and least-privilege live-usage token acquisition before billed work; initial live reservation combines that M2M bearer with the current forwarded user bearer, while RealQA recurring calls use the same feature-scoped identity with authorized-usage scopes and no forwarded bearer. Deck additionally requires a future additive delibase same-service, single-reservation finalization grant before implementation/activation so an encrypted durable billing attempt can commit/release with a fresh Deck M2M token after request loss without retaining a forwarded bearer or authorizing another provider call.
- The internal registry remains closed and source-controlled. No public plugin/tracker SDK, third-party view/provider, remotely supplied component tree, runtime code download, arbitrary remote UI, or public API is authorized. Remote client/extension telemetry remains prohibited; only redacted server operational logs/metrics/traces/audits are permitted.
- Networking remains deny-by-default except the desktop-only signed updater, exact Logto/DeliDev auth, exact Deck/RealQA origins, same-origin signed RealQA-assets PUT/public GET, verified mobile auth callback, exact-origin Chrome Native Messaging boundaries, and typed OS-browser handoff to exact GitHub.com OAuth/configured-App authorization or natively constructed pull-request shapes. DeliDev's Deck settings use their separate validated one-use top-level same-tab GitHub authorization handoff. Updater discovery is limited to the unauthenticated GitHub Releases API for source-backed `delinoio/oss` `devhud@v*` tags, rejects drafts/unrelated releases/invalid SemVer/unsupported targets and an invalid or missing signed manifest before asset selection, downloads only GitHub Release manifests/assets, and has no mobile endpoint or permission. Bundled webviews never receive browser access to those remote origins: authenticated feature RPCs use a private native Connect transport with a compile-time service/procedure-to-origin table, the sole signed-out RPC is native `DeckDeviceService.UnregisterDevice` cleanup using its OS-vault single-registration grant, RealQA upload uses a separate exact-origin signed-PUT operation, and only the authenticated RealQA desktop window receives its closed platform-bounded capture procedures. No boundary accepts an arbitrary URL, method, header, redirect, generic frontend network request, generic opener, or generic screen authority, and `http://tauri.localhost` is not a feature-server CORS origin. Add narrow per-window/per-command allowlists without generic frontend filesystem/screen/process/store/network/opener authority.
- RealQA composer source pixels and nondestructive operations remain local to the draft/session boundary. Logout and reset clear every accepted source and cancel in-flight acceptance before removing authentication or pairing state. Approval must bind the expected native source revision, reject stale replacements before rendering, admit at most one process-wide CPU-heavy acceptance or flatten operation through a shared permit before either decode path, spawn flattening away from the Tauri command thread, and release the permit when processing finishes or fails. Its native image-safety path validates every closed edit operation, crop and sequence before deterministic flattening, re-encodes approved PNG/WebP without source metadata, and accounts for the replacement output under the same per-image/session limits. Large source-derived previews must cap retained renderer pixels without changing full-resolution native output and warn when a capped preview cannot represent the native pixelation block grid or blur radius. Only the typed approved result may form a future upload payload; raw source bytes never enter upload payloads, server state, logs, diagnostics, or clipboard side effects.
- Preserve exact lifecycle distinctions. Deck's only offline PR cache is the minimal encrypted widget snapshot; ordinary Deck shows offline state. `CreateView`, each logical `RefreshView`, and each logical `RegisterDevice` creation/renewal are replay-safe through client-generated UUID v7 identities; registration exact replay returns the identical registration ID, lease, and single-purpose revocation grant, while a renewal additionally requires the current device revision so stale configuration cannot replace newer state. The refresh attempt durably covers reservation, provider dispatch, and billing finalization so ambiguous retries cannot dispatch or charge twice. The attempt envelope-encrypts only its exact single-reservation finalization grant through terminal commit/release or observed expiry; a billing-only worker resumes stable commit/release after request loss or restart with a fresh same-service M2M bearer and cannot issue provider requests or use the grant for reserve, substitution, or another reservation. Canonical raw queries, identity-bearing typed builder clauses, and retained current-snapshot repository/title/author/reviewer/assignee/label fields use managed at-rest encryption and authorized decryption. Opaque notification events resolve only through authenticated `ResolveNotificationEvent` for the matching active registration after current authorization/detail opt-in checks, otherwise generic text is used without provider refresh. Deck logout/reset first revoke the device push registration; offline/ambiguous revocation disables local/platform delivery and retains only the registration ID, lease expiry, and single-purpose `UnregisterDevice` revocation grant in an OS-vault cleanup tombstone so cleanup can finish before later registration even after an account switch. Logout then clears device tokens/PR data/snapshots; disconnect additionally clears provider/cache/notification/widget data while retaining disconnected views; reset clears device tokens/snapshots/effective shortcuts but not server views/connections; owner-scoped idempotent `DeleteFeatureData` and account/org deletion follow the Deck server contract. RealQA `CreatePreset` is replay-safe through a client-generated UUID v7 idempotency key. Before GitHub create/reconciliation, RealQA persists a durable submission attempt that remains server-claimable without another client retry until the exact issue is reconciled and its assets are idempotently promoted or, by the 24-hour staging boundary, converted to removed placeholders with bounded staging/issue-reference/storage-grant cleanup. RealQA permits encrypted offline drafts only after prior login/device binding; logout locks rather than deletes them; reset deletes drafts/tokens/shortcut state/pairing but not Chrome-owned permissions; disconnect retains disconnected presets/mappings; authenticated paginated `ListSubmissions` preserves discovery of only caller-authorized retained submission/asset references for later deletion; owner-authorized `RebindSubmissionStorageAuthorization` serializes a replay-safe replacement-grant attempt through the caller's memory-only forwarded bearer and compare-and-swaps the exact validated submission mapping; `DeleteFeatureData` removes scoped presets/submissions/assets plus GitHub connection/installation/credential records, and asset/account/org deletion plus the 24-hour staging/30-day billing-grace/placeholder rules follow the RealQA server contract. Both feature servers application-level envelope-encrypt retained GitHub user credentials with per-record data keys, versioned managed environment keys, provider-adapter-only decrypt authority, rotation/rewrap, and no plaintext database/backup/observability exposure. Delibase account/organization deletion must transactionally enqueue immutable Deck and RealQA lifecycle calls, authenticate them with the feature-specific M2M identity, and retry them through the retained outbox/dead-letter contract; each receiver pins token `sub` and `client_id` to its configured exact delibase lifecycle client ID and fails startup once lifecycle mode exists if the pin is missing or malformed. DeliDev browser code never invokes those deletion modes.
- RealQA recurring storage billing uses issue #756's implemented synchronized bounded background-usage contract. After upload verification establishes a positive retained-byte maximum, it must persist a per-submission initial authorization attempt with exact inputs and a stable downstream key before `CreateBackgroundUsageAuthorization`; a response loss is replayed by a fresh authenticated `SubmitIssue` request before provider work or cleanup may conclude, and unresolved attempt/tombstone state is retained rather than treated as no grant. Recurring work uses `ReserveAuthorizedUsage`/`CommitAuthorizedUsage`/`ReleaseAuthorizedUsage`; zero maxima/daily units skip those mutations, and no forwarded token is stored or replaced by a live forwarded-token usage RPC. Durably checkpoint every completed UTC day, process the oldest unresolved checkpoint while it remains the immediately preceding reservable day, and finish an accepted reservation later through its stored period. Failure to establish a reservation before the next rollover starts non-billable, never-back-billed grace at the missed period's start and blocks submissions rather than silently skipping the period. A non-deletion transition that closes/replaces an `ACTIVE` grant while retaining images persists one serialized cutoff, settles billable accrual through it, and starts replacement accrual at the same cutoff; already closed grants are not back-billed. Deletion persists its cutoff, tombstones public identifiers, stops accrual, and removes image objects independently of delibase availability, retaining only the minimum mapping/billing retry tombstone until pre-cutoff settlement and exact closure finish. At the unrecovered 30-day grace deadline this deletion cannot wait for billing teardown, and later retries never restore public readability. Its live transfer meter requires at least an 86,400-second reservation TTL; uploads stop within 23 hours, and `SubmitIssue` must start by that deadline with a fresh forwarded bearer so its durable finalization attempt owns bounded verification and commit/release during the final hour. A deadline/cleanup worker never performs live usage without that bearer; an unfinalized reservation expires and its staging is deleted without promotion. Short-TTL mappings are rejected before upload. Its recurring worker uses the outbound RealQA delibase identity described above with OAuth client-credentials tokens from the validated Logto issuer, memory-only token caching, and fail-closed startup before submissions/work claims when configuration or authorized-usage token acquisition fails; this identity is separate from the inbound lifecycle client pin. Rebind may replace an exact old `ACTIVE`, `REVOKED`, or `ACCESS_LOST` authorization but rejects deleted-owner/resource and substituted grants. Owner-request and abandoned-submission cleanup retain each creation attempt, mapping, and retry tombstone until the exact grant is recovered when necessary and an exact-bound, expected-revision idempotent `MarkBackgroundUsageResourceDeleted` call reports a matching closed status; delibase-lifecycle cleanup also accepts already-terminal `OWNER_DELETED` because delibase closes owner grants before dispatch.
- Current production tools, widget registration, and user-visible widget state remain empty until implementation/distribution changes. Issues #755/#757 do not deploy services, configure DNS/R2, register GitHub Apps, publish images/widgets/extensions/stores, inject production identities, enable catalog entries, or roll out operations.
- Run root `pnpm generate:proto`/`pnpm check:proto`, per-package TypeScript typechecks, and generated Go test/vet for both proto roots now. DevHud's Chrome capture bridge implements `test:realqa:native`, `test:realqa:extension`, and `check:realqa:package`; as remaining product and server implementations land, add `test:deck`, `test:deck:widgets`, and `test:realqa` plus Go format/vet/test and sqlc/PostgreSQL/provider/image checks for both servers. Missing planned commands must not be represented by passing placeholders.

### Repository Default Technology Choices

- Follow `docs/repository-defaults.md` when a more specific project or domain contract does not choose a different approach.
- New persisted entities should use UUID v7 identifiers by default unless a documented compatibility, storage, protocol, or product issue requires another ID shape.
- AI-based search should default to Cloudflare AI Search unless a project contract documents a different backend and migration boundary.
- When a new project does not specify its primary language, default to Golang.
- Prefer Rspack-family build tools when possible, including Rsbuild and Rspress for app and documentation surfaces.
- Static sites under `apps/` should use Rsbuild/Rspress-style toolchains and deploy to Cloudflare Pages by default. Existing documented exceptions remain valid until their project contract changes.
- File handling should default to Cloudflare R2 object storage plus signed URLs for upload and download access unless a project contract documents another storage or access pattern.

### TTL Command Contract

- `cmds/ttlc` command identifiers are `build`, `check`, `explain`, and `run`.
- `ttlc run` requires `--task` and accepts optional `--args <json>` with default `{}`.
- `ttlc run` response payload includes `result`, `run_trace`, and root-task `cache_analysis`.

### binpm Cache Contract

- `~/.binpm/cache` is the user-level global asset cache shared by all `binpm` installs for the same account.
- `binpm` CLI source input may normalize GitHub.com shorthands such as `owner/repo` and supported `https://github.com/owner/repo` release URLs, but persisted manifests, lockfiles, package records, cache metadata, logs, and JSON diagnostics must use canonical `github:` or `gitlab:` source strings.
- `binpm` cache reuse must be validated with the strongest available integrity source: provider asset digest, upstream checksum material, successfully verified signature, or locally recorded SHA-256 metadata.
- `binpm` package signature verification is distinct from direct-installer verification for binpm's own release artifacts. Package signatures may satisfy strict verification only when a supported verifier validates the selected asset under the documented package trust policy; raw signature, SBOM, provenance, attestation, certificate, or Sigstore sidecar presence alone is not verification evidence and must be reported separately from trusted evidence when detected.
- `binpm --json` must preserve stable read-only diagnostic contracts and must also support stable final-result envelopes for mutating `install`, `add`, `update`, and `remove` commands. Successful JSON mode must emit exactly one compact object on stdout without ANSI color; progress, human diagnostics, and tracing must stay separate from stdout; errors must keep the parseable stderr envelope with `error.message` and `error.exit_code`.
- Cache management and diagnostic command identifiers are `list`, `prune`, `clean`, and `key` under `binpm cache`.
- `binpm cache prune` and `binpm cache clean` must not remove installed package records or executable links/copies under `~/.binpm/bin`.
- `binpm cache clean` must state the removed cache asset boundary and the preserved `~/.binpm/cache/refs`, package-record, and executable boundaries in human and JSON output.
- `binpm cache prune` must remove stale structured local-project cache references before asset pruning while preserving active and legacy references, and must guide legacy reference migration through future local install, update, or removal flows.
- `binpm cache key` must be read-only and must not download, install, or populate cache entries.
- `binpm cache key` must warn or expose structured status when `binpm.lock` is absent.

### binpm Source Contract

- Stable `binpm` source identifiers are `github:owner/repo[@version]`, `github:<host>/owner/repo[@version]`, and `gitlab:<host>/<namespace...>/<project>[@version]`. GitLab sources always require an explicit host, including `gitlab:gitlab.com/<namespace...>/<project>[@version]` for GitLab.com; `gitlab:group/project` is intentionally invalid.
- binpm provider tokens are host-scoped. GitHub.com may use `BINPM_GITHUB_TOKEN_GITHUB_COM`, `BINPM_GITHUB_TOKEN`, or `GITHUB_TOKEN`; GitHub Enterprise must use `BINPM_GITHUB_TOKEN_<NORMALIZED_HOST>`. GitLab.com may use `BINPM_GITLAB_TOKEN_GITLAB_COM`, `BINPM_GITLAB_TOKEN`, or `GITLAB_TOKEN`; self-managed GitLab must use `BINPM_GITLAB_TOKEN_<NORMALIZED_HOST>`. For explicit hosts, `<NORMALIZED_HOST>` must encode non-ASCII-alphanumeric UTF-8 bytes as `_HH_` uppercase hexadecimal so distinct hosts cannot share a token variable. Generic SaaS tokens must not be sent to enterprise or self-managed hosts.
- binpm release lookup diagnostics must distinguish missing authentication, insufficient permissions, and rate limiting while keeping tokens, authorization headers, private-token headers, query strings, fragments, and credential-bearing URLs out of logs, errors, persisted URLs, cache metadata, package records, and lockfiles. Missing-auth diagnostics for explicit GitHub Enterprise and self-managed GitLab hosts must print the exact expected host-scoped token variable name, and JSON diagnostics must expose safe env-var name fields without token values.
- `binpm` source versions are exact release tag requests only; omitted `@version` selects latest stable, while `@latest`, semver range-like selectors, channel selectors, and major-version pins must be rejected before manifest or lockfile persistence. Diagnostics may suggest an exact leading-`v` tag alternative when the release list shows one, but exact-match semantics must not change.
- GitLab versionless installs must exclude upcoming releases, releases with future `released_at` values, and known SemVer prerelease tag identifiers while preserving non-SemVer stable GitLab tags.
- GitLab release asset links must use HTTPS link URLs and HTTPS final redirect targets before candidate scoring or download.
- GitLab generated `assets.sources` source archives must not be selected as installable assets.
- Source-archive-only release diagnostics must remain distinct from no-asset and target-mismatch failures, list ignored source archive names when safe, and guide maintainers toward prebuilt portable archives or bare executables.
- Linux musl missing-libc diagnostics must name rejected assets and include safe remediation: upstream explicit `musl`/`static`/`portable`/`universal`/`any` naming first, then target overrides only after compatibility verification.
- Direct URLs, registries, and package-manager backends remain out of scope until documented in `docs/crates-binpm-foundation.md`; recognizable package-manager backend prefixes must fail with explicit unsupported-backend diagnostics.

### binpm Local Tooling Contract

- `binpm.toml` is the committed project-local tool declaration file.
- `binpm.lock` is the committed deterministic project-local resolution file and must keep target-specific records.
- `binpm init` manifest creation must target the current Git worktree root when available, otherwise the nearest ancestor containing `binpm.toml` when present, otherwise the current directory. It must print the resolved full manifest destination before creation or overwrite refusal and print a clear created-manifest line after successful creation. `--manifest-path <PATH>` is the documented explicit destination escape hatch for creating a new `binpm.toml`; it must still refuse existing files and must not overwrite manifests.
- `binpm.lock` must not include install timestamps, last-used timestamps, absolute cache paths, or other machine-local operational metadata.
- `binpm.lock` must store sanitized canonical asset URLs only, never query strings, fragments, credential-bearing URLs, or expiring signed download URLs.
- Project-local executable files must be installed under `$repoRoot/.binpm/bin`.
- `binpm install <source> --as <cmd> --bin <upstream-binary>` must preserve explicit global command aliases and selected upstream binaries in global package records without changing source identity. Human source-install output must show `install scope: global` before mutation, show the installed command alias and selected upstream binary as separate fields so repository names, local/global command aliases, and upstream binary names are not conflated, and when a project manifest is detected state that the manifest is not modified with guidance to use `binpm add <cmd> <source>` for project-local tools. Source-form install is global-only; `binpm install <source> --local` must be rejected with guidance to use `binpm add <cmd> <source>`.
- `binpm add <cmd> <source> --bin <upstream-binary>` must persist the upstream binary selection in `binpm.toml`; `binpm add --manifest-only` must only mutate `binpm.toml`; `binpm add ... --also <cmd=upstream-binary>` must expand to separate deterministic `[tools.<cmd>]` declarations; and `binpm x --package <source> --bin <upstream-binary> [cmd]` must use that upstream binary for one-off execution without inferring a source from command names. Manifest-only success output and later `list`, `doctor`, and frozen `x` diagnostics must make declared-but-not-installed state visible and point to `binpm install`.
- Archive binary ambiguity errors must list plausible executable candidates, include concrete retry commands using `--bin`, and mention repeated `--also <cmd=upstream-binary>` values for local multi-binary archives while keeping one `[tools.<cmd>]` manifest table per command.
- Local `binpm remove` must clean project-local package records when they exist.
- Local target-specific asset overrides must use `[tools.<cmd>.targets.<target-key>]` in `binpm.toml`.
- Local `binpm install`, `binpm update`, and `binpm x` must honor `--frozen-lockfile`; `CI=true` enables frozen behavior by default, and `--no-frozen-lockfile` is the explicit escape hatch. Frozen commands must fail when they would need to create or modify `binpm.lock`, except empty-manifest local updates that require no lockfile changes must succeed without creating `binpm.lock` and must report the no-op without file-change plans. Frozen mode is a lockfile write guard, not an offline or cache-only mode. Documented execution aliases `binpm exec` and `binpm run` must share `binpm x` lockfile and command execution behavior while `binpm x` remains canonical.
- Frozen local install and `x` may restore missing `.binpm/bin` executables and `.binpm/packages` package records from existing target lock records when cache bytes match the locked SHA-256. If cache repair needs a download, it may use only the lockfile's persisted sanitized asset URL, must validate the recorded SHA-256 before installing or populating cache, and must not require provider release-list pagination. Same-origin locked GitHub or GitLab provider URLs may use runtime-only host-scoped provider authentication when configured; external locked asset URLs must not receive provider credentials.
- `binpm verify --require-verified` must fail when no provider digest, upstream checksum sidecar, upstream checksum manifest, or successfully verified signature under a documented trust policy is available, and strict failure diagnostics must distinguish missing trusted evidence from unsupported sidecar presence.
- Local and global `binpm update` and scoped `binpm remove` must print selected local/global scope before mutation and support `--dry-run` previews that do not mutate manifests, lockfiles, package records, cache references, or executables. `binpm update` with no command names must visibly state that all tools in the selected scope are targeted. Local update must advance exact-version manifest records to latest stable by updating `binpm.toml`, `binpm.lock`, and installed project-local executables consistently. `binpm update --global [cmd...]` must use existing global package records, preserve command aliases and selected upstream binaries, resolve latest stable releases, and finalize through the same cache/install/verification path as global installs.
- `--no-confirm` is a stable scripting flag for bypassing confirmation prompts on future dangerous operations.
- `binpm env --shell` must keep supported shell values explicit: `bash`, `zsh`, `fish`, `powershell`, and `pwsh` are supported; `pwsh` targets PowerShell 7 setup profiles; and `cmd` is accepted only to return a clear deferred-shell diagnostic with actionable cmd.exe PATH guidance. `--shell` may be omitted for best-effort shell inference, and `--global`/`--local` may narrow output to one PATH command without mutating profiles.
- Global install, add, doctor, and plain env PATH setup messaging must remain guided and non-mutating. `binpm env setup --shell <shell> [--dry-run]` is the explicit opt-in profile modification command and may append only the global bin PATH line after previewing the exact file and line; it must tell PowerShell 7 users to pass `--shell pwsh`, refuse ambiguous shell/profile targets, and not imply project-local `.binpm/bin` entries are suitable for profile persistence.

### binpm Docs App Contract

- `apps/binpm-docs` is the Rspress static documentation app for `binpm` and uses the existing `apps/*` workspace.
- The canonical production URL for `apps/binpm-docs` is `https://binpm.delino.io`.
- `apps/binpm-docs` must use Cloudflare Pages as the default static deployment target unless `docs/project-binpm.md` and `docs/apps-binpm-docs-foundation.md` document a replacement.
- binpm documentation content must be sourced from repository contracts and must not infer product behavior or page content from the live `https://binpm.delino.io` site.
- `apps/binpm-docs` must expose a visible GitHub repository link to `https://github.com/delinoio/oss` in top-level social links and in the document-page footer.
- binpm direct-installer documentation must include latest docs-site installer commands for `https://binpm.delino.io/install.sh` and `https://binpm.delino.io/install.ps1`, preserve current and pinned first-party raw GitHub installer commands, describe checksum verification through `SHA256SUMS`, and keep binpm release verification separate from package verification.
- binpm installation and release documentation must describe Homebrew as prebuilt-only, describe disabled `cargo-binstall` quick-install and compile fallbacks, and distinguish first-party binpm release platforms from broader third-party target parsing support.

### Nodeup Docs App Contract

- `apps/nodeup-docs` is the Rspress static documentation app for `nodeup` and uses the existing `apps/*` workspace.
- The canonical production URL for `apps/nodeup-docs` is `https://nodeup.delino.io`.
- `apps/nodeup-docs` must publish public direct-installer entrypoints at `https://nodeup.delino.io/install.sh` and `https://nodeup.delino.io/install.ps1`.
- `apps/nodeup-docs` must use Cloudflare Pages as the default static deployment target unless `docs/project-nodeup.md` and `docs/apps-nodeup-docs-foundation.md` document a replacement.
- `apps/nodeup-docs` must expose a visible GitHub repository link to `https://github.com/delinoio/oss` in top-level social links and in the document-page footer.

### nodeup Shim and Self Cleanup Contract

- `nodeup shim setup` is the stable idempotent setup/repair command for managed `node`, `npm`, `npx`, `yarn`, and `pnpm` shims.
- `nodeup shim setup` PATH activation remains non-mutating by default; output must provide shell- and OS-aware activation and verification guidance when the shim directory is not active.
- `nodeup self uninstall` removes Nodeup-owned data, cache, and config roots only; binary, managed shims, and shell profile/PATH cleanup remain manual and must be separated from removed data in human and JSON output with shell- and OS-aware follow-up guidance.

### Thenv Component Contract

`thenv` is a two-component project with fixed mapping:

```ts
enum ThenvComponent {
  Cli = "cli",
  Server = "server",
}
```

- `Cli` -> `cmds/thenv`
- `Server` -> `servers/thenv`

### Serde Feather Component Contract

`serde-feather` is a two-component project with fixed mapping:

```ts
enum SerdeFeatherComponent {
  Core = "core",
  Macros = "macros",
}
```

- `Core` -> `crates/serde-feather`
- `Macros` -> `crates/serde-feather-macros`

### Rustia Component Contract

`rustia` is a three-component project with fixed mapping:

```ts
enum RustiaComponent {
  Core = "core",
  Llm = "llm",
  Macros = "macros",
}
```

- `Core` -> `crates/rustia`
- `Llm` -> `crates/rustia-llm`
- `Macros` -> `crates/rustia-macros`

### Documentation-First Policy

- New project creation requires `docs/project-<id>.md` and at least one `docs/<domain>-<project-or-component>-<contract>.md` before runtime implementation.
- Every structural change to project paths must update the corresponding project index and relevant domain contract docs in the same change.
- Repository and domain policy updates must be written in the appropriate `AGENTS.md` in the same change.
- Domain-level `AGENTS.md` files must remain aligned with `docs/` contracts.

### New Project Onboarding Checklist

- Reserve a unique `project-id`.
- Create project path skeleton and add `.gitkeep` if implementation is not started.
- Add `docs/project-<project-id>.md` using `docs/project-template.md`.
- Add at least one domain contract doc using `docs/domain-template.md`.
- Documentation-only phase may mark canonical paths as `planned` before creating path skeletons; create the skeleton and add explicit workspace membership in the same change where Rust runtime implementation begins.
- Update root and domain `AGENTS.md` files when project ownership or contracts change.
- Ensure path and naming contracts are consistent across docs and AGENTS rules.

### Naming Rules

- Use lowercase kebab-case for project IDs and directory names unless runtime conventions require otherwise.
- Use `project-` prefix for project index docs.
- Use domain prefixes (`apps-`, `cmds-`, `servers-`, `crates-`, `protos-`, `packages-`) for domain contract docs.
- Use enum-like canonical identifiers in documents where values must remain stable.

### GitHub Issue Style Contract

- Apply this contract to all open/new GitHub issues.
- Use issue titles in the format `<domain>: <description>`.
- `<domain>` must use stable lowercase identifiers from project/domain contracts (for example: `ttl`, `nodeup`, `serde-feather`, `thenv`).
- `<description>` should be concise, specific, and start with a lowercase verb phrase when possible.
- Do not use bracket-style project prefixes like `[serde-feather]`.
- Use the following Markdown section order for issue bodies:
  - `## Summary`
  - `## Evidence`
  - `## Current Gap`
  - `## Proposed Scope`
  - `## Acceptance Criteria`
  - `## Test Scenarios`
  - `## Out of Scope`
- Optional `## Additional Notes` may be appended only when needed.

### GitHub Pull Request Title Contract

- Apply this contract to newly created pull requests.
- Pull request titles must use Conventional Commit-style format with a required scope: `<type>(<scope>): <description>`.
- `<type>` must be an appropriate Conventional Commit type such as `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `ci`, `build`, `perf`, or `revert`.
- `<scope>` must use a stable lowercase project, component, domain, or tooling identifier from repository contracts when one applies (for example: `ttl`, `nodeup`, `serde-feather`, `thenv`, `docs`, `ci`).
- `<description>` should be concise, specific, and start with a lowercase verb phrase when possible.
- Do not create unscoped pull request titles or use bracket-style project prefixes like `[serde-feather]`.

### Node Runtime Baseline

- Root `.nvmrc` is the canonical Node.js runtime selector for local development workflows.
- The current required runtime is Node.js `24` (LTS major line).
- When bumping the runtime baseline, update `.nvmrc` and relevant CI/runtime docs in the same change set.

### Frontend Design Rules

- Frontend work in `apps/` must follow Toss Design Guidelines for UX/UI decisions across web and mobile surfaces.
- If a form has a single critical input, that input must receive focus when the form is shown.
- Dialog UIs must support closing with the `Esc` key.

### Shell Command Safety Rules

- Use `$(...)` for command substitution; do not use legacy backticks in new scripts.
- Wrap all file paths in quotes by default in shell commands and scripts to prevent whitespace and glob-expansion bugs.
- Apply strict quoting and escaping for all dynamic shell values to prevent command injection and parsing bugs.
- Run GitHub CLI (`gh`) commands outside sandbox restrictions by default; use the required approval flow when escalation is needed.
- If an operation is blocked by sandbox restrictions, retry it without sandbox restrictions using the required approval flow.

### Logging Rules

- Write sufficient logs to support debugging, incident analysis, and operational troubleshooting.
- Prefer structured logging over ad-hoc plain text logs for business and system events.
- Go code should use `log/slog` (or a compatible structured logger built on it).
- Rust code should use `tracing` (or a compatible structured logging facade).
- CLI and operator-facing logs should enable ANSI color by default; allow opt-out with documented flags or environment variables.

### CI Baseline

Repository-wide quality CI is defined in `.github/workflows/CI.yml`.

Coverage expectations:
- `go-quality`: runs `go fmt ./...` (fails if formatting changes are applied) and `go vet ./...` on Ubuntu.
- `go-test`: runs `go test ./...` on `ubuntu-latest`, `macos-latest`, and `windows-latest`.
- `rust-fmt`: runs `cargo fmt --all --check`.
- `rust-clippy`: builds the DevHud frontend, installs its Linux desktop prerequisites, runs `cargo clippy --workspace --all-targets --all-features --exclude devhud -- -D warnings`, then lints the mutually exclusive DevHud runtime foundation with `cargo clippy -p devhud --all-targets --features desktop-cef --locked -- -D warnings`.
- `rust-test`: runs `cargo test --workspace --all-targets`.
- `devhud-realqa-macos`: runs `pnpm --filter devhud test:realqa:native` on `macos-14` to exercise the macOS capture fixtures and compile the adapter for `x86_64-apple-darwin` and `aarch64-apple-darwin`.
- `node-mpapp-test`: runs `pnpm install --frozen-lockfile` and `pnpm --filter mpapp test`.
- `node-mpapp-lint`: runs `pnpm install --frozen-lockfile` and `pnpm --filter mpapp lint`.
- `node-devhud`: runs `pnpm install --frozen-lockfile` and DevHud `typecheck`, `lint`, unit, accessibility, build, diagnostics-contract, and portable mobile-contract commands.
- `devhud-widget-android`: compiles and tests the package-local build-only Android widget foundation plus private native plugins and verifies that the release app does not register the widget.
- `devhud-widget-ios`: compiles and tests the package-local build-only WidgetKit foundation plus private native plugins on macOS and verifies that the release app does not embed the widget.
- `node-binpm-docs-test`: runs `pnpm install --frozen-lockfile` and `pnpm --filter binpm-docs test`.
- `node-nodeup-docs-test`: runs `pnpm install --frozen-lockfile` and `pnpm --filter nodeup-docs test`.
- `node-public-docs-test`: runs `pnpm install --frozen-lockfile` and `pnpm --filter public-docs test`.
- `delibase-server`: runs sqlc reproducibility checks, the delibase Go test suite serially by package against one shared PostgreSQL 17 database while preserving intra-package concurrency coverage, and the non-root Docker image health/readiness validation on delibase server, generated Go API, or shared server infrastructure changes.
- `node-delidev-app`: runs `pnpm --filter @delinoio/delibase-connect build`, the DeliDev `typecheck`, `lint`, `test`, `build`, `test:pwa`, and `test:browser` scripts, rejects deterministic rebuild changes to the checked-in `dist` artifact, and installs the required Playwright browser engines before browser smoke tests.
- `proto-contracts`: runs `pnpm check:proto`, then Go test/vet and TypeScript typechecks for every implemented delibase, Deck, or RealQA proto package. It runs on shared workflow/tooling changes or changes below any of the three proto roots and resolves each compatibility baseline from that contract's own descriptor at the immutable event base.
- `ci-result`: provides a single aggregate status that fails when any executed domain job fails or is cancelled.

Change-scoped execution rules:
- CI jobs perform self-gating (there is no standalone `detect-changes` job).
- Go and Rust jobs use in-job path-based change detection via `dorny/paths-filter`.
- Existing Node workspace test/lint jobs use in-job Turbo affected detection via `pnpm dlx turbo@2.9.14 query affected --packages <workspace>`.
- `node-delidev-app` uses in-job `dorny/paths-filter` gating across its app, delibase client, workspace, app-domain policy, and CI workflow inputs.
- Changes to `.github/workflows/CI.yml` force all `go`, `node`, and `rust` domain jobs to run.
- `workflow_dispatch` runs all domain jobs regardless of changed paths.
- When build or test commands change in project contracts, update this section and `.github/workflows/CI.yml` in the same commit.

Release automation baseline:
- `auto-publish` is defined in `.github/workflows/auto-publish.yml`.
- Trigger contract: runs on `push` to `main` and supports `workflow_dispatch`.
- Branch guard contract: publish job runs only when `github.ref == 'refs/heads/main'`.
- Publish command contract: `cargo run -p cargo-mono -- publish`.
- Workflow permission contract: `permissions.contents: write`.
- Tag push contract: after successful publish command execution, run `git push --tags` without no-tag fallback handling.
- Tag push authentication contract: checkout must disable persisted credentials (`persist-credentials: false`) and clear `http.https://github.com/.extraheader` before pushing tags so `GH_TOKEN` auth is authoritative.
- Required secret contract: `CARGO_REGISTRY_TOKEN` (crate publish) and `GH_TOKEN` (tag push authentication and Homebrew release workflow PR submissions). `GH_TOKEN` must be a dedicated non-`GITHUB_TOKEN` credential so tag pushes emit downstream `push` events for tag-triggered workflows.
- `release-cargo-mono` is defined in `.github/workflows/release-cargo-mono.yml`.
- Trigger contract: runs on tag push `cargo-mono@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS cargo-mono release artifacts to GitHub Releases for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`.
- `release-binpm` is defined in `.github/workflows/release-binpm.yml`.
- Trigger contract: runs on tag push `binpm@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS binpm release artifacts for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, including standalone prebuilt binaries (`binpm-<os>-<arch>[.exe]`) and archive assets (`binpm-<os>-<arch>.tar.gz|zip`), then updates Homebrew (`binpm`) from prebuilt archives for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`.
- `release-nodeup` is defined in `.github/workflows/release-nodeup.yml`.
- Trigger contract: runs on tag push `nodeup@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS nodeup release artifacts for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, including standalone prebuilt binaries (`nodeup-<os>-<arch>[.exe]`) and archive assets (`nodeup-<os>-<arch>.tar.gz|zip`), then updates Homebrew (`nodeup`) from prebuilt archives for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`.
- `release-derun` is defined in `.github/workflows/release-derun.yml`.
- Trigger contract: runs on tag push `derun@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS derun release artifacts and updates Homebrew (`derun`) from GitHub release prebuilt archives (`darwin-amd64`, `darwin-arm64`, `linux-amd64`).
- `release-with-watch` is defined in `.github/workflows/release-with-watch.yml`.
- Trigger contract: runs on tag push `with-watch@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS with-watch release artifacts for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, including standalone prebuilt binaries (`with-watch-<os>-<arch>[.exe]`) and archive assets (`with-watch-<os>-<arch>.tar.gz|zip`), then updates Homebrew (`with-watch`) from GitHub release prebuilt archives (`darwin-amd64`, `darwin-arm64`, `linux-amd64`, `linux-arm64`).

### Documentation Lifecycle Rules

- Every structural repository change must update relevant project index docs and domain contract docs in the same change set.
- New project creation is blocked until its project index doc and at least one domain contract doc exist.
- Documentation-only project onboarding may use `planned` paths, but runtime implementation must not begin before canonical paths are created and documented.
- Repository-wide and domain rules must be maintained in the appropriate `AGENTS.md`.
- Documentation policy updates and documentation changes that introduce or modify repository/domain policy guidance must update the relevant `AGENTS.md` files in the same change, and documentation edits must not silently omit or reinterpret ambiguous requested or source-backed content without user confirmation.
- When user-facing documentation content changes, update relevant pages in `apps/public-docs` in the same change set as needed.
- Run `git commit` only after `git add`; once files are staged, create the commit without unnecessary delay.
- Committing may require workspace binaries (for example, git hooks). If required binaries are missing, run `pnpm install` at the repository root and retry the commit.
- After addressing pull request review comments and pushing updates, resolve the corresponding review threads.
- If a project splits into multiple deployables, the project index must include path ownership and integration boundaries, and component-level domain docs must exist.
