# React Forge Figma Contract

## Scope and ownership
Extend the private Node 24/macOS arm64 React Forge runtime with new Figma Design creation and preservation-aware editing of external files. The React package owns components, sessions, Keychain adapters and official MCP transport. `forge-figma` owns pure model validation, diff and bounded batches; `react-forge-node` adapts that engine to Node workers.

## Interfaces
`Format.Figma`, `createSession`, `openFigma` and `/figma` support pages, frames/auto layout, text, shapes/vectors, images, components, variants/instances, variables and styles. React rendering is local. Explicit `publish()` pins settled React work and applies a revision. Inspection and mounts use document-scoped handles. Only selected properties and managed children belong to an edit; unrelated and unsupported content remains untouched. Unsafe edits and external conflicts fail closed.

The TSX CLI writes an explicit `.figma.json` publication receipt, not a `.fig` document. It contains non-secret identities, bindings and status, never credentials or design contents. Existing output requires overwrite. Sessions remain in memory: no implicit recovery database, daemon or autosave. Figma native IDs remain distinct from Forge UUID-v7 identities. Workflow state must not enter plugin data.

## Authentication and network boundary
Only the official `https://mcp.figma.com/mcp` receives OAuth credentials. macOS adapters read matching Codex or Claude Code Figma MCP Keychain entries, never general login/API credentials. Explicit provider selection is supported; automatic selection is pinned and never changes on permission failure. Tokens stay in memory, never argv, diagnostics, receipts or fixtures. On expiry/401, reread the same provider once; otherwise require reauthentication in that provider. Never refresh or rewrite its tokens.

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
