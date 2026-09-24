# DeliDev owned process contract

## Scope
`cmds/delidev-cli/internal/process` owns Worker-launched Git, native harness and terminal process lifecycles. The complete product requirements remain in [issue requirements](cmds-delidev-requirements.md); the evidence ledger distinguishes implementation from native-platform verification.

## Runtime and Language
Go and OS process primitives. No container, additional application sandbox, or replacement for native harness permissions is introduced.

## Users and Operators
Each launch belongs to an immutable UUID-v7 execution/job/session owner and a private execution scope. An owner may have several distinct process scopes. Worker shutdown and explicit Stop act only on owned processes.

## Interfaces and Contracts
A launch creates durable ownership and reaches a start barrier before receiving the actual command. Explicit resume crosses that barrier once. Command arguments, environment, input and output never enter ownership journals. Native stdin, stdout and stderr remain distinct byte streams with bounded framing and backpressure, preserving partial multibyte sequences for the adapter to decode.

Natural exit, cancellation and parent disconnection reconcile owned descendants before confirming resource release. PID absence alone is not proof; use start identity plus the execution owner and an OS ownership scope. Recovery never signals a PID whose recorded start identity differs. A missing or unreadable ownership journal for a launched process is an explicit recovery error. Uncertainty blocks replacement and credential/runtime destruction.

Linux uses a dedicated re-executed subreaper per process scope; its kernel child-reaping result proves descendant completion. If that supervisor is itself killed before durable completion, missing ancestry cannot be reconstructed safely; keep uncertainty until independent proof, such as a changed boot identity. macOS uses an independently launched, uniquely named per-execution supervisor and its inherited XNU resource coalition, verifying membership and process birth before signaling. Unsupported kernel interfaces fail explicitly. Windows uses `PROC_THREAD_ATTRIBUTE_JOB_LIST` to atomically create a suspended child inside a non-breakaway kill-on-close Job Object, then persists its start identity before resume; retained job identity supports recovery and prevents unrelated PID termination.

The private supervisor transport is internal local IPC only and adds no remotely reachable Worker listener. A dropped control connection cancels the owned scope. Supervisor startup has a deadline, output is bounded by synchronous consumption, and slow/broken consumers cannot authorize duplicate execution.

## Storage
Owner-ID directories index process scopes directly, so session recovery does not scan unrelated process history. Worker Git inspection/preparation/cleanup uses these scopes, and workspace rollback requires process reconciliation first. Private atomic ownership journals record version, execution owner, supervisor/process start identity, boot/kernel ownership identity, start-barrier state and reconciled completion. They contain no prompt, command/environment, upstream key, proxy token, or raw output. Keep incomplete journals for recovery. Completed scope removal is explicit and cannot delete session workspaces.

## Logging
Log execution/owner identifiers, stable lifecycle states and typed safe failure causes only. Raw native stderr and command/environment dumps are excluded.

Native executable launch failures distinguish missing files/interpreters, permission denial, incompatible executable formats and unavailable native resources through closed classifications. The private supervisor forwards only that classification, never an OS error string or command path; launch failure still requires the normal ownership reconciliation before dependent runtime cleanup.

## Build and Test
Package race tests and vet; real temporary subprocesses must cover separate streams, stdin, partial bytes, natural exit, cancellation, daemonized descendants, start barriers, missing journals, unrelated processes, parent disconnect and crash recovery. Windows/Linux cross-compilation is not native lifecycle evidence. Native harness capability tests are separate from process-fixture tests.

## Dependencies and Integrations
Platform ownership primitives follow [Linux subreaper semantics](https://man7.org/linux/man-pages/man2/PR_SET_CHILD_SUBREAPER.2const.html) and [Windows Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects) and [atomic Job List startup attributes](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-updateprocthreadattribute). The initial Unix ownership implementation adapts the repository's existing async-commit-hook native scope mechanism; DeliDev adds execution ownership and distinct interactive streams without coupling either product's configuration or lifecycle state.

## Change Triggers
Update this contract and scoped AGENTS when process ownership, proof of termination, journal contents or supported-platform behavior changes. Preserve uncertainty whenever native proof becomes unavailable.

Preparation recovery uses `ReconcileOwnerContext` to stop between bounded native ownership checks when its Worker deadline/cancellation fires. A check already terminating owned work finishes its confirmation before returning. Cancellation is not completion proof; partial reconciliation retains workspace recovery and blocks replacement preparation.
