# async-commit-hook implementation evidence

The complete issue text is preserved in [requirements](cmds-async-commit-hook-requirements.md). This matrix is updated with executable evidence, not assumed completion.

| Requirement group | Implementation boundary | Required evidence | Status |
| --- | --- | --- | --- |
| Runtime, ownership, six targets, language | command core, platform adapters | six builds, local integration | pending |
| Configuration and dependency graph | configuration/planner | invalid settings, cycles, applicability | pending |
| Registration, hooks and durable receipts | registry/submission/install | worktree identity, detached hook, retries | pending |
| Source isolation and evidence cleanup | workspace/evidence | committed bytes, LFS/submodule refusal, cleanup faults | pending |
| Queue, replace, cancellation | scheduler/process adapters | cross-worker groups, descendants, interruption | pending |
| Daemon and on-demand parity | process lifecycle | startup races, drain/force, viewer independence | pending |
| Persistence and retention | SQLite/evidence | restart, prune dry-run, no resurrection | pending |
| Exact final gate and optional checks | shared gate | latest attempt, incompatible/missing evidence | pending |
| JUnit and Go test failures | reports | failures, malformed/missing reports, unknown locations | pending |
| Comparison and failed reruns | comparison/rerun | test identities, dependency closure, provenance | pending |
| Inbox and acknowledgement | common application service | shared state, idempotency, no implicit ack | pending |
| CLI JSON and exit statuses | CLI | all commands, stdout separation, wait expiry | pending |
| Pre-push | hook gate | all policies, multi-ref, non-HEAD, tags/deletions | pending |
| MCP, skill, three agent clients | stdio/install adapters | protocol, client discovery, preservation | pending |
| Web read/control and accessibility | React application | navigation, states, keyboard, focus, inert logs | pending |
| Local API pairing/security | Connect/auth | expiry, replay, revoke, Origin/Host, bounded files | pending |
| Credentials/trust/account ownership | env/redactor/state | boundary splits, permissions, no raw secrets | pending |
| Six archives/installers/Homebrew | packaging | target manifests, checksum/signature failure | pending |
| Explicit self-update and recovery | updater | active refusal, backup, rollback, ownership | pending |
| Manual release and static deployment | workflow | non-publishing dry-run, contract checks | pending |
| Documentation/support/operations | internal/public docs | route checks, source-to-doc coverage | pending |
| Actual six-target machine validation | owner amendment | explicitly excluded, never claim validated | excluded |
| Public release/site deployment | owner amendment | workflow only; no publication | excluded |
