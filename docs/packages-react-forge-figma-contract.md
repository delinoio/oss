# React Forge Figma Contract

## Scope and ownership
Extend private React Forge with new Figma Design creation and preservation-aware editing of external files. Authenticated Figma acceptance targets Node.js 24 on macOS arm64 with macOS Keychain; the local Office/PDF formats retain their six-host support. The React package owns components, sessions, Keychain adapters and official MCP transport. `forge-figma` owns pure model validation, diff and bounded batches; `react-forge-node` adapts that engine to Node workers.

## Interfaces
`Format.Figma`, `createSession`, `openFigma` and `/figma` support pages, frames/auto layout, text, shapes/vectors, images, components, variants/instances, variables and styles. React rendering is local. Explicit `publish()` pins settled React work and applies a revision. Inspection and mounts use document-scoped handles. Only selected properties and managed children belong to an edit; unrelated and unsupported content remains untouched. Unsafe edits and external conflicts fail closed.

The TSX CLI writes an explicit `.figma.json` publication receipt, not a `.fig` document. It contains non-secret identities, bindings and status, never credentials or design contents. Existing output requires overwrite. Sessions remain in memory: no implicit recovery database, daemon or autosave. Figma native IDs remain distinct from Forge UUID-v7 identities. Workflow state must not enter plugin data.

## Authentication and network boundary
Only the official `https://mcp.figma.com/mcp` receives OAuth credentials. macOS adapters read matching Codex or Claude Code Figma MCP Keychain entries, never general login/API credentials. Explicit provider selection is supported; automatic selection is pinned and never changes on permission failure. Tokens stay in memory, never argv, diagnostics, receipts or fixtures. Expiry and HTTP 401 share one credential reread allowance for the entire session, including reconnect attempts. A still-rejected credential permanently stops that session; require reauthentication in the selected provider and a new session. Never refresh or rewrite its tokens.

The official TypeScript MCP SDK owns Streamable HTTP. Check advertised tools and code limits before writes. Explicit local image bytes may use official asset upload tools; arbitrary remote downloads remain excluded. This is a Figma-specific exception to local-only networking/storage. Other formats retain their offline contracts.

## Scheduling and failure semantics
Diff before writes, deduplicate reads/assets, and batch by page, dependencies and serialized code size (currently 50,000 characters). Share rate admission across sessions with the same authentication and serialize file writes. Honor Retry-After and bounded jittered exponential retries for safe transient failures. Exhausted daily/monthly quotas do not cause retry loops.

Remote publication is not atomic file replacement. Report completed, partial and unknown outcomes truthfully. Reconcile uncertain writes before retrying proven pending changes. Never blindly repeat file or ambiguous node creation. Cancellation stops subsequent batches and does not undo confirmed writes. No automatic whole-task timeout. Diagnostics contain stable codes, stages/revisions, counts and timing, never contents or secrets.

## Validation
Offline CI uses synthetic credentials and fake MCP. Cover model/diff/batching, React updates, external preservation, no-op publication, stale targets, rate admission/retries, uncertain writes, cancellation/disposal, expiry/redaction and installed CLI.

Live evidence creates five editable ROAM mobile screens (Explore, Search, Destination, Itinerary, Saved) in the selected Pro team's Drafts, closes/reopens the session, changes text/images/layout and proves preservation and duplicate-free republication. Also edit an external fixture without Forge metadata. Record URLs, structural checks, screenshots and call counts without credentials or generated dist.

## References
- [Project](project-react-forge.md)
- [Official tools](https://developers.figma.com/docs/figma-mcp-server/tools-and-prompts/)
- [Official limits](https://developers.figma.com/docs/figma-mcp-server/rate-limits-access/)

## Implemented model and ownership

Scene elements require an explicit page/parent. References use authored `nodeKey` aliases or explicitly selected `@remote-ID` values. The native planner uses enum node/action types, validates kind-specific properties, orders parent/resource/component dependencies, and bounds each batch to 24 operations in addition to the negotiated code budget. Page updates execute in that page's context. Local image bytes use the shared native PNG/JPEG decoder with the Figma upload ceiling of 10 MiB and the existing 64-million-pixel limit.

The public session shares the real reconciler and its settling semantics. Figma adds `refresh` for page/resource/selected-node reads and `publish` for remote application; it has no `exportBuffer` or local font-registration API. Inspection returns stable native IDs, logical handles, parent/page identities and a bounded display name. Selected reads include ancestors, so overlap checks work without scanning a whole page. Resource updates use explicit `target` declarations. Mounted elements retain their kind and parent. Instance component identity, component-set membership, and variable type/collection changes are rejected after initial ownership; mutating those structures cannot silently discard foreign overrides or contents. Pages, resources and components/sets are not automatically deleted by omission; external instance preservation cannot be proved for component removal.

Declared children are an ownership list, not a replacement for all children. Existing native children require explicit target IDs. Omitted fields preserve current values, including fields that were declared by an earlier render; omission is not a reset instruction. Container removal fails if foreign descendants remain. Unmount keeps the last confirmed state and stops React ownership. A new process should open the receipt/file, inspect and explicitly select targets; replaying an unrelated root tree is not automatic recovery.

Each publish pins the model and revision, verifies all known selected baselines before the first write, and repeats guards at batch execution. Post-write snapshots include indirectly changed collection membership, variant components and their former parents, so subsequent batches do not conflict with the session's own changes. SHA-256 guards cover supported node fields, parent identity and the full native child-ID sequence. Derived Auto Layout/text geometry is excluded from the guard when its source properties determine it; measured geometry is returned separately. The sandbox implements SHA-256 over UTF-8 because Node crypto is unavailable there. Snapshots return compact digests instead of design text: live official MCP text responses truncate around 20 KiB, independently of the advertised 50,000-character code limit. Page/resource/selected reads are paginated in groups of at most 32. Native batch caps, compact snapshots and dynamic code splitting bound normal mutation responses as well.

A no-change publication within a session makes no tool calls. Known applied writes whose responses were lost can be confirmed from actual declared properties; only proven pending operations may be retried, once, against the reread baseline. Mixed or unprovable outcomes remain unknown. New IDs are never inferred from names. Partial property-set failures return all known created IDs and the completed-operation count; the next plan updates those IDs instead of recreating them. This optimistic protocol is not an atomic compare-and-swap against other Figma clients and provides no remote rollback.

## Credentials, budgets and receipts

The macOS adapter invokes `/usr/bin/security find-generic-password` with a non-secret service/account selector and captures stdout privately. Codex records are under `Codex MCP Credentials`, keyed by the configured server name plus the truncated SHA-256 of the official HTTP configuration. Claude Code's shared credential record is filtered to the exact matching `mcpOAuth` server/URL; unrelated login entries are never used. Optional Claude config-directory suffixes are supported. Unsupported formats fail with a fixed authentication diagnostic. Host storage formats are compatibility adapters, not APIs owned by React Forge; changes require fixtures and revalidation.

`CredentialSource.Auto` pins its first usable provider, including when its token has expired. There is no permission-error fallback to another account. The SDK has no OAuth refresh provider, attaches bearer credentials only to the exact official MCP endpoint, refuses redirects and rereads the selected store once on 401. Raw exec, SDK and HTTP failures never become public errors. Scoped asset URLs accept HTTPS Figma authorities and receive only image bytes and content type, without the bearer token.

Admission budgets and file queues are process-local and shared across sessions. A one-way in-memory authentication digest keys the budget; it is not logged or persisted. Explicit selected plan data configures the documented starter/view/collab monthly or paid full/dev daily policy. Existing files without a selected plan use conservative 10/min admission and authoritative server quota responses. Explicitly documented exempt tools are `whoami`, `create_new_file` and `add_code_connect_map`. The current paid per-minute policies are Pro/Education 10, Organization 15 and Enterprise 20. Local counters do not claim visibility into other clients' consumption. Retry-After controls admission; long exhaustion or daily/monthly error classifications stop retries. Safe transient failures have up to three jittered retries; ambiguous writes do not.

Receipts include file URL/key, local revision, outcome, UUID/native bindings, kinds, state fingerprints, image-content-hash mappings, confirmed changed IDs and tool/retry/wait totals. Image mappings allow reuse when reopening a receipt. Receipts exclude text, asset bytes, tokens, refresh tokens and credential paths. An output-path conflict is checked before remote publication; a later local save failure reports the already-published receipt. Diagnostics expose only stable codes, revisions, batch/change/call counts and retry/wait durations. SDK initialization/listing requests are separate from tool-call counts.

## Validation artifacts

The ROAM generation/edit examples and compact macOS arm64 evidence are maintained in `packages/react-forge/examples/travel-figma*.tsx` and `packages/react-forge/tests/evidence/travel-figma-macos-arm64.json`. Live acceptance details and its limits are in [validation](packages-react-forge-validation.md). CI remains offline with respect to Figma and Keychain, using the actual official SDK against a synthetic HTTP MCP peer plus an executable fake canvas.
