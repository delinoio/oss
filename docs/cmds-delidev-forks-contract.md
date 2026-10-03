# DeliDev same-account Codex session forks

## Scope

Issue #1092 adds independent native Codex forks to the Go session, Worker and
workspace owners. Issue #964 and the existing session, harness and workspace
contracts remain normative. Fork does not imply Sidechat, account switching,
transcript replay or support for unknown native history.

Canonical owners are `cmds/delidev-cli/internal/{server,worker,workspace,harness/codex,cli}`, additive `protos/delidev/v1` and generated clients, with presentation in `apps/delidev`.

## Runtime and Language

Go owns business logic, durable jobs and native/workspace operations. The desktop
uses existing React/TypeScript and authenticated Connect Query. Codex is pinned
to the repository's independently verified native `0.151.0` profile.

## Users and Operators

Owner and paired clients request/observe Fork through Connect, CLI or desktop.
Only the original authorized Worker may inspect its private source and create a
child. Provider/account authorities remain the original immutable selection. The current native Fork coordinator supports API-authenticated Codex sources only. Managed subscription assignments are rejected before acceptance and Worker journaling until Fork has a separately verified protected lease, joined cleanup and final credential write-back. Ordinary Fork capability does not grant managed authentication.

## Interfaces and Contracts

### Acceptance and publication

Owner/paired clients use authenticated `SessionService.ForkSession` and
`session fork`. A UUID-v7 request, exact source revision and completed native
turn identify the immutable boundary. Only an original root session is eligible;
reject a published forked child before accepting another job, even after that
child completes its own turn. Acceptance creates one durable Worker job;
the child is published only after both native history and every workspace are
verified and native process cleanup is independently confirmed. Exact receipt
replay observes the original job/child without repeating native side effects.

Sessions with observed native subagents retain version-1 paused completion under
the subagent contract. Their closed child inventory cannot satisfy Fork's
independent version-2 checkpoint requirement or promote that completion.

Status observation and acceptance-receipt reads require current owner/client
authority in the same read transaction before reading any job or child document.
Worker credentials and missing principals cannot use this client-only observation
RPC. A stale paired-client principal cannot bypass device revocation.

The initial profile preserves the original Worker, account, connection,
provider, model and exact immutable configuration. Current eligibility is checked
at acceptance, Worker claim, publication and later continuation. Source queued
inputs remain source-owned; the child has no copied queue, approvals, interactions
or executable historical work. The child retains source/session/boundary links.
Source continuation is excluded while a fork owns its boundary; this reservation
does not rewrite the source session or advance routing.

First child dispatch applies the ordinary fresh Worker-instance gate, including
rejection of observations more than one second in the future. A backward host
clock move cannot promote a disconnected Worker into a queued-input claim or
turn an empty Resume into dispatch readiness.

### Public commands and child continuation

`delidev session fork --id <source> --revision <revision> --turn-id <native-turn>
--name <child> [--workspace worktree|general-chat|local] [--local-worker-dir <scope>]
[--request-id <uuid-v7>] [--wait]` has the same authenticated semantics as Connect.
`delidev session fork --job-id <accepted-job> [--wait]` observes without mutation.
`--wait` observes for at most 135 seconds within a 145-second command deadline;
timeout returns the original job and cannot cancel or repeat Fork.
Private scope paths are CLI/Worker inputs and never part of public RPC rollout
selection. A copied child starts paused and requires a new queued input plus
explicit Resume. Its first turn resumes the child checkpoint, and subsequent
FIFO/Resume uses the child's verified completion on the same new history. Bind
lost-report recovery to that fork runtime for the first child execution;
the fresh execution ID identifies the original report rather than its native home.
Keep original account/connection and configuration even after current Agent edits;
current eligibility failures retain input instead of rerouting. Child metadata
exposes its source/boundary link; source transcript records remain source-owned.

First-child execution publication must bind the exact native child thread from
the immutable fork assignment. Its accepted new input must use a fresh turn,
never the inherited last source turn. Check these identities before changing
progress or queue accounting; rejected publications leave source, child, queue
and original job records unchanged.

## Storage

### Native and workspace ownership

Use pinned Codex `0.151.0` `thread/fork` with inclusive `lastTurnId`, explicit
child cwd and runtime workspace roots. Path import may name only the exact
validated rollout returned for the original Worker-private runtime. Reject
incomplete, active, paginated, child, goal and unsupported histories. The initial
profile supports complete user/assistant text and reasoning turns (at most 128
turns / 4 MiB, with a 64 MiB rollout); settled tools and rich auxiliary histories
remain unsupported until their complete inherited state is independently verified. Unknown
creation or cleanup outcomes retain uncertainty and never authorize replay.

Project forks default to separate detached worktrees at every actual source
HEAD, including unpushed commits, index changes, working changes and ignored or
untracked files. No fetch may move that boundary. General Chat copies its owned
files to a new owned directory. Explicit Local sharing requires independently
authenticated same-machine Worker authority and an original Local source whose
checkouts are all user-owned. Reject Local sharing from managed Worktree sources
before acceptance and again at Worker preparation/publication: parent deletion
owns those paths. Use an independent Worktree copy instead. Copying is bounded to 256 MiB and 100,000 entries per tree and a two-minute
Worker deadline, with per-chunk cancellation;
source observations are compared before and after all repositories. Unsupported
files, mixed snapshots or a failed second copy cannot publish a partial child.
Source reads and destination writes use separate opened filesystem roots;
canonical destination validation rejects linked targets before copying. Copied
files and directories are synchronized before the ready manifest is published,
and original permission modes are restored independently of the Worker umask.
Git-reported index paths use the native Worker separator and path comparison
rules before canonical ownership checks; Windows slash-separated Git output
must not be compared as an unnormalized Unix path.

Optional preparation/session/execution JSON fields and job kinds are additive.
Existing records retain their omitted-field bytes. Fork uses the existing
synchronized job journal, UUID-v7 receipts, private native runtime and atomic
SQLite state/events; no destructive schema migration or external object storage
is introduced. The Worker stores one canonical synchronized `fork-completion.json`
whose exact digest binds the native child and its immutable manifest/configuration.

### Permanent deletion and independent child lifetime

Publication retains the immutable configuration/boundary seed and original Worker
cleanup device on the child. First continuation verifies that seed and its own
preparation/checkpoint, without requiring any surviving parent session or fork
job. Parent permanent deletion removes only the parent-owned job journals and
native histories; it preserves published child runtimes and managed worktrees.
Deletion before the child's first input still requires the owning Worker's
acknowledgement of both its workspace and exact digest-bound fork runtime.
The cleanup plan carries bounded identities/digests, never native paths or history.
Interrupted child cleanup retains its original tombstone and checkpoint ownership
proof. A completed acknowledgement is reusable only while every original copy
remains absent. An unpublished unresolved fork blocks accepting irreversible
parent deletion until that original operation proves cleanup or publication.

A managed child copies from the parent's actual repository path while retaining
the original manifest's stable registration source, after comparing both Git
common directories. Recovery and removal use that stable source after the parent
managed workspace has been removed. Explicit Local still owns no checkout files.

Managed database restore follows the shared storage contract and refuses
replacement while a claimed or uncertain fork retains native ownership.
Restoration preserves published fork metadata and original execution selection,
while retaining paused dispatch and requiring recovery. Database publication
alone cannot prove native cleanup or authorize continuation or another Fork.

## Security

### Failure ownership

Acceptance, actor, Worker instance, original assignment, complete checkpoint,
workspace manifest and exact native request are independently bound. Current
source revision and authority are checked again when claiming and publishing.
The ordinary synchronized Worker journal precedes native creation; a started
operation is never retried after reconnect. Definite pre-native/copy failures may
settle failed without a child. File, HEAD and index drift detected before native
creation must roll back every verified owned copy and release the source job
reservation only after cleanup succeeds. Keep the original pre-copy observation
through preparation so changes before the first copy are also rejected before
native creation. Unproved Git process ownership or failed cleanup remains
uncertain; the same drift detected after native creation remains uncertain.
Unknown creation or cleanup remains uncertain,
retains the private runtime/workspace and holds the source reservation for
operator investigation. No source execution claim is advanced by inspection.

Rejected source-history inspection removes its proven-unused fresh runtime only
after independently confirmed source-process cleanup. Synchronize its parent and
confirm absence before returning a definite rejection; failed removal or unjoined
inspection retains uncertainty. Successful inspection keeps the runtime for the
original native creation attempt.

Workspace request derivation checks all repository manifest eligibility and the
complete request structure before creating a child process index or running Git.
An unborn later repository, duplicate repository ID or missing primary therefore
cannot leave an unpublished child process scope after definite rejection. Actual
source HEADs are read only after those checks; native read/ownership failures
retain recovery classification and process evidence.

Every later pre-native workspace rejection applies the same unused-runtime
cleanup after joined source inspection and independently verified owned-copy
rollback. A `RecoveryRequired` workspace result may leave owned copies without
attempting rollback and cannot authorize runtime removal, even before native
creation. Disable this removal authority before attempting to open the native
child process: possible child state and unjoined inspection always remain
retained. Log only the closed runtime phase and stable failure code.

The product never selects a rollout path. Native metadata selects one exact
regular Worker-private `sessions/*.jsonl` file, with canonical rooted accesses,
link/special-file refusal, immutable byte proof and before/after identity checks.
Rollout descendant permissions use owner-only Windows DACL checks through the
private-state security owner; Unix keeps its existing group/other write-bit
refusal. A private home alone cannot hide an unprotected linked file. Go's
emulated Windows permission bits are not an ACL observation.
Secret Local proof is dedicated write-only authority, excluded from receipts.
Fork receives no registered inference grant; only later ordinary execution may
receive one after current eligibility checks. Imported source workspaces remain
original-owned; the fork coordinator never advances their execution claim.

## Logging

Keep structured `log/slog` start/finish, accepted request/job/source identities,
native request ownership, stable outcome codes and ordinary process/preparation
lifecycle evidence. Never log rollout paths/bodies, copied bytes, prompts,
configuration content, credential values, private environments or raw native
errors. An uncertain result remains independently visible in its retained job.

## Build and Test

Use temporary Worker/database/repository/native-provider fixtures without user
credentials. Keep deterministic implementation verification separate from
installed native, real-account, other-platform and release acceptance. Required
checks are root `go test -race ./cmds/delidev-cli/...`, root
`go vet ./cmds/delidev-cli/...` and `pnpm proto:check` for schema changes.

Frontend changes additionally require `pnpm test` from `apps/delidev`. Native
fixtures are explicitly opted in with `DELIDEV_NATIVE_THREAD_EXECUTABLE`; use
private generated homes and keyless scripted loopback providers. Required output
packages are generated before compilation, and repository-owned `dist` output
is removed from the final worktree.

## Dependencies and Integrations

Reuse existing workspace closed inspections, process ownership/cleanup, Worker
journals/outboxes, immutable execution checkpoints, current account selection and
Connect generation. Native Fork is the pinned official app-server method; no
transcript synthesis, cross-account routing, SDK/engine or hosted service is added.

## Change Triggers

Update this contract, project index, issue-owned evidence, session/workspace/harness
contracts and the relevant scoped Go owners' `AGENTS.md` files for profile or ownership changes.
Update protocol/client contracts, their AGENTS files and generated bindings for
RPC changes. Keep desktop contracts/AGENTS synchronized with presentation changes.

## References

- [DeliDev project](project-delidev.md)
- [Repository defaults](repository-defaults.md)
- [Session contract](cmds-delidev-sessions-contract.md)
- [Workspace contract](cmds-delidev-workspace-contract.md)
- [Harness contract](cmds-delidev-harness-contract.md)
- [Issue #1092](https://github.com/delinoio/oss/issues/1092)
- [Complete issue #964 requirements](cmds-delidev-requirements.md)

Workspace storage and fork ownership compose at the original source boundary. Fork
acceptance, claim and publication require a present workspace, with no pending or
uncertain storage operation. Storage admission waits for unresolved fork jobs to
settle; a stored workspace must be explicitly restored before it can be forked.
