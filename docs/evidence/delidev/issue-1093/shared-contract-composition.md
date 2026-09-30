# Shared compaction contract composition

On 2026-09-30, PR #1215 was open at
`cda6f56a3c3967027a3c6805a6bf6fb5be358251`. Its proposed common contract and
EntityKind 32, SystemCapability 15, WorkerCapability 5 and SessionChange field 9
reservations cover issues #1093, #1202 and #1203. They were not present on the
inspected main revision `ad0e3e9a29cb3d8375ab5d168bb160c35a023250`.

This replacement uses additive session-owned request/response declarations and
a new exclusive SessionContextCapability enum. It does not consume those shared
numbers, alter existing wire declarations or require a migration. Its existing
generic durable job carries a distinct original native action UUID. No unmerged
reservation was activated to bypass the main-first rule.

The profile contract was moved without removing its requirements to
`docs/cmds-delidev-claude-compaction-contract.md`, and navigation references were
updated. This keeps the proposed shared contract at its independent owned path.
The historical fixture observations and acceptance limits are unchanged.

When the prerequisite lands, composition against that main contract remains
required before activating any shared declarations. A passing Claude profile
check cannot establish Codex or OpenCode support, and this replacement does not
claim that the planned common operation/response or durable action resource is
already implemented for those profiles.

After the document move, `pnpm ci:contracts` passed all 113 checks (24.738 s),
and `git diff --check` passed. No generated source or runtime code changed in
this composition clarification.
