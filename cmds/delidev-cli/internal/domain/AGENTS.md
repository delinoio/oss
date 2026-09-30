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

- Explicit stopped Codex API account selection follows the sessions/proxy contracts. Require exact terminal/cleanup/checkpoint evidence and current eligibility from the original candidate snapshot; retain revisioned selection history, pause until explicit Resume and never reroute automatically. Preserve original usage/assignments and read the predecessor checkpoint under its complete original account/connection pair while creating a fresh successor grant. Explicitly switching away and back after reconnection must retain the original checkpoint connection independently of the fresh selected connection. Missing or account-bound history cannot switch; every switched relay request independently rejects remote history references.

- Claude continuation candidates require a correlated non-aborted successful or failed root outcome, unchanged permission and settled original input/callback/task/compaction facts. A `background_requested` terminal is ineligible even without a tracked background-task event. Failed predecessors require explicit Resume intent; a candidate never substitutes for independently verified native history and cleanup.

- Explicit outbound profiles and immutable selections follow `docs/cmds-delidev-network-contract.md`. Keep closed Direct/HTTP/HTTPS/SOCKS5 modes, bounded exact-host/IP/CIDR bypass rules and separate write-only credentials; never interpret DNS answers or wildcards as bypass authority.

- ALLGREEN merge-queue CI follows `docs/cmds-delidev-integrations-contract.md`. Select only the exact entry head after complete stable PR/queue/entry/configuration/rules/check inventories. Recheck the complete CI inventory after the final rules read even when the initial observation is not queued, so entry into the queue invalidates earlier PR-commit evidence. Actions results require original `merge_group` and matching workflow suite/commit; HEADGREEN, absent proof and queue state never establish failure. Fresh remediation must match original queue/entry identity; retain historical proofs without current authority after removal.

- Keep negotiated native accounting unit kinds distinct under the usage contract. GrokClosedInput preserves its supplied uint64 total, original input/history/closure/source references and immutable attribution; it has no pricing or budget contribution.
