# DeliDev Session Terminals Contract

## Scope

Issue #1088 adds interactive terminals owned by the session's execution Worker.
Canonical code lives in `cmds/delidev-cli/internal/{terminal,process,worker,server,store,cli}`,
the additive `TerminalService` and Worker messages in `protos/delidev/v1`, the
generated API client, and `apps/delidev/src/session-terminals.tsx`. This contract
does not authorize arbitrary-machine terminals or inbound Worker listeners.

## Runtime and Language

Go owns product validation, native execution and terminal lifecycle. Unix uses
PTYs inside the existing independent process supervisor; Windows uses ConPTY
attached to the original atomically assigned Job Object. React presents the
authenticated Connect operations. Terminal stderr shares the native byte stream.

## Users and Operators

Owner and client devices create and control terminals on a prepared active
session. The selected paired Worker claims native operations and reports their
outcomes through outbound authenticated connectivity. Viewing clients may
disconnect and reattach without replacing the shell.

## Interfaces and Contracts

The server advertises `SYSTEM_CAPABILITY_SESSION_TERMINALS_V1` with value 4;
the Worker must advertise `WORKER_CAPABILITY_SESSION_TERMINALS_V1` with value 3.
These independent enum spaces preserve the merged user-service system value 3.
Creation requires a ready
original workspace, current Worker instance/lease and current session revision.
The terminal retains the session/project, machine, original Worker instance and
claimed paired device. Input and resize require the current running terminal,
its revision and an available owning Worker. Close is a separate durable intent
that takes precedence over queued create/input/resize operations. Replayed
product receipts return current records instead of replaying native operations.

- `TerminalService.CreateTerminal` accepts a session mutation, optional absolute
  shell override and dimensions. Terminal IDs and all request/operation IDs are
  canonical UUID-v7 values.
- `ResourceService` lists and inspects `ENTITY_KIND_TERMINAL` records using its
  existing bounded filters, revisions, authorization and pagination.
- `TerminalService.ControlTerminal` accepts the closed input/resize/close action
  enum and a terminal mutation. At most one input/resize/create operation is
  pending per terminal. Input contains 1–32768 original bytes. Dimensions are
  1–500 rows and 1–1000 columns, initially 24×80 in the desktop and CLI.
- `TerminalService.WatchTerminalOutput` attaches to an existing terminal with
  an optional epoch UUID and uint64 sequence. It streams original bytes, explicit
  gaps and content-free heartbeat/current-terminal metadata. Reattachment uses
  this same operation; it never creates a shell.
- `WorkerService.WatchTerminals` is an auxiliary channel joined to the current
  primary Worker stream. `ClaimTerminal` durably claims the exact operation;
  `ReportTerminal` records bounded native facts; `PublishTerminalOutput` accepts
  ordered original byte frames. Every call revalidates machine/instance/device
  authority. Replacement Workers may claim only explicit close reconciliation
  on the same original device, never redispatch an earlier native operation.

The CLI exposes `delidev session terminal create|list|inspect|input|resize|output|reattach|close`.
`--id` targets the session for create/list and the terminal for other operations.
Mutations use the common durable `--request-id` and `--revision`; creation also
accepts `--shell`, `--rows` and `--columns`. Input reads exact bytes from
`--input FILE|-`, requires piped stdin or a file, and cannot share credential
stdin. Output/reattach accepts `--epoch`, `--after` and `--follow`; follow emits
versioned JSON byte frames with Base64 data and canonical decimal-string
sequences, preserving partial UTF-8 and uint64 precision. Without follow, it
returns one observation, which can be a metadata heartbeat.

On macOS the default shell comes from the effective native account's `UserShell`
directory-service field. Linux reads that account's passwd shell through owned
`getent`. Windows uses configured OpenSSH `HKLM\SOFTWARE\OpenSSH\DefaultShell`,
or the native system `cmd.exe` when that setting is absent. Failed discovery or
an invalid configured/default/explicit executable never selects another shell.
Explicit overrides must be absolute existing regular executable files, resolved
on the Worker. Unix shells receive `-i`; Windows shells receive no synthetic
Unix flags. The child receives bounded ordinary account/system context without
the Worker's pairing or provider credentials.

The launch directory is fixed to the original ready manifest's primary
repository, or its General Chat directory. The existing anchored continuation
identity verification covers every repository before native launch and permits ordinary dirty files
and commits. It does not take or replace the agent execution lease. Native
startup, input/resize, shell lookup and workspace verification remain bounded;
unknown native outcomes become `uncertain`, requiring exact close reconciliation.

Agent Stop preserves terminals. Archive atomically queues closes for only the
selected session and remains `archiving` until all agent/title/preparation and
terminal cleanup gates succeed. The central session publication barrier also
covers late completion/recovery paths. Session or terminal deletion refuses
unconfirmed terminal ownership. The repository's broader permanent-session
deletion operation remains separate work; its storage boundary cannot bypass
this gate. Close joins the original process tree and output machinery before
reporting cleanup. Missing/changed ownership never authorizes PID termination.

## Storage

Terminal records use the existing additive generic entity store and event/
receipt transaction. No destructive migration or schema bump is required.
There are at most eight live terminals per session, 128 retained terminal
records per session and 32 live terminals per machine. States are `starting`,
`running`, `exited`, `closed` and `uncertain`; cleanup is an independent fact.
Pending input bytes exist only in the accepted operation until its outcome or
close is committed. Mutation receipts retain references and input digests, not
a second input or output copy. Terminal history is retained after cleanup.

Worker-private synchronized operation journals retain original IDs, semantic
digests, claim/report IDs, phase and bounded native results; they never contain
input/output bytes. Native-start intent is synchronized before side effects.
An interrupted started operation is reported uncertain instead of repeating
input, resize or shell creation. Finished results retry their exact report;
confirmed reports retire the journal. A proven pre-native original creation
journal can reconcile a lost claim acknowledgement without inventing a process.
Native process journals retain the existing independent ownership/cleanup proof.

Output is ephemeral: each server ring retains at most 512 KiB and 1,024 frames, with 128 rings
and LRU eviction bounding retention to 64 MiB. Frames are at most 32 KiB. Worker
output uses a bounded 64-frame queue and backpressure. Identical most-recent
frame retries are acknowledged; changed bytes or reordered frames fail.
Eviction, server restart, missing prefixes, forward cursors and abandoned exit
output expose a gap. Every shell has one native output epoch; reconnect does
not restart its producer. Normal exit joins/drains output before cleanup is
reported; forced close may abandon bytes and exposes that fact.

## Security

Owner/client product authority is separate from machine-bound Worker authority.
No terminal is created merely by mounting a view, observing output, reconnecting
or replaying a request. Only original independently verified process scope
ownership permits termination, including shell-discovery children. Client
disconnect ends its observation; Worker reconnect within the same process
retains the original shell. Worker shutdown joins all owned terminals. Worker
replacement loses input authority and requires explicit cleanup reconciliation.
Shell paths, cwd, terminal input/output and account context never enter logs or
native process journals. Transport uses the existing authenticated loopback/TLS
and exact origin rules.

## Logging

Structured logs identify terminal/session/machine IDs, accepted action, replay,
state, cleanup fact and stable error code. Interrupted auxiliary channels log
the machine and safe code. Existing process-scope logs retain ownership IDs and
safe lifecycle facts. Never log raw bytes, shell command text, paths, tokens,
environment values or raw OS errors.

## Build and Test

Run root `go test -race ./cmds/delidev-cli/...` and
`go vet ./cmds/delidev-cli/...`; protocol changes require `pnpm proto:check`.
Generate and test the API client and run `pnpm test` in `apps/delidev` after
hydrating its required LFS assets. Remove repository-generated `dist` output
after validation. Controlled fixtures cover Unix TTY/resize/descendant cleanup,
ConPTY native ownership, Local/multi-repository Worktree/General Chat directory
selection, request/acknowledgement loss, interrupted-operation refusal, ordered
bytes, split UTF-8, reattachment gaps and Stop/Archive/deletion barriers.

Keep executed checks and retained fixtures distinct from native Windows/Linux,
real remote Worker, native desktop visual and release acceptance. The desktop
currently provides a bounded plain-text output view with line input and control
bytes, not a full VT/full-screen application emulator. Byte-stream consumers
retain all native control bytes. Evidence belongs in the evidence ledger.

## Dependencies and Integrations

Reuse the existing PTY, native Windows API, process ownership, workspace
verification, SQLite receipts/events, Connect and Connect Query dependencies.
Agent, preparation and title lifecycle remain separate owners joined by Archive.
No new inbound server or external account/provider dependency is introduced.

## Change Triggers

Update the project index, session/process/workspace contracts, protocol/client/
desktop contracts, evidence ledger and scoped `AGENTS.md` when terminal
ownership, limits, shell selection, native replay, cleanup or presentation
contracts change. Generate all Go/TypeScript/Connect Query bindings together.

## References

- [Project index](project-delidev.md)
- [Sessions](cmds-delidev-sessions-contract.md)
- [Owned processes](cmds-delidev-process-contract.md)
- [Workspaces](cmds-delidev-workspace-contract.md)
- [Protocol](protos-delidev-v1-contract.md)
- [API client](packages-delidev-api-client-contract.md)
- [Desktop](apps-delidev-desktop-contract.md)
- [Evidence](cmds-delidev-evidence.md)
- [Repository defaults](repository-defaults.md)
- [Issue #1088](https://github.com/delinoio/oss/issues/1088)
