# DeliDev native subagent observations

## Scope
Issue #1094 exposes native child ownership, lifecycle, available output, model and usage through the existing authenticated session resource boundary. Go owns adapters and publication in `cmds/delidev-cli`; `protos/delidev/v1` and `packages/delidev-api-client` own additive typed identifiers; `apps/delidev` renders read-only session observations. Issue #964 remains the complete product contract.

## Runtime and Language
Go server/Worker/CLI, generated Protocol Buffers/Connect bindings and the React desktop client. This profile supports Codex `0.151.0` API and Claude `2.1.236` API under their existing immutable account/model/relay assignments. Other harnesses and native versions have no child publication profile.

## Users and Operators
Authorized owner and paired clients inspect retained session observations. Only the originally assigned authenticated Worker publishes them. Native task tools may initiate children as part of the original harness execution; product clients receive no create, message, interrupt, restart or child-resume operation.

## Interfaces and Contracts
`SystemService.GetStatus` advertises `SUBAGENT_OBSERVATION_V1`; `EntityKind.SUBAGENT` is additive. Their wire values use main’s established allocation reservations (entity kind 30 and capability 12), leaving existing assignments unchanged. `ResourceService.GetResource`, session-filtered `ListResources`, snapshots and resource events retain their existing authorization and pagination. `delidev session subagents --id <session-uuid> [--limit <1..100>] [--page-token <opaque-token>]` uses the same authenticated resource read. The desktop Subagents disclosure requires the typed capability, refreshes on session/resource revisions and provides bounded first/next pages. Refresh and pagination issue reads only.

The original Worker publishes `subagent-observed` through `WorkerService.PublishExecution`, with the same UUID-v7 receipt, exact monotonic sequence and immutable execution/native root/turn ownership. Each batch contains at most 128 observations and 512 KiB encoded data. Validate the complete batch before resource, session or event publication. Child records use separate UUID-v7 product IDs and retain exact native IDs, root, parent, optional original parent tool, harness/version, execution, first/last publication sequence and original source identifiers. The execution tree admits at most 1,024 retained nodes, 128 live nodes and 512 KiB of metadata. Reject foreign parents, cycles, duplicated batch identities, reused product/native ownership, reparenting, changed original tool references and terminal regression atomically. Same-session native ownership cannot be reused by another execution. Exact receipt replay returns the original result and creates no duplicate resource or event.

Codex consumes canonical `collabAgentToolCall` and `subAgentActivity` items, including the latter’s string-valued activity kind. Spawn ownership comes from the original sender/receiver relationship; activity paths cannot invent a parent. Read-only `thread/list` with `ancestorThreadId`, `useStateDbOnly=true` (no rollout metadata scan/repair) and child source filters validates exact native session-tree, parent, provider and version metadata. Bounded `thread/turns/list` reads the latest child turn with full items. Descendant inspection never calls child `thread/resume`, input or control operations. History reads have a 30-second cancellable deadline and are limited to 16 list pages and a 128-node complete observation batch; overflow returns failure without partial publication. Known child notifications never become root transcript/usage. Requested spawn models remain distinct from observed settings models; thread metadata or parent configuration cannot supply an absent observed model.

Claude composes original `local_agent` tasks with acknowledged root Agent/Task proposals, or a nested Agent/Task tool retained from that parent's verified content. Task depth, agent type, description and explicit background/transcript/ambient flags remain native observations. Original session/input/turn and parent-tool identity bind child content independently of root-result arrival. Ordered text/thinking blocks retain native kinds and message identity; opaque bodies/signatures/media remain unavailable. Task summaries and output locators cannot supply verified child transcript or authorize file reads. Stored-history projection follows the existing private derived-path, complete transcript, original forwarded-message proofs and exact task/tool/agent sidecar verification. It remains a stored observation, never a synthetic stream or continuation checkpoint. Missing, unavailable or unsupported history preserves the already retained observations.

Statuses are pending, running, paused, completed, failed, interrupted, shutdown or unavailable. Unavailable cannot establish terminal cleanup. Native shutdown may refine a terminal status; it cannot reopen, and late Codex turn completion/history retains the explicit shutdown lifecycle. Root completion cannot finish children; a terminal child cannot finish its running descendants. Exact owned child observations may arrive after root completion, before independent cleanup confirmation. Live children keep the process/cleanup obligation open. Closed children still require the original process and workspace cleanup report. Sessions with observed children use version-1 paused completion and cannot acquire version-2 continuation or history-recovery authority from this profile.

Output is an explicitly partial recent native text subset, not a reconstructed complete transcript. Output, requested model, observed model and usage are nullable; missing remains unavailable, including an unobserved model even when a requested model is known. A record retains its last available output/model/usage when a later status report omits it. Original source coverage is retained without truncation, capped at 4,096 reports per node and the existing resource document bound.

Usage records retain exact nullable decimal-string counters and closed `child-cumulative` or `child-response` scope. Codex native cumulative/last/context metadata, Claude task counters and Claude provider-response metadata remain exact validated JSON strings, preserving large integer spelling, zeros, nulls, cache/thinking/tool details and native report families. Each source coverage entry retains only the usage actually supplied by that report. The Worker runner routes forwarded child provider usage only through the child-content adapter; its root-only usage adapter ignores those already-published reports. These counters never enter response billing rows, cost estimates, budget arithmetic or parent-inclusive totals. Reports with overlapping scopes are not added; unavailable counts are not zero and a total is never fabricated from partial counters.

## Storage
Existing generic session-owned resource storage retains child observations; there is no destructive migration, backfill or new secret table. Execution progress retains bounded ownership metadata only. Durable request receipts retain the exact original event payload. Resource updates, revision advancement and event/session publication occur in one transaction, including rejection after root completion. Output and observations remain protected retained conversation data with existing session/server retention; no cloud storage or telemetry export is introduced.

## Security
Revalidate original Worker/process/job and client authorization under the existing publication/read contracts. Ownership derives from native identity evidence, never paths, prose, requested models, task summaries or model-proposed identifiers. No account/provider fallback, child-control capability or reconnect replay of native side effects is granted. Claude history uses only the existing verified owned transcript reader. HTML/text/native JSON is rendered inertly; unsupported opaque content stays private. Tests use isolated temporary state and controlled native/provider fixtures without user credentials.

## Logging
Use existing structured redacted publication and owned-process diagnostics with job/execution identity, closed event kind and stable failure class. Retain missing history as unavailable and its existing bounded history-read diagnostics. Never log child output, task descriptions, model-proposed paths, transcript bodies, native reports, secrets or provider diagnostic text.

## Build and Test
Run `go test -race ./cmds/delidev-cli/...`, `go vet ./cmds/delidev-cli/...`, `pnpm proto:check` and `pnpm test` in `apps/delidev`. Focused fixtures cover nested/two-child ownership, atomic foreign/cycle/reuse rejection, exact receipt replay, late completion, unavailable versus requested/observed models, exact overlapping usage, ordered Claude content, read-only Codex history operations and live-child cleanup. Hydrate required LFS assets, explicitly generate required package/embed `dist` and remove generated `dist` before finishing. Fixture verification is distinct from unperformed real-account, native platform and release acceptance.

## Dependencies and Integrations
The existing session, execution outbox, native adapter, process, usage and generated resource-client contracts remain authoritative. Codex canonical schema reference is pinned to commit `d8673cb68e349c208659b986697773d3145dbb14`; Claude uses the retained `2.1.236` task/content/history profile. No new runtime dependency is added.

## Change Triggers
Update this contract, project/catalog index, harness/session/process/usage/protocol/client/desktop contracts, independent issue evidence and relevant `AGENTS.md` when versions, bounds, native source coverage, ownership, storage or capabilities change. Child-control or continuation support requires independent original evidence and an explicit contract extension.

## References
- [Project index](project-delidev.md)
- [Requirements](cmds-delidev-requirements.md)
- [Native adapters](cmds-delidev-harness-contract.md)
- [Sessions](cmds-delidev-sessions-contract.md)
- [Owned processes](cmds-delidev-process-contract.md)
- [Usage](cmds-delidev-usage-contract.md)
- [Connect protocol](protos-delidev-v1-contract.md)
- [Client](packages-delidev-api-client-contract.md)
- [Desktop](apps-delidev-desktop-contract.md)
- [Repository defaults](repository-defaults.md)
- [Codex canonical child items](https://github.com/openai/codex/blob/d8673cb68e349c208659b986697773d3145dbb14/codex-rs/app-server-protocol/src/protocol/v2/item.rs)
- [Codex native thread ownership](https://github.com/openai/codex/blob/d8673cb68e349c208659b986697773d3145dbb14/codex-rs/app-server-protocol/src/protocol/v2/thread_data.rs)
