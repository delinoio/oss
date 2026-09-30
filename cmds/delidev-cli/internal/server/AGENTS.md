# DeliDev server ownership

Follow the parent instructions and the owning contracts in `docs/`. These rules retain the original requirements; cross-domain changes must also read the affected owners' instructions.

- Keep PR activity projection and original-source validation in `activity_pr.go` under the activity contract. Validate immutable original versions and stable numeric PR ownership, preserve historical navigation across repository renames, cap complete pages in protobuf and JSON, and advertise the typed activity capability. Reads cannot change Inbox or PR handling or infer verified handling from an attempt.

- The executable is `delidev`; ordinary commands cannot implicitly start the server. Server/sidecar behavior is identical. Local detached TLS readiness may trust only the explicitly configured certificate, with exact peer matching, certificate validity/server-use checks and authenticated status. Wildcard binds dial matching loopback; ordinary/remote clients retain standard hostname and CA verification. Release the completed child startup lifecycle lock before serving any HTTP request, so authenticated readiness already permits immediate reuse and explicit Stop even while startup logging or maintenance setup is delayed.

- Keep business logic in Go and product communication in authenticated Connect. Workers initiate outbound connections; never add a client-facing WebSocket or SSE API.

- Use private temporary state/accounts/repositories in tests. Never access user logins, redeem credits, publish to GitHub, or invoke inference from ordinary tests.

- Preserve durable request receipts, typed revision checks, atomic state/events/routing, independent outcome/archive/recovery, uncertainty before retries, and deletion tombstones.

- Follow `docs/cmds-delidev-providers-contract.md` for non-inference validation. Keep HTTP outside account locks/transactions, cancel on disconnect/revocation, revalidate authority and generation at publication, and replay accepted failed observations without network work. Public/custom model lists do not establish credential validity; unobservable authentication records an unsupported validation state. Never follow redirects, inherit environment proxies, retry provider requests, ingest diagnostic bodies or infer quota recovery/costs from validation.

- Execution progress and private completion checkpoints must preserve every accepted same-turn input in delivery order, with the primary input first, distinct UUIDs and canonical SHA-256 digests. Recheck each retained queue record's session/execution/mode/request/digest before continuation. An absent legacy list represents only its single original input, never reconstructed Steer history. Bound accepted inputs to 4,095 and checkpoint files to 1 MiB without truncation; missing, reordered or extra input evidence cannot authorize continuation. Public Steer acceptance/control/delivery uses its separately validated durable claim and publication paths; private checkpoint primitives never independently grant send authority.

- Live permission acceptance requires a unique original thread/turn/call/arrival and possible delivery of its exact owner response. Hash immutable sent bytes using pinned effective entries precedence, including present empty entries; never combine deprecated mirrors or reinterpret filesystem paths/globs. Require exact core output permissions, scope and strict-review meaning. Publish only content-free claim-bound evidence for a permissions approval, decrement unconfirmed accounting once, and preserve closure, transport, cleanup and unrelated recovery independently. Generic command/file completion, request resolution and missing history remain insufficient. Validation logs use closed stages without grants, paths, raw output or digests.

- Generic resource pagination must fit both binary and JSON Connect encodings and resume after the last returned record when the byte budget truncates a count-bounded page.

- Keyless account cleanup/deletion must remain usable without an OS credential store. Skip vault access only with validated immutable keyless API provider ownership; preserve relay cancellation, cleanup generations, receipt replay and credential-bearing staged-intent reconciliation.

- Repository GitHub access follows the integration contract: derive the exact configured owner/repository/profile/generation, bound and join reads outside locks, and recheck authorization and repository revision before response. Keep all eight endpoint states independent; absent head evidence cannot invent Checks/statuses, and availability cannot imply successful CI, satisfied rules, reviewer identity or future authorization. No fallback token, response URL authority, access persistence or inspection receipt.

- Session PR links follow the integration contract. Resolve actual PR/repository numeric and node identities through fresh explicit-profile detail reads; recheck project membership/revision, repository selection/generation and actor before atomic creation. Bound/deduplicate per session, retain links after Archive or configuration changes, and unlink only an exact session-owned revision. Actor-bound receipt replay cannot repeat lookup or resurrect a tombstoned link. Historical association metadata grants no execution, current access, evidence handling or remediation authority.

- Official GitHub form preparation is a current profile/revision-bound owner/client metadata read, without PAT access or server-side browser launch. Keep verified canonical prefills and explicit classic public/private choices; explain manual fine-grained repository selection and unavailable Checks prefill. Local presentation accepts only closed canonical GitHub form/PR/issue URLs, with no shell, inherited credential environment, arbitrary browser command or output-pipe inheritance. Bound/join dispatch and preserve uncertainty without automatic retry.

- Applicable PR rules use the current base ref and complete active-only endpoint inventory with checked page metadata, exact ruleset/App numbers, original-policy digests and repeated inventory plus PR binding. Preserve unknown sources and explicit zero App IDs, aggregate without overwriting independent rulesets, and fail on partial/mixed/changed data. Never infer evaluated-commit/result or remediation authority from a rule read; follow the integration contract.

- Required CI inspection uses only the fixed read-only GraphQL document, current REST-bound PR identity and complete repeatedly checked rule/rollup inventories. Match active rules, exact context/App, PR-specific required flags and verified head/test-merge operands. Preserve Actions event provenance, same-name Check/status separation, unknown App-bound statuses/queues/future rules and terminal-only failure classification. Recompute before publication; observations never substitute for fresh remediation authorization. Follow the integration contract.

- Remediation policy persistence follows the integration contract. Validate server defaults and optional complete repository replacements identically; never merge false/empty/missing override fields with server authority. Keep stable external reviewer IDs separate from local Agent/machine references, inventory/remap policy-only machines in portable configuration and recheck dependencies at deferred publication. Saving metadata cannot dispatch work, reset a chain, consume attempts or authorize evidence handling; execution needs independently fresh prerequisites.

- Revoked desktop eligibility must bind the entire credential, including token and pairing identity, to the private commitment retained after fresh authenticated local pairing or a prior immutable completed recovery receipt. Synchronize fresh proof before publishing the active credential or retiring the pending pairing journal; interruptions must retry that exact accepted pairing. Never reconstruct missing original proof from revoked metadata or ordinary reuse; legacy revoked scopes without proof remain recovery-required.

- Require and validate the original local desktop pairing journal and its request-bound grant before revoked eligibility or recovery intent. Both original credential and pairing commitments are mandatory on journal load; validate archived pairing ownership on retries and never reconstruct absent original pairing evidence.

- Enforce the optional desktop `--expected-endpoint` guard against the authenticated owner client before any recovery journal, archive or pairing mutation. The guard never selects another endpoint, and a mismatch preserves the original credential and all recovery state.

### Source ownership

- Keep process startup/shutdown in `server_startup.go`, HTTP authorization in `server_http.go`, Connect registration in `server_routes.go`, and system status/capability responses in `server_status.go`. New service behavior belongs in its service file; preserve middleware and registration order.

- Verified settled failed Claude completions retain the original failed job/outcome, confirmed cleanup and paused dispatch without next-input intent. Explicit Resume and exact receipt replay use existing fresh-ownership/FIFO gates; lost-report recovery is comparison-only and must preserve the original failure diagnostic on both the session and execution job, plus the pause.
