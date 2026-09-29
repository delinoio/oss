# DeliDev same-account Codex session forks

## Scope

Issue #1092 adds independent native Codex forks to the Go session, Worker and
workspace owners. Issue #964 and the existing session, harness and workspace
contracts remain normative. Fork does not imply Sidechat, account switching,
transcript replay or support for unknown native history.

## Acceptance and publication

Owner/paired clients use authenticated `SessionService.ForkSession` and
`session fork`. A UUID-v7 request, exact source revision and completed native
turn identify the immutable boundary. Acceptance creates one durable Worker job;
the child is published only after both native history and every workspace are
verified and native process cleanup is independently confirmed. Exact receipt
replay observes the original job/child without repeating native side effects.

The initial profile preserves the original Worker, account, connection,
provider, model and exact immutable configuration. Current eligibility is checked
at acceptance, Worker claim, publication and later continuation. Source queued
inputs remain source-owned; the child has no copied queue, approvals, interactions
or executable historical work. The child retains source/session/boundary links.
Source continuation is excluded while a fork owns its boundary; this reservation
does not rewrite the source session or advance routing.

## Native and workspace ownership

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
authenticated same-machine Worker authority. Copying is bounded to 256 MiB and 100,000 entries per tree and a two-minute
Worker deadline, with per-chunk cancellation;
source observations are compared before and after all repositories. Unsupported
files, mixed snapshots or a failed second copy cannot publish a partial child.

## Evidence

Use temporary Worker/database/repository/native-provider fixtures without user
credentials. Keep deterministic implementation verification separate from
installed native, real-account, other-platform and release acceptance. Required
checks are root `go test -race ./cmds/delidev-cli/...`, root
`go vet ./cmds/delidev-cli/...` and `pnpm proto:check` for schema changes.

## Public commands and child continuation

`delidev session fork --id <source> --revision <revision> --turn-id <native-turn>
--name <child> [--workspace worktree|general-chat|local] [--local-worker-dir <scope>]
[--request-id <uuid-v7>] [--wait]` has the same authenticated semantics as Connect.
`delidev session fork --job-id <accepted-job> [--wait]` observes without mutation.
`--wait` observes for at most 135 seconds within a 145-second command deadline;
timeout returns the original job and cannot cancel or repeat Fork.
Private scope paths are CLI/Worker inputs and never part of public RPC rollout
selection. A copied child starts paused and requires a new queued input plus
explicit Resume. Its first turn resumes the child checkpoint, and subsequent
FIFO/Resume uses the child's verified completion on the same new history. Keep
original account/connection and configuration even after current Agent edits;
current eligibility failures retain input instead of rerouting. Child metadata
exposes its source/boundary link; source transcript records remain source-owned.

## Failure ownership

Acceptance, actor, Worker instance, original assignment, complete checkpoint,
workspace manifest and exact native request are independently bound. Current
source revision and authority are checked again when claiming and publishing.
The ordinary synchronized Worker journal precedes native creation; a started
operation is never retried after reconnect. Definite pre-native/copy failures may
settle failed without a child. Unknown creation or cleanup remains uncertain,
retains the private runtime/workspace and holds the source reservation for
operator investigation. No source execution claim is advanced by inspection.
