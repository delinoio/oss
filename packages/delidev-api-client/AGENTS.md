# DeliDev delidev-api-client ownership

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
- TerminalQuery and WorkerQuery expose the generated session terminal operations. Preserve exact bigint cursors/revisions, original bytes, explicit output gaps and request identities without persistence or native side-effect retries; follow the terminal contract.

- Codex Fork uses owner/client-only `SessionService.ForkSession` and `GetSessionFork`, typed `ForkWorkspace`, and allocation-ledger capability `CODEX_SESSION_FORK_V1 = 13`. Local proof is write-only; exact job/child observation cannot replay native creation. Preserve split service ownership and generated compatibility exports under `docs/cmds-delidev-forks-contract.md`.

- Managed database restore follows `docs/cmds-delidev-storage-contract.md`: exact inspected image/live revision and actor-bound external receipts, exclusive settled ownership, immutable current safety/deletion authority, paused/quarantined historical work, and pre-open journal recovery. Preserve typed publication-versus-startup outcomes; uncertain retries never republish or revive native claims.

- Generated `SessionQuery.switchSessionAccount` preserves the exact session revision, UUID-v7 receipt and explicitly selected account. Gate availability with the typed System capability, retain uncertain requests without automatic mutation retries, and leave stopped-session compatibility/authorization in Go.

- Generated UsageQuery consumers verify the NATIVE_UNITS_V1 echo before interpreting native accounting, preserve decimal totals and distinct unit kinds, and keep legacy response fields separate. Grok pricing and budget contribution remain unavailable.
