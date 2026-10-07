- Per-key API formats follow the catalog/protocol contracts and main-first PR #1646 allocations. Negotiate ProviderInventory capability 7, retain closed API format/authentication enums and schema-3 API family checks independently of ordered-source Agents, and preserve original request bytes. Portable v4 accepts existing v1–3 semantics; no Worker wire change or client-side format conversion is authorized.

- Failed subscription cleanup follows `docs/cmds-delidev-subscription-contract.md#failed-subscription-cleanup-reservations` after main reservation PR #1614 (System 41). Preserve server-owned complete batches, original actor/login/revision, shared vault/reference deletion checks, atomic result/tombstone receipts, joined shutdown and restore quarantine. Terminal failures require a new explicit batch; status/restart never relaunches login or callbacks. No database migration or Rust/native change.

- Agent Worker wizard saving follows the catalog/protocol contracts: derive one current account source, require at least one account, preserve ordered routing, resolve exact model identity and write model plus Worker in one receipt transaction. Bind source-scoped list cursors before pagination. Preserve canonical metadata/history and legacy accountless APIs; capability 33 is configuration support, never native readiness.
# DeliDev delidev-api-client ownership

- Repository listing/Clone use generated IntegrationQuery/WorkerQuery under the protocol/client contracts, with independent System 31/32 / Worker 18 negotiation. Preserve explicit profile revision/generation/page, transient fresh local proof and identical uncertain request bytes. No PAT read model, client-side Git, persistent draft or frontend registration follow-up is permitted.

- Buf generates service-specific files; `scripts/delidev/proto-compat.mjs` generates historical TypeScript import facades. Keep both package-root exports and legacy `./gen/*` paths working. Regenerate facades through `pnpm proto:generate`, never by hand.

Follow the parent instructions and the owning contracts in `docs/`. These rules retain the original requirements; cross-domain changes must also read the affected owners' instructions.

- Generated ActivityQuery retains the typed PR metadata and PR_HANDLING_V1 capability under the activity contract. Keep exact source revisions and version references; source inspection is an explicit disposable read, and attempt success cannot become verified handling.

- DeliDev generated user-service queries retain exact bigint revisions and original requests; capability support is separate from native manager availability and cleanup. Keep private native identities and credentials off the service wire, and follow `docs/cmds-delidev-user-services-contract.md`.

- Backup job observation uses owner/client `GetBackupCreation` and `GetBackupDeletion` independently of bounded history pages. Keep accepted IDs and exact revisions through navigation, refresh inventory after observed completion, and never replay a mutation to poll status. Follow `docs/cmds-delidev-storage-contract.md`.

- Managed backup deletion uses existing durable jobs/receipts and schema-24 indexing, plus immutable synchronized `backup-deletions/` intents outside SQLite before unlink. Preserve exact inspected revision/metadata/hash, original actor/request identity, startup obligation reconstruction, creation-replay suppression, bounded pending retries and source preservation on mismatch. Never evict deletion obligations or equate logical image bytes with reclaimed disk space; follow `docs/cmds-delidev-storage-contract.md`.

- DeliDev portable configuration uses generated ConfigurationQuery export/preview/apply bindings. Keep original document bytes and exact request identities under `docs/cmds-delidev-configuration-transfer-contract.md`; client parsing never becomes authorization, validation or numeric reserialization authority.

- DeliDev workspace file queries use generated `SessionQuery.readSessionWorkspace`; no client filesystem or duplicated authorization logic. Keep file contents in bounded nonpersistent view caches, preserve exact decimal size strings, and render them as inert text under `docs/cmds-delidev-files-contract.md`.

- `packages/delidev-api-client` owns the private generated TypeScript/Connect Query bindings and bounded read-only synchronization helpers. Follow `docs/packages-delidev-api-client-contract.md`. Generate all descriptors from `delidev.v1`; never duplicate Go product validation or add implicit startup/mutation retry.

- Generated DeliDev UsageQuery exposes the additive explicit granularity/timezone fields and optional same-snapshot daily/model analytics. Preserve exact decimal counter strings and server-supplied Other groups; the client must not recreate aggregates or rank results locally. Follow `docs/packages-delidev-api-client-contract.md`.

- DeliDev `SessionQuery.readSessionReviewContext` carries authoritative Worker diff coordinates with original side/range/text/newline facts. Clients cannot manufacture line anchors for binary, mode-only, symbolic-link or submodule changes. Keep observations bounded and nonpersistent under `docs/cmds-delidev-files-contract.md`.

- DeliDev generated SessionQuery local review mutations preserve exact decimal revisions, original anchor/context and immutable submitted snapshots. Retain exact uncertain wire requests across panel navigation; current Resource refresh cannot silently update a selected revision. Use the dedicated owner/client operations under `docs/cmds-delidev-files-contract.md`.

- DeliDev generated IntegrationQuery follows `docs/cmds-delidev-integrations-contract.md`; write-only PATs never enter query keys/read models and pending revision strings remain exact. Generate all service descriptors from the canonical proto.

- DeliDev IntegrationQuery includes the generated repository access read. Consumers preserve its exact scope and decimal revision and independently validate observations; endpoint availability is not future authorization or semantic CI/rules evidence.

- DeliDev IntegrationQuery includes generated repository content queries. Keep strict query/scope validation, exact decimal IDs with original API identity source, explicit search/page limits and nullable mergeability; no query cache or content response may carry saved PATs.

- DeliDev retained PR feedback uses generated IntegrationQuery refresh/list/dismissal bindings. Preserve exact remote numeric strings, resource revisions and original content-version/request identities. No implicit collection, mutation retry, handling inference or browser persistence is allowed; follow the integration contract.

- DeliDev IntegrationQuery exposes generated retained remediation-history and explicit allowance-resumption bindings. Preserve exact numeric PR IDs and original set/revision/request identity without persistence, implicit mutation retry or execution inference; follow the integration/client contracts.

- DeliDev provider and model settings follow `docs/cmds-delidev-provider-activation-contract.md`: capability-gate against ProviderInventory, derive exact counts from the server, paginate custom providers, filter model reads/selections server-side to enabled API providers, and preserve explicit Off references. Do not add a client-side activation/eligibility source.

- DeliDev session forwards follow `docs/cmds-delidev-forwarding-contract.md`: preserve explicit loopback port selection, original client/Worker/device/instance ownership, negotiated capabilities and bounded ordered opaque traffic. Native claims precede sockets and receipt replay grants no new lifetime. Keep Stop independent from Archive, gate every Archive completion on both original cleanup outcomes, and retain positive private cleanup receipts through offline reporting without redialing or recreating listeners. Worker credentials receive only their original forwarding peer endpoints.

- Preserve legacy generated-path reflection exports as well as declaration imports. Generate the aggregate descriptor view in the compatibility pass, retain original declaration order and canonical TypeScript object identity, and cover both direct enumeration and registry construction in compatibility tests.

- Generated SessionQuery permanent-deletion acceptance/status and Worker cleanup queries preserve original UUIDs, BigInt revisions, pending removal and unknown reclaimed bytes. Generate through the canonical split schema and compatibility pass; no client-side ownership decisions or automatic mutation replay. Follow `docs/cmds-delidev-storage-contract.md`.
- WorkspaceStorageQuery exposes generated owner/client storage request, inspection and cancellation operations. Preserve exact session/job revisions, original receipts and separate observed native outcomes without implicit mutation retry or cleanup inference.

- TerminalQuery and WorkerQuery expose the generated session terminal operations. Preserve exact bigint cursors/revisions, original bytes, explicit output gaps and request identities without persistence or native side-effect retries; follow the terminal contract.

- Export generated PullRequestFixQuery and its typed profile from the additive PR-fix service. Preserve original request bytes/revisions and facade declaration identity; native Git authority remains Worker-local.

- Native subagent observations follow `docs/cmds-delidev-subagents-contract.md`. Validate original bounded ownership and complete batches before atomic publication; preserve exact receipts, source coverage, requested versus observed models and nullable non-additive usage. Live/unavailable children retain independent cleanup obligations after parent completion. Observation never grants child control or unproved continuation.
- DeliDev API clients export the generated SubscriptionService and closed managed Codex capability/action types. Keep authentication bundles out of Query keys, persistence, synchronization, errors and ordinary resource models. Public clients cannot invoke the protected Worker lane. Follow `docs/cmds-delidev-subscription-contract.md` and retain generation freshness checks.
- DeliDev API clients export the generated SubscriptionService and closed managed Codex capability/action types. Keep authentication bundles out of Query keys, persistence, synchronization, errors and ordinary resource models. Public clients cannot invoke the protected Worker lane. Follow `docs/cmds-delidev-subscription-contract.md` and retain generation freshness checks.

- Generated NetworkQuery follows `docs/cmds-delidev-network-contract.md`, retaining exact revisions and write-only credential input outside query caches. Preserve both service-specific exports and legacy generated paths/reflection; signed Worker metadata never proves native installation or encrypted credential transfer.

- Codex Fork uses owner/client-only `SessionService.ForkSession` and `GetSessionFork`, typed `ForkWorkspace`, and allocation-ledger capability `CODEX_SESSION_FORK_V1 = 13`. Local proof is write-only; exact job/child observation cannot replay native creation. Preserve split service ownership and generated compatibility exports under `docs/cmds-delidev-forks-contract.md`.

- Managed database restore follows `docs/cmds-delidev-storage-contract.md`: exact inspected image/live revision and actor-bound external receipts, exclusive settled ownership, immutable current safety/deletion authority, paused/quarantined historical work, and pre-open journal recovery. Preserve typed publication-versus-startup outcomes; uncertain retries never republish or revive native claims.

- Generated `SessionQuery.switchSessionAccount` preserves the exact session revision, UUID-v7 receipt and explicitly selected account. Gate availability with the typed System capability, retain uncertain requests without automatic mutation retries, and leave stopped-session compatibility/authorization in Go.

- Native context and manual compaction use the generated SessionService declarations and closed SessionContextCapability values in `docs/cmds-delidev-claude-compaction-contract.md`. Preserve raw bounded observation JSON, exact uint64 mutation revisions and UUID-v7 retry identity; never infer utilization or action success from an outer envelope.

- Generated UsageQuery consumers verify the NATIVE_UNITS_V1 echo before interpreting native accounting, preserve decimal totals and distinct unit kinds, and keep legacy response fields separate. Grok pricing and budget contribution remain unavailable.

- Generated BrowserQuery bindings expose only the metadata/cleanup contract in `docs/cmds-delidev-browser-contract.md`. Keep browser URLs, tabs and native paths out of product RPCs and React Query server state; local native presentation is independently authorized and generation-bound.

- Generated NativeModelQuery bindings retain exact bigint revisions, original mutation identities and immutable observation cursors under `docs/cmds-delidev-native-models-contract.md`. Keep canonical registration a separate explicit save and advisory metadata outside any client authorization/readiness engine.

- Generated SessionQuery request diagnostics keeps optional metadata, original identity and exact bigint revision/latency under the diagnostics contract. Never infer requests from time proximity or resend a request from a diagnostic receipt.

- `configuration-identity.ts` owns bounded schema-family read negotiation and closed JSON/wire service identity mappings. Preserve API-only v1, recognize only owning v2 families and keep retired original documents inert. It grants no mutation, native support or credential authority; synchronization preserves exact revisions and full snapshot/event atomicity under the client contract.

- Worker network bootstrap/status uses canonical generated NetworkQuery/WorkerQuery declarations. Preserve exact uint64 generations and original request identity. Ciphertext export is a mutation result, never persistent query state; credentials/private keys/decrypted derivatives cannot enter query keys or read projections. Public desired/effective/native states remain independent and grant no execution or observed route use.

- OpenCode Fork independently negotiates System 26 / Worker 15 through the existing SessionQuery fork operations. Preserve exact native turn identities and inherited provenance; preparation grants no child control, effective settings or additive usage. Follow `docs/cmds-delidev-forks-contract.md`.

- OAuth generated bindings retain independent capability 5, field 9 and the closed enums/RPCs under the OAuth contract. Keep code out of cached query/mutation variables and preserve exact original completion identity for code-free local recovery; never add automatic exchange retry.

- Provider presentation identity/order comes from the closed 35-ID mapping in provider-presets.ts; keep the original six, added 26 and local three order consistent with server inventory. This mapping never replaces authenticated availability, account counts or capability negotiation.

- Export independent server-login capability 30, typed progress state/name/generation and write-only original callback messages. URLs, callback bytes and unsaved suggested names stay outside shared caches/persistence/logs. Preserve API, Worker protected-lane and quota authorization; generate outputs from reconciled schemas.

- Subscription diagnostic bindings are generated from main-established protocol allocations. Preserve missing diagnostics separately from an empty detected version; metadata never grants callback/login replay or automatic retry authority.

- Agent Worker ordered source routes follow the catalog, desktop, protocol and sessions contracts. Main reservation PR #1371 owns System capability 36 and SaveAgentWorkerRequest.route_models field 5; preserve capability 35. Keep schema-3 routes exclusive with legacy fields, all models/accounts referenced and saved atomically, per-source routing state updated only with a successful first claim, confirmed-exhaustion-only fallback and observed-recovery preference for later new sessions. Preserve immutable executions, old-client write protection and portable v1/v2/v3 compatibility; add no SQLite migration.

- Remote repository clients negotiate generated System 37 before saves/imports and preserve Worker 19 independent managed-clone gates. Retain original request bytes and source-kind/URL identity, optional checkout semantics and legacy immutable records under the protocol/workspace contracts.

- Native Claude generated bindings expose System 38 / Worker 20 and closed method/diagnostic/profile metadata. Never place original login URLs or approval-code bytes in query caches, persistent mutation state or logs. Submit is initiating-owner/client-only; protected Take is original-Worker-only. Capability metadata grants no native login or cross-device execution.
- Inline Worker models and endpoint-only completion follow the main-first reservation amendment in `docs/cmds-delidev-catalog-contract.md`. Reserve System 42 / Worker 22 and the complete endpoint/identity/pricing declarations before activation. Compose complete DB 32 / protocol 2 reset with Worker schema 4 and portable bundle 4, removing independent Models/persistent API catalogs while preserving exact inline settings, pricing history and native/account authority. Earlier DBs and backups are unsupported. The owner waives earlier DB retention, permitting an explicit reset of its DB/sidecars without conversion; protected credentials and native ownership retain their original cleanup authority. Reservation-only changes grant no support.
