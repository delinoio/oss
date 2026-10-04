# DeliDev domain ownership

Follow the parent instructions and the owning contracts in `docs/`. These rules retain the original requirements; cross-domain changes must also read the affected owners' instructions.

- PR activity uses the closed metadata-only shapes in `pr_activity.go` under the activity contract. Keep original problem versions, actor provenance and attempt outcomes distinct from dedicated handling verification; never infer verification from success, dismissal or provider state.

- Service status observations hold the state gate against controller admission, probe the runtime lock before reading completion, and retain an acquired runtime lock through that observation. Preserve incomplete controller evidence as uncertain; an earlier journal snapshot cannot establish cleanup after lock release.

- Windows service process ownership resolves the reported Win32 image path under the installation's canonical path contract, including 8.3 aliases, and matches the original executable file identity. Preserve independent SID and before/after process-birth checks.

- Return safe deletion-attempt errors with retained job state even on unchanged retries, so maintenance logs persistent failures using only job ID and typed code. Preserve stable revisions for identical pending outcomes.

- Deletion recovery caps canonical obligations at 4,096 separately from the 8,192-entry directory bound. Pending atomic-write remnants consume only directory capacity and remain preserved; validate every retained obligation before reconstruction.

- Recheck WAL/SHM/journal absence before publishing an inspection, including when opened main-file identity and bytes remain unchanged throughout the private copy.

- Follow `docs/project-delidev.md`, `docs/cmds-delidev-contract.md`, `docs/protos-delidev-v1-contract.md`, and the complete issue #964 requirements snapshot.

- Missing vault identity recovery may remove only validated initial-pin atomic scratch in an otherwise empty root under its exclusive lock; preserve all populated-vault evidence.

- Git remote HEAD inspection treats only documented symbolic-ref status 1 as an unavailable default; propagate all ownership, timeout, launch and other exit failures.

- Apply bounded cross-frame secret reflection checks to SSE field names, comments and metadata values as well as decoded JSON field names, string deltas and exact numeric spellings before delivering any original frame bytes. Unknown or colonless fields cannot bypass these checks; repeated JSON enclosing keys cannot reset a nested key-fragment match.

- Model discovery must apply raw/Base64 credential reflection checks to the retained decimal spelling of numeric context limits as well as retained strings; reject the complete catalog before publication on a match.

- Coherent resource snapshots share the binary/JSON aggregate byte bound and must fail with explicit narrower-scope guidance before serialization, returning no partial resources or cursor on overflow.

- Published PR feedback uses only the closed read-only GraphQL documents, complete independently paginated review/conversation/thread inventories and repeated inventory plus PR binding. Exclude pending drafts before projection, retain approved/dismissed published content, recompute body/edit versions independently from provider state, and validate exact IDs/URLs/parent references. Preserve unknown actors without App/permission inference; no observation grants durable handling or execution. Follow the integration contract.

- Required CI contexts retain original suite/lifecycle/output and separately labeled workflow aggregate metadata. Independently validate complete bounded check/status proof and repeated observations. Per-result versions exclude required flags, App display names and workflow-wide attempt/update changes: partial reruns cannot make unchanged old CheckRuns eligible again. Keep original output inert and out of logs; follow the integration contract.

- Pinned required workflows follow `docs/cmds-delidev-integrations-contract.md`: require explicit source SHA, original numeric repository/path identity, the current ordered-parent test merge, complete suite/current-attempt job inventories and native PR requiredness. Preserve Unknown for missing, competing, stale or unsupported evidence and retain historical status-check proof/version compatibility. Observation cannot authorize execution.

- Permanent session deletion validates UUID uniqueness against its own 4,096-copy capacity; do not reuse the 1,000-link helper for that ownership plan.
- Private artifact decoding may use only its explicit owning byte bound with the same strict UTF-8, duplicate-key, unknown-field and single-document validation; public command JSON retains the fixed 1 MiB bound.

- Session terminal records and closed action/state enums follow `docs/cmds-delidev-terminals-contract.md`; keep native cleanup independent of observed exit and retain exact original operation identities. Pending input bytes belong only to accepted private dispatch; public resources retain operation/state metadata while omitting those bytes.

- Remediation defaults use merge; rebase requires explicit effective server/repository policy and an exact expected-head lease. Manual requests bind exact decimal set/problem revisions and immutable source/content identities. Closed typed push proofs distinguish verified, unchanged and uncertain state; native success alone never means handled.

- Every immutable manual-fix execution assignment independently requires Codex Execute mode and explicit workspace-write or full-access permission. Initial request acceptance cannot preserve write authority after the selected Agent becomes read-only or returns to default permission before dispatch.

- Explicit stopped Codex API account selection follows the sessions/proxy contracts. Require exact terminal/cleanup/checkpoint evidence and current eligibility from the original candidate snapshot; retain revisioned selection history, pause until explicit Resume and never reroute automatically. Preserve original usage/assignments and read the predecessor checkpoint under its complete original account/connection pair while creating a fresh successor grant. Explicitly switching away and back after reconnection must retain the original checkpoint connection independently of the fresh selected connection. Missing or account-bound history cannot switch; every switched relay request independently rejects remote history references.

- Manual compaction follows `docs/cmds-delidev-claude-compaction-contract.md`. Keep its distinct action/job/reference and original compact status separate from conversation input/outcome and usage; validate exact immutable restore assignments, native provenance, missing versus zero measurements and explicit Resume after failure.

- Claude continuation candidates require a correlated non-aborted successful or failed root outcome, unchanged permission and settled original input/callback/task/compaction facts. A `background_requested` terminal is ineligible even without a tracked background-task event. Failed predecessors require explicit Resume intent; a candidate never substitutes for independently verified native history and cleanup.

- Native subagent observations follow `docs/cmds-delidev-subagents-contract.md`. Validate original bounded ownership and complete batches before atomic publication; preserve exact receipts, source coverage, requested versus observed models and nullable non-additive usage. Supplied child output must explicitly declare partial=true; reject omitted or false markers before any batch publication. Live/unavailable children retain independent cleanup obligations after parent completion. Codex descendant inventory uses state-DB-only reads without native metadata repair. Native shutdown may refine terminal status without reopening lifecycle. Observation never grants child control or unproved continuation.
- Permanent deletion accepts original workspace-storage jobs with optional reserved snapshot UUIDs. Preserve omitted legacy fields and bind each nonempty snapshot ID only to its storage copy.
- Permanent-deletion copies for manual compaction retain a distinct original action UUID, never a replacement conversation execution ID. Reject missing, malformed or mixed action ownership; existing deletion plans retain their exact bytes and digest. Follow `docs/cmds-delidev-claude-compaction-contract.md`.

- Optional Account.subscription state contains only server-owned generation references, identity commitments and actor/lease fences. Configuration cannot manufacture or replace it; historical accounts omit it unchanged. Keep closed action/phase/capability values under `docs/cmds-delidev-subscription-contract.md`.
- Grok public tool, interaction and terminal documents follow the pinned issue #1091 harness/protocol contracts. Keep original request namespaces and lexical numeric IDs through the Grok-only decimal identity, nullable question data, exact decimal uint64 counters and exclusive response/terminal families. Original native Plan decisions cannot be converted to common Plan approval. Null/mixed/foreign response fields must fail before a native encoder or side effect.

- Retain original Grok request JSON bytes alongside the typed observation for byte-based proposal digests. Bound both within the existing public event limit; neither representation grants native, filesystem or response authority.
- Compare complete Grok request-ID values for original interaction/reply ownership, including the kind and decimal spelling. Normalized namespace keys retain duplicate-detection compatibility but cannot establish exact request identity.

- Explicit outbound profiles and immutable selections follow `docs/cmds-delidev-network-contract.md`. Keep closed Direct/HTTP/HTTPS/SOCKS5 modes, bounded exact-host/IP/CIDR bypass rules and separate write-only credentials; never interpret DNS answers or wildcards as bypass authority.

- Same-account Codex forks follow `docs/cmds-delidev-forks-contract.md`. Keep source boundary reservations read-only, original actor/current account checks at acceptance/claim/publication, once-only journaled native Fork, private rollout proof, complete multi-repository snapshot checks across native creation, separate opened roots for copy reads/writes, synchronized copied files/directories, independent child queues and immutable continuation settings. Unknown native/cleanup outcomes never authorize another Fork.

- Fork-origin metadata is a validated child-owned immutable seed with original Worker cleanup device and checkpoint/input digests. Permanent-deletion work may contain a bounded child fork-runtime ownership reference before any execution copy exists; parent work must never adopt a published child runtime. Follow `docs/cmds-delidev-forks-contract.md`.

- Observed native subagents retain version-1 paused completion even after every child closes. Fork requires an independent version-2 checkpoint; child observation or cleanup cannot promote that completion. Preserve both the subagent and fork contracts.

- ALLGREEN merge-queue CI follows `docs/cmds-delidev-integrations-contract.md`. Select only the exact entry head after complete stable PR/queue/entry/configuration/rules/check inventories. Recheck the complete CI inventory after the final rules read even when the initial observation is not queued, so entry into the queue invalidates earlier PR-commit evidence. Actions results require original `merge_group` and matching workflow suite/commit; HEADGREEN, absent proof and queue state never establish failure. Fresh remediation must match original queue/entry identity; retain historical proofs without current authority after removal.

- Keep negotiated native accounting unit kinds distinct under the usage contract. GrokClosedInput preserves its supplied uint64 total, original input/history/closure/source references and immutable attribution; it has no pricing or budget contribution.

- Shared subagent validation reserves every non-empty original parent-tool identity for one child across the complete batch and retained tree, including terminal children. Reject conflicting claims atomically; Codex retains its existing prohibition on Claude parent-tool fields. Follow `docs/cmds-delidev-subagents-contract.md`.

- Shared subagent usage validation requires an original bounded native report for every supplied usage observation. Decode the exact source-specific Codex cumulative, Claude task or Claude provider schema, reject unknown/case-aliased/duplicate fields and invalid native counters, and require exact nullable normalized counter parity before accepting any batch member. Collaboration/activity cannot carry usage. Validate Claude numeric counters through the existing closed provider schema using a temporary copy; preserve original report bytes and keep unavailable usage nil.

- Validate incoming subagent telemetry before merging retained last-available facts: Claude task reports cannot supply output/observed model; Codex activity cannot supply output/observed/requested models; Codex collaboration cannot supply observed models or content blocks. Keep retained earlier telemetry independently readable and reject an invalid complete batch before writes. Follow `docs/cmds-delidev-subagents-contract.md`.
- A restored managed subscription may retain a valid historical generation without a connection only while recovery-required. This is quarantined evidence; ordinary usable generations still require subscription-authenticated connections. Follow the storage and subscription contracts.

- Native Fork accepts API-authenticated Codex sources and the separately negotiated bounded OpenCode Unix General Chat profile. OpenCode freezes all five original operation UUIDs, a complete native message/part clone map and child-owned inherited transcript provenance before publication; inherited messages grant no queue/input or accounting authority. Reject managed subscription configuration before acceptance and Worker journaling until Fork owns a separately verified protected lease and joined credential write-back; native source inspection grants no authentication authority.
- Protected browser records contain only canonical server/device/account/profile IDs, monotonic revisions and closed cleanup state. Keep browsing data and paths outside Device metadata and product documents; follow `docs/cmds-delidev-browser-contract.md`.

- Repository-inspection metadata uses the independent `repository-inspection-metadata-v1` Worker capability. Validate/deduplicate it alongside titles/forwarding/terminals without changing existing values or the closed inspection input. Follow the workspace/protocol contracts.
- Automatic PR source revisions remain exact canonical decimal strings. Explicit session Stop/Archive/Restore retain server-owned automation suppression, cleared only by successful explicit Resume. Keep a failed automatic queue paused; replacement requires the original profile, settled failure and independently confirmed cleanup under the integration/session contracts.
- Native observation JSON preserves decimal uint64 revisions, distinct picker/executable IDs and bounded closed advisory metadata under `docs/cmds-delidev-native-models-contract.md`. Reject the entire observation on duplicate identity, malformed metadata, secret reflection or missing confirmed cleanup; detected Codex executable digests never grant readiness.

- ClaudeMainLoopInput and OpenCodeStep accounting follow the usage contract. Keep original source identities and immutable prices, nullable Claude primitives, unavailable OpenCode normalized zeros, independent native totals and disjoint OpenCode reasoning pricing. Never add assistant/cumulative/inherited observations or reinterpret units as responses.

- Keep independent service-native subscription identity closed to ChatGPT/Claude/Grok and its exact harness. API Provider identity remains separate. Match complete account/model identity in routing and immutable snapshots; retired Agents require explicit reconfiguration before resolution.

- Native subscription observations preserve sparse quota fields, authoritative credit counts and original generation-bound operation keys under the subscription contract. Only fresh positive native evidence may clear exhaustion; typed failure/reset outcomes cannot grant recovery.

- Worker network bindings/status use closed route states and exact decimal-string generations under the network contract. Separate desired/effective/native generations, original recipient scope and immutable active route copies. Public key or attachment metadata grants no pairing, execution, decrypted cache or observed native capability.
- A terminal workspace-storage job may retain one immutable `storage_reconciled_by` reference established only while settling an uncertain original through successful explicit recovery. It authorizes no native replay; verify original device/instance, immutable assignment and the successful recovery claim before acknowledging a stale original report.

- Workspace-storage recovery alone may carry a 3 MiB input containing two individually bounded original preparation/manifest copies and at most eight original claim references. The owning workspace/store decoders narrow this typed allowance; ordinary job/entity/command bounds stay unchanged.

- Codex child configuration follows issue #1101 and `docs/cmds-delidev-subagents-contract.md`: preserve omitted defaults, map the exact model/effort/numeric concurrency keys, freeze canonical child model identity before the first digest and revalidate that original model under the same parent account. Require System capability 22 and Worker capability 11. Relay requests narrow to the original parent/child model set with model-specific diagnostics and native response ownership; no independent child account, requested-to-observed substitution or later Agent/catalog rewrite is permitted.

- OpenCode foreground children follow issue #1208 and the subagent contract: close original task input/metadata and product/native tool references, retain exact independent child IDs, same model and nullable response counters. Keep task observations separate from independently read content/history, reject nested/reused/background ownership and never add child usage to root accounting.

- Native compaction context and versioned action/results follow `docs/cmds-delidev-compaction-contract.md`. Keep automatic context metadata outside input/content/accounting, preserve ordered item ownership and reject open boundaries at terminal/continuation. Version-2 Codex results cannot borrow Claude's version-1 lifecycle or failed-command Resume authority; domain shape validation alone grants no reserved capability or native send.

- The closed compaction-http response source accepts nullable nonnegative integer splits only with original compaction source-turn ownership and an absent unobserved action turn. Ordinary native response/event/result inventories cannot claim this Go-relay source. Preserve the version-2 Codex manual result union and independent legacy Claude version-1 fields. Follow `docs/cmds-delidev-compaction-contract.md`.
- OpenCode context metadata preserves a selected known/user-declared model limit in an optional immutable execution snapshot before its first digest. Omitted legacy limits remain unknown. Original automatic context progress uses the closed OpenCode native part identity and ordered lifecycle; summaries/continuation users grant no product input or fabricated accounting.

- OpenCode manual compaction uses closed version-3 input/results independently of Claude v1 and Codex v2. Require original session/input/part/summary/event identities, once-only original step usage and separate acknowledgment, lifecycle, history and cleanup proof; no mixed harness fields or failed-action Resume authority.

- New OpenCode known/declared context snapshots pin closed `native-v1` policy before the first digest. Omitted historical policy keeps its original initializer/checkpoint bytes; policy validation rejects unknown values and grants no observed native capacity.

- Compaction retains immutable original execution and restore inputs through one strict 3 MiB input/4 MiB job decoder. Store reads, server scope/settlement, Worker dispatch and original cleanup use those same typed bounds. Worker Connect receive capacity covers the finite serialized job; ordinary jobs retain their 1 MiB contract. Follow `docs/cmds-delidev-compaction-contract.md`.

- The existing 1 MiB Worker job output bound also covers the complete OpenCode Fork identity map. Worker preflight must reserve the entire serialized result before native mutation; native message/part inspection maxima cannot authorize truncation or an oversized durable output.

- Active pricing retains the closed compaction HTTP source's nullable counters through immutable response estimates. Price only available components under the original selected basis; missing cache splits remain unavailable and ordinary native response validation remains unchanged.
