# DeliDev owned process contract

## Scope
`cmds/delidev-cli/internal/process` owns Worker-launched Git, native harness and terminal process lifecycles. The complete product requirements remain in [issue requirements](cmds-delidev-requirements.md); distinguish implementation from native-platform verification in pull requests, issues and CI logs/artifacts.

## Runtime and Language
Go and OS process primitives. No container, additional application sandbox, or replacement for native harness permissions is introduced.

## Users and Operators
Each launch belongs to an immutable UUID-v7 execution/job/session owner and a private execution scope. An owner may have several distinct process scopes. Worker shutdown and explicit Stop act only on owned processes.

## Interfaces and Contracts
The [subagent observation profile](cmds-delidev-subagents-contract.md) preserves cleanup ownership for every live/unavailable descendant after parent completion. The Worker keeps the original process open while native children settle, without sending child-control operations. History reads and closed tree state do not independently prove process cleanup; a failed, canceled or uncertain inspection retains the existing owned-process cleanup/report boundary.

A launch creates durable ownership and reaches a start barrier before receiving the actual command. Explicit resume crosses that barrier once. The shared admission boundary checks the original Start context synchronously; already-observable cancellation or deadline expiry refuses command transmission/suspended-child resume and independently joins the original owner before returning its typed result. Cleanup uncertainty retains RecoveryRequired precedence. Cancellation racing an already-admitted command keeps the original post-admission cleanup/uncertainty semantics. Command arguments, environment, input and output never enter ownership journals. Native stdin, stdout and stderr remain distinct byte streams with bounded framing and backpressure, preserving partial multibyte sequences for the adapter to decode.

Natural exit, cancellation and parent disconnection reconcile owned descendants before confirming resource release. PID absence alone is not proof; use start identity plus the execution owner and an OS ownership scope. Recovery never signals a PID whose recorded start identity differs. A missing or unreadable ownership journal for a launched process is an explicit recovery error. Uncertainty blocks replacement and credential/runtime destruction.

Linux uses a dedicated re-executed subreaper per process scope; its kernel child-reaping result proves descendant completion. If that supervisor is itself killed before durable completion, missing ancestry cannot be reconstructed safely; keep uncertainty until independent proof, such as a changed boot identity. macOS uses an independently launched, uniquely named per-execution supervisor and its inherited XNU resource coalition, verifying membership and process birth before signaling. Unsupported kernel interfaces fail explicitly. Windows uses `PROC_THREAD_ATTRIBUTE_JOB_LIST` to atomically create a suspended child inside a non-breakaway kill-on-close Job Object, then persists its start identity before resume; retained job identity supports recovery and prevents unrelated PID termination.

A Unix stdout/stderr pipe setup failure after the start barrier but before command launch persists completed ownership before supervisor exit. Failure to persist that proof retains recovery uncertainty; native-started failures still require descendant reconciliation.

Unix reconciliation atomically persists completion when the current boot identity
differs from the valid original journal. Preserve the original owner, process birth,
boot identity and kernel metadata; a failed publication remains a recovery error.
The existing owner maintenance and released-controller checks retire that completed
scope on a later scan. Same-boot supervisor death and missing, invalid or mismatched
ownership still cannot supply reboot completion proof.

The private supervisor transport is internal local IPC only and adds no remotely reachable Worker listener. A dropped control connection cancels the owned scope. Supervisor startup has a deadline, output is bounded by synchronous consumption, and slow/broken consumers cannot authorize duplicate execution.

## Storage
Owner-ID directories index process scopes directly, so session recovery does not scan unrelated process history. Worker Git inspection/preparation/cleanup uses these scopes, and workspace rollback requires process reconciliation first. Private atomic ownership journals record version, execution owner, supervisor/process start identity, boot/kernel ownership identity, start-barrier state and reconciled completion. They contain no prompt, command/environment, upstream key, proxy token, or raw output. Keep incomplete journals for recovery. Owner reconciliation prunes only platform-validated completed scopes whose native controller lock has been released, before applying the 10,000 retained-scope bound. It reads bounded directory batches, serializes retirement with an owner maintenance lock, atomically renames each completed scope to a synchronized `.retired-<UUID>` entry and then removes it. Interrupted retired-directory removal is retryable; incomplete/invalid journals and journals still owned by an open Handle are retained. The generic reconciler retains the owner index even when empty; terminal ownership alone retires that empty index and its released recovery lock after synchronizing the independently verified cleanup report acknowledgement. Interrupted terminal retirement is locally retryable without server record availability; missing individual journals still cannot prove individual process completion. This cleanup cannot delete session workspaces.

Controller preparation creates the UUID scope directory exclusively under its already-private owner. If controller creation fails before native startup is attempted, rollback compares the original directory identity, removes only that empty directory and synchronizes its parent. Existing, replaced or nonempty scopes (including partial lock files) are retained for recovery; failed synchronization is also explicit recovery uncertainty. This path never substitutes for cleanup proof after native startup begins.

Capture the original directory identity through an open handle before attempting the controller, rather than deferring Windows identity lookup until rollback; see [Go's Windows file identity implementation](https://go.dev/src/os/types_windows.go). Close that handle before native startup or removal.

## Original Worker controller observation

`ControllerIdentity` contains only a PID and exact kernel birth; it grants no native process-scope ownership or termination authority. `ObserveControllerIdentity` returns the closed outcomes same-original-alive, original-exited and unknown. A malformed identity or inspection error remains unknown. A different independently verified birth proves only that the original controller exited; the unrelated current process is never adopted or terminated.

Linux uses boot identity plus kernel start ticks. Missing procfs evidence alone is insufficient; a signal-zero kernel ESRCH independently confirms absence and delivers no signal. Darwin uses the kernel start timestamp and a successful empty exact-PID kernel observation to confirm absence. Windows uses creation FILETIME, narrowly classified nonexistent-PID OpenProcess failure or a signaled process handle. Access denial and every other failed kernel read remain unknown. These observations never call `ProcessAlive`, scan process lists, erase journals or prove descendant cleanup.

The Worker owns the private generation/scope/registration/desktop-client bindings and synchronized evidence publication. Fresh authenticated desktop admission rechecks that evidence under its final original locks; see [the CLI contract](cmds-delidev-contract.md#main-desktop-automatic-worker-management). Legacy missing proof is not reconstructed or migrated. Record actual platform acceptance separately from fixtures and builds.

## Logging
Log execution/owner identifiers, stable lifecycle states and typed safe failure causes only. Raw native stderr and command/environment dumps are excluded.

Windows classifies bad image format, invalid executable signatures/modules, marked-invalid images, machine-type mismatch and missing image subsystems as unsupported, using the [documented loader status codes](https://learn.microsoft.com/en-us/windows/win32/debug/system-error-codes--0-499-). Native executable launch failures distinguish missing files/interpreters, permission denial, incompatible executable formats and unavailable native resources through closed classifications. The private supervisor forwards only that classification, never an OS error string or command path; launch failure still requires the normal ownership reconciliation before dependent runtime cleanup.

## Build and Test
Package race tests and vet; real temporary subprocesses must cover separate streams, stdin, partial bytes, natural exit, cancellation, daemonized descendants, start barriers, missing journals, unrelated processes, parent disconnect and crash recovery. Windows/Linux cross-compilation is not native lifecycle evidence. Native harness capability tests are separate from process-fixture tests.

## Dependencies and Integrations
Platform ownership primitives follow [Linux subreaper semantics](https://man7.org/linux/man-pages/man2/PR_SET_CHILD_SUBREAPER.2const.html) and [Windows Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects) and [atomic Job List startup attributes](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-updateprocthreadattribute). The initial Unix ownership implementation adapts the repository's existing async-commit-hook native scope mechanism; DeliDev adds execution ownership and distinct interactive streams without coupling either product's configuration or lifecycle state.

## Change Triggers
Update this contract and scoped AGENTS when process ownership, proof of termination, journal contents or supported-platform behavior changes. Preserve uncertainty whenever native proof becomes unavailable.

Preparation recovery uses `ReconcileOwnerContext` to stop between bounded native ownership checks when its Worker deadline/cancellation fires. A check already terminating owned work finishes its confirmation before returning. Cancellation is not completion proof; partial reconciliation retains workspace recovery and blocks replacement preparation.

## Interactive native terminals

Terminal creation synchronizes its private process root and original owner index
before the Worker's native-start intent. A crash before native launch can
reconcile that retained empty index; missing or changed ownership remains
uncertain. Shell-discovery children and the interactive shell share this exact
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
