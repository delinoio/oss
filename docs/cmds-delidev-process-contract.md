# DeliDev process lifecycle contract

> Runtime ownership behavior follows [runtime ownership observations](cmds-delidev-ownership-contract.md). Its authentication, nonblocking observation and retained-handle rules supersede runtime ownership restrictions below; component/source ownership and unrelated validation remain separate.


## Scope
`cmds/delidev-cli/internal/process` owns Worker-launched Git, native harness and terminal process lifecycles. The complete product requirements remain in [issue requirements](cmds-delidev-requirements.md); distinguish implementation from native-platform verification in pull requests, issues and CI logs/artifacts.

## Runtime and Language
Go and OS process primitives. No container, additional application sandbox, or replacement for native harness permissions is introduced.

## Users and Operators
Each launch retains immutable UUID-v7 execution/job/session attribution and a private execution scope. Several process scopes may share that attribution. Worker shutdown and explicit Stop use retained native process, job or control handles.

## Interfaces and Contracts
The [subagent observation profile](cmds-delidev-subagents-contract.md) preserves cleanup ownership for every live/unavailable descendant after parent completion. The Worker keeps the original process open while native children settle, without sending child-control operations. History reads and closed tree state do not independently prove process cleanup; a failed, canceled or uncertain inspection retains the existing owned-process cleanup/report boundary.

A launch creates durable ownership and reaches a start barrier before receiving the actual command. Explicit resume crosses that barrier once. The retained control connection admits input after a valid ready frame; mismatched journal owner, process-birth or readiness observations log and continue. Actual transport, framing and filesystem I/O failures remain errors. Command arguments, environment, input and output never enter ownership journals. Native stdin, stdout and stderr remain distinct byte streams with bounded framing and backpressure, preserving partial multibyte sequences for the adapter to decode.

Natural exit, cancellation and parent disconnection attempt joined cleanup through retained handles. Record confirmed cleanup separately from admission. A missing, unreadable or mismatched ownership journal logs an unconfirmed observation; it never reconstructs a termination target from a PID, birth sample or service label. Actual I/O errors remain errors. Unknown cleanup permits subsequent work without claiming the previous process exited.

Linux uses a dedicated re-executed subreaper and retains the original command process/control handle. macOS uses an independently launched supervisor with a retained control connection; XNU coalition observations describe descendant completion. Windows retains the originally created non-breakaway Job Object handle. Cold recovery never opens a Job Object or signals a process reconstructed from historical metadata. A supervisor may observe surviving descendants without possessing their termination handles; retain incomplete scope records, bound the existing join and allow later admission. Platform primitive failures remain actual native errors.

A Unix stdout/stderr pipe setup failure after the start barrier but before command launch persists completed ownership before supervisor exit. Failure to persist that proof retains recovery uncertainty; native-started failures still require descendant reconciliation.

Unix reconciliation may persist completion only from an observed changed boot
identity and a valid original journal. Preserve the original owner, process birth,
boot identity and kernel metadata; a failed publication remains an actual error.
The existing owner maintenance and released-controller checks retire that completed
scope on a later scan. Same-boot supervisor death and missing, invalid or mismatched
ownership still cannot supply reboot completion proof.

The private supervisor transport is internal local IPC only and adds no remotely reachable Worker listener. A dropped control connection cancels the owned scope. Supervisor startup has a deadline, output is bounded by synchronous consumption, and slow/broken consumers cannot authorize duplicate execution.

## Storage
Owner-ID directories index process scopes directly, so session recovery does not scan unrelated process history. Worker Git inspection/preparation/cleanup uses these scopes, and workspace rollback attempts reconciliation without requiring ownership proof for admission. Private atomic ownership journals record version, execution owner, supervisor/process start identity, boot/kernel ownership identity, start-barrier state and reconciled completion. They contain no prompt, command/environment, upstream key, proxy token, or raw output. Keep incomplete journals for recovery. Owner reconciliation prunes only platform-validated completed scopes whose native controller lock has been released, before applying the 10,000 retained-scope bound. It reads bounded directory batches, serializes retirement with an owner maintenance lock, atomically renames each completed scope to a synchronized `.retired-<UUID>` entry and then removes it. Interrupted retired-directory removal is retryable; incomplete/invalid journals and journals still owned by an open Handle are retained. The generic reconciler retains the owner index even when empty; terminal ownership alone retires that empty index and its released recovery lock after synchronizing the independently verified cleanup report acknowledgement. Interrupted terminal retirement is locally retryable without server record availability; missing individual journals still cannot prove individual process completion. This cleanup cannot delete session workspaces.

Controller preparation creates the UUID scope directory exclusively under its already-private owner. If controller creation fails before native startup is attempted, rollback compares the original directory identity, removes only that empty directory and synchronizes its parent. Existing, replaced or nonempty scopes (including partial lock files) are retained for recovery; failed synchronization is also explicit recovery uncertainty. This path never substitutes for cleanup proof after native startup begins.

Capture the original directory identity through an open handle before attempting the controller, rather than deferring Windows identity lookup until rollback; see [Go's Windows file identity implementation](https://go.dev/src/os/types_windows.go). Close that handle before native startup or removal.

## Logging
Log execution/owner identifiers, stable lifecycle states and typed safe failure causes only. Raw native stderr and command/environment dumps are excluded.

Windows classifies bad image format, invalid executable signatures/modules, marked-invalid images, machine-type mismatch and missing image subsystems as unsupported, using the [documented loader status codes](https://learn.microsoft.com/en-us/windows/win32/debug/system-error-codes--0-499-). Native executable launch failures distinguish missing files/interpreters, permission denial, incompatible executable formats and unavailable native resources through closed classifications. The private supervisor forwards only that classification, never an OS error string or command path; launch failure remains an actual error; dependent cleanup attempts reconciliation and logs unknown ownership without blocking admission.

## Build and Test
Package race tests and vet; real temporary subprocesses must cover separate streams, stdin, partial bytes, natural exit, cancellation, daemonized descendants, start barriers, missing journals, unrelated processes, parent disconnect and crash recovery. Windows/Linux cross-compilation is not native lifecycle evidence. Native harness capability tests are separate from process-fixture tests.

## Dependencies and Integrations
Platform ownership primitives follow [Linux subreaper semantics](https://man7.org/linux/man-pages/man2/PR_SET_CHILD_SUBREAPER.2const.html) and [Windows Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects) and [atomic Job List startup attributes](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-updateprocthreadattribute). The initial Unix ownership implementation adapts the repository's existing async-commit-hook native scope mechanism; DeliDev adds execution ownership and distinct interactive streams without coupling either product's configuration or lifecycle state.

## Change Triggers
Update this contract and scoped AGENTS when process ownership, proof of termination, journal contents or supported-platform behavior changes. Preserve uncertainty whenever native proof becomes unavailable.

Preparation recovery uses `ReconcileOwnerContext` to stop between bounded native ownership checks when its Worker deadline/cancellation fires. A check already terminating through a retained handle finishes its bounded join before returning. Cancellation is not completion proof; partial reconciliation retains its uncertainty metadata while replacement preparation may proceed.

## Interactive native terminals

Terminal creation synchronizes its private process root and original owner index
before the Worker's native-start intent. A crash before native launch can
reconcile that retained empty index; missing or changed ownership remains
uncertain and does not block later admission. Shell-discovery children and the interactive shell share this exact
terminal owner index, so retained native scopes still require normal joined
reconciliation.

`process.Config.Terminal` selects a bounded 1–500-row, 1–1000-column terminal instead of ordinary pipes. Unix allocates a PTY inside the original independent supervisor and starts a new controlling session. Windows ConPTY is attached alongside the atomic suspended Job Object assignment. Neither replaces descendant ownership with a process group. Terminal stderr shares the native terminal byte stream; ordinary process streams retain their existing separation.

Resize is serialized with input and acknowledged after native application. Native bytes remain undecoded until the consuming client. PTY EOF and ConPTY closure are joined with output consumption and original descendant reconciliation before completed ownership is published. ConPTY drains output concurrently with closure to avoid its synchronous output deadlock. Unix slave allocation or shell launch failure before native execution publishes clean prelaunch completion; unsupported native APIs retain typed failures.

ConPTY startup explicitly sets `STARTF_USESTDHANDLES` with null standard handles
and disables handle inheritance. This prevents Windows from copying redirected
parent stdio into the shell instead of binding its pseudoconsole; see the
[Microsoft terminal discussion](https://github.com/microsoft/terminal/discussions/15814).
Ordinary pipe launches retain their explicit inherited handle list, and both
launch modes retain suspended creation with atomic Job Object assignment.

Focused macOS arm64 process fixtures exercise an actual interactive `/bin/sh`, TTY detection, resize, multibyte bytes, natural exit and owned descendant cleanup. Windows cross-compilation does not establish native ConPTY acceptance.

## Optional user-service controllers

Optional user-service controllers use process-birth observation for identity checking but do not reuse execution-scope signaling or change harness ownership. Their independent foreground exclusivity, durable Stop and registration cleanup are defined in the [user-service contract](cmds-delidev-user-services-contract.md). Service-controller exit is not per-session cleanup proof.

## Desktop host and independent Workers

The resident desktop CLI has a separate original-child lifetime from Worker-launched execution scopes. The native main process owns its kill-on-close Windows Job, persistent Linux spawning thread/parent-death signal and macOS original-parent/pipe observer. Independent detached Workers remain outside this containment and retain their original execution cleanup authority. App loss terminates only its resident CLI/internal server; forced termination leaves native cleanup uncertainty visible. Explicit fixed Local Workers may follow the same-server execution locator without changing immutable registration or selected outbound policy; Saved/remote Workers cannot. Follow the [desktop lifetime boundary](apps-delidev-desktop-contract.md#app-owned-sidecar-shutdown).
