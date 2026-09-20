# Pinned tracing patches

Base: vite-task `13aa80a0dac698023ce68ba16497b16e5330600b`, MIT (see UPSTREAM-LICENSE). Detours `9764cebcb1a75940e68fa83d6730ffaf0f669401`, with its original license. `vendor-provenance.json` records included runtime crates and original file hashes. `scripts/vendor.py` imports into an empty directory only; it never overwrites patches.

| Patch | Reason and scope | Validation | Removal condition |
| --- | --- | --- | --- |
| Workspace closure | Inherit only the required fspy runtime closure, omit upstream application/test harness dependencies, and keep artifact dependencies in an independent nightly workspace. No root dependencies change. | Independent Cargo checks, locked builds, root dependency regression | Upstream offers a standalone distributable engine compatible with the repository root toolchain. |
| No substitution | Remove macOS Oils/coreutils materialization and download build script; protected child execution is observed as unsupported and proceeds unchanged. | Protected executable, nested child and ordinary native fixtures | Upstream exposes equivalent no-substitution behavior and unsupported observations. |
| Fallible session initialization | Replace the global shared temporary directory and panic initializer with explicit per-execution initialization. | Initialization failure and private cleanup fixtures | Upstream offers a fallible owned session API. |
| Lifecycle ownership | Hold collection through process-group/job completion; assign Windows jobs before resume; enforce cancellation/lingering-child grace and reaping. | Native timeout, cancellation, lingering and child fixtures | Upstream enforces the same finite-command lifecycle and failure distinctions. |
| Bounded evidence | Carry a per-session byte/path capacity and dynamic slot count in private IPC; collect static Linux accesses into that same sparse disk-backed channel instead of unbounded arenas. Retain committed frames with an explicit incomplete flag after overflow. | Overflow, large-access, spill and static Linux fixtures | Upstream provides equivalent budgets and partial evidence without allocation after exhaustion. |
| Attachment evidence | Private ATTACHED and UNSUPPORTED mode bits report injection readiness and observed unsupported children; public reports omit attachment markers. | Real native read/write fixtures and unsupported target rejection | Upstream provides equivalent typed readiness/coverage signals. |
| External path extraction | Expose an owned path using upstream Windows namespace normalization, retaining observations outside snapshot scope. | Path, external access and Windows fixtures | Upstream provides an equivalent all-path API. |
| No unused environment marker | Remove `FSPY=1` injection from Unix and Windows launchers. The runtime has no consumer; it changes commands that inspect the variable and overwrites explicitly selected values. Required tracing payload/loader coordination remains unchanged. | Native `tracing_preserves_unset_and_selected_fspy_values_in_children` checks ordinary/traced, nested, and clean execution | Upstream stops injecting the unused marker and preserves caller state. |

These changes do not add event timelines, syscall-success claims, a PTY, a service manager, privileged tracing, or a security sandbox. A source build is not platform certification; release validation consumes actual native evidence for each exact artifact.

The collection-loss close bit preserves the committed frame count with an atomic
OR. Replacing the count with only the close flag loses partial evidence when the
bounded buffer fills. `cache_policy_and_overflow_fail_closed` in
`tests/native.rs` covers continued child execution, retained accesses, and a failed
verification outcome. Remove this patch only when the upstream reader/writer
protocol preserves committed records on collection loss.

The independent Windows Cargo target enables Tokio's pinned `tokio_unstable`
configuration so `spawn_with` can assign the suspended primary process to its Job
before injection and thread resumption. Both tracing and the uninstrumented Git
metadata/source-preparation children share the owned lifecycle implementation.
Native Windows CI compiles this boundary and integration tests exercise clean,
repeat, timeout, and lingering-child reaping. Remove the cfg only after the pinned
Tokio release provides this callback through a stable API with the same ordering.

Windows child creation calls the original CreateProcess function with a temporary
suspended flag, copies only the tracing payload, injects directly, and resumes
the original successful child even when optional injection fails. It never
launches a helper or replacement executable. Injection and DLL hook initialization
failures set the typed unsupported marker rather than aborting the host process.
Failed OS thread resumption reaps the suspended child before returning failure.
Native Windows nested-child and timeout fixtures exercise this boundary. Remove
this patch when upstream preserves successful child execution, reports collection
loss, and owns suspended handles under the same constraints.

Detours transaction APIs return zero on success (LONG), unlike BOOL APIs. DLL
detach uses the LONG checker so successful teardown does not emit a false
unsupported marker. Native Windows finite-command tests require complete
collection after process exit. Remove this fix only when upstream uses the
documented transaction result convention in every detach call.

The shared bounded Mach-O parser checks restrictive signatures for both root
preflight and intercepted macOS child execution. A hardened descendant continues
unchanged and emits unsupported coverage, even outside SIP system directories.
The native hardened-image fixture checks both preflight rejection and successful
child execution with an incomplete receipt. Remove this patch when upstream
provides equivalent passive protection classification without executable substitution.

Unix collector initialization and path/exec adaptation failures now preserve the
host call and set typed incomplete evidence when the channel is available. Native
removed-cwd and invalid-payload-child fixtures verify continued execution; the
root supervisor rejects missing attachment. Variadic execl allocates its trailing
NULL slot and exposes only initialized pointers; a 35-argument native fixture
checks this boundary. Unsupported macOS descendants lose only Runlens's inherited
DYLD entry, retaining user preloads: an ordinary arm64 dylib cannot load in an
arm64e system child on macOS 14. Native protected-child tests cover continued
execution on both macOS architectures. Remove these patches when upstream provides
fallible initialization, non-aborting collection, correct argv allocation, and
owned-preload removal at the unsupported-child boundary.

Windows payload propagation retains and validates the DLL path's trailing NUL
before constructing a native C string. Native nested-child fixtures cover payload
propagation. Remove this fix when upstream serializes and validates the complete
NUL-terminated slice rather than relying on bytes after its allocation.

Unix rename interception records both endpoints as write attempts before forwarding
unchanged. macOS covers rename, renameat, renamex_np and renameatx_np; Linux covers
libc rename/renameat/renameat2 and all corresponding raw syscalls through seccomp,
including static children and directory-relative destinations. Native successful
and failed rename fixtures verify both paths, actual deletion versus mere attempts,
and external deny-write policy failures. Linux CI repeats these cases with the
static fixture. Remove this patch when upstream records both rename endpoints and
marks unresolved arguments as incomplete while preserving the child operation.

Unix path removal hooks cover unlink, unlinkat (including AT_REMOVEDIR), rmdir,
and the libc remove wrapper. Linux seccomp covers the corresponding raw syscalls
for static children. Paths are write attempts even on failure; resolution failures
remain incomplete without cancelling the syscall. Native successful/missing file
and directory fixtures exercise external deny-write rules, absolute and dirfd
paths, and absence of invented external snapshot changes. Remove this patch when
upstream covers this removal family with the same failure and evidence semantics.

Unix libc and Linux seccomp share open-mode classification. O_CREAT/O_TRUNC and
Linux O_TMPFILE add write attempts independently of O_ACCMODE; stdio '+' adds
both read and write for r+/w+/a+ (including binary-mode spellings). Native external
create/truncate/update-mode fixtures and static Linux openat fixtures enforce
write boundaries, with read-only controls. Remove this patch when upstream shares
correct creation/truncation/update-mode semantics across both collection paths.

The Windows LONG checker compares the actual Detours return value with NO_ERROR,
not a constant or thread-local last error. Setup failures therefore reach the
existing transaction-abort and UNSUPPORTED marker path while the child continues.
The Windows native workflow explicitly runs the checker regression with success,
access/handle failures, and a real rejected DetourAttach call. Remove this patch
when upstream preserves LONG error codes at all setup and teardown boundaries.
The Windows preload test target retains its `tempfile` development dependency:
even a filtered Detours test compiles the existing pathname helper fixtures.
Remove this dependency only when those fixtures no longer require temporary files.

Windows NtCreateFile classification includes creation, overwrite and supersede
dispositions independently of DesiredAccess. Both create and open hooks include
FILE_DELETE_ON_CLOSE. Unknown dispositions report unsupported evidence. Portable
shared classification tests cover every disposition with read-only access and
the non-mutating FILE_OPEN control. Remove this patch when upstream preserves
these mutation attempts in its native open hooks.

Windows NtSetInformationFile now records source-handle write attempts before
disposition, allocation, EOF or metadata updates. Delete and metadata access
rights also classify as writes. Renames/links and unknown information classes
mark collection incomplete because their additional destinations are not yet
decoded; failed handle resolution does the same. Handle-local position and I/O
settings are excluded. The bypass-access-check rename/link classes (56/57) remain
unresolved too; I/O completion notification is the separate class 41. Portable
classification regressions and Windows native
rename/delete policy fixtures cover this conservative boundary. Remove the patch
when upstream traces both endpoints and all supported mutation classes without
turning missing evidence into a pass or interrupting the original operation.

Unix path/descriptor mutation hooks now cover mkdir/mknod/mkfifo, hard links,
symlinks, chmod/chown, truncation, timestamps, extended attributes and macOS file
flags. Linux seccomp observes the corresponding raw syscalls, including static
children and directory-relative variants. Hard links record both endpoints;
symlink target text is not an input read. Failed calls remain attempts, and lost
argument resolution remains incomplete. Native external-boundary fixtures cover
creation, links and metadata success/failure, with static Linux counterparts.
Explicit directory creation attempts still require the directory in write
allowlists; the snapshot-only ancestor exception does not authorize an attempt.
Remove this patch when upstream covers these mutation families with equivalent
bounded evidence and continued child execution on collection errors.

Native image identity is bound across launch. Linux executes the retained inspected
file descriptor, preserving argv[0]; Windows holds the canonical executable against
write/delete and real ancestor directories against rename until CreateProcess
returns. macOS reports the main mapped image's vnode through PROC_PIDREGIONPATHINFO
in the preload constructor before main. Its private IMAGE marker is scoped to the
root PID and never serialized as an access. Missing/conflicting image evidence or
changed retained metadata clears the digest and makes collection incomplete.
`launched_image_identity_survives_or_detects_path_replacement` replaces the path
after the last preflight and checks native execution and identity on each platform.
Remove these patches when upstream supplies an equivalent launch-bound identity.

Linux dynamic images install the same inherited seccomp notification filter as
static images. Preload alone misses inline kernel calls. The native dynamic
inline-unlinkat fixture verifies external write-policy denial on both Linux
architectures, including failed attempts. Remove this patch when upstream covers
direct syscalls in all supported images independently of libc interposition.

The generic libc syscall varargs interposer is removed: callers legally pass
zero to six arguments, so unconditionally extracting six is undefined behavior.
Kernel seccomp collection covers statx and direct filesystem syscalls without
reading C varargs. Native Linux zero/one/three/five-argument fixtures verify return
values and read observations. Restore an interposer only if upstream can prove
ABI-safe forwarding for both supported Linux architectures without missing calls.

Windows process-image attribute parsing uses bounded ReadProcessMemory copies
instead of references into caller memory. It validates list alignment, complete
entry count, checked offsets, duplicate image attributes, UTF-16 byte alignment,
and a 65,534-byte image bound. Invalid pointers or metadata mark collection
incomplete while forwarding the original NT call. Native Windows CI exercises
valid lists, short/overflow lengths, malformed image sizes and inaccessible
pointers. Remove this patch only when upstream safely handles invalid user memory
without preempting the NT syscall's own error handling.

The preload's Linux libc shim explicitly declares glibc futimesat using the
public time/sys/time.h signature. Pinned libc 0.2.185 omits that binding, which
otherwise breaks both Linux native builds. A native libc futimesat mutation
fixture verifies write attempts. Remove the local declaration when the pinned
libc dependency exports the same supported 64-bit GNU interface.

## Linux readlink dependencies

- Reason/scope: inherited seccomp filters omitted `readlink` (x64) and `readlinkat` (both architectures), hiding dependencies on link text. Record read attempts on the named link, including empty-path O_PATH descriptors; never reinterpret returned target text as an access. Resolution errors keep the child syscall and mark evidence incomplete.
- Regression: `linux_readlink_attempts_cannot_bypass_read_denials` runs dynamic and static raw syscalls, missing paths, and empty-path descriptors and checks external read denial plus absence of invented target accesses.
- Removal: drop this local handler only when the pinned upstream observes both syscall families with equivalent failure/descriptor behavior and these native regressions pass.

## Isolated Windows environment path spelling

- Reason/scope: canonicalizing fresh HOME/cache paths yields Windows verbatim prefixes that Git rejects when used for global configuration. Preserve canonical location while passing ordinary drive/UNC spelling to child environment variables; this changes no selected source or credential isolation.
- Regression: `isolated_windows_environment_uses_git_compatible_paths` checks every selected path and asks native Git to read the owned empty global configuration; clean/repeat integration tests exercise HEAD and clone operations.
- Removal: retain normalized public path spelling until every supported preparation tool accepts verbatim environment paths and native Windows tests prove parity.

## Linux null metadata operands

- Reason/scope: kernel collection treated Rust/libc's `statx` availability probe (NULL without AT_EMPTY_PATH) as an unreadable filename and marked normal executions incomplete. NULL with AT_EMPTY_PATH instead identifies the descriptor on Linux 6.11+. Handle both statx/fstatat forms, recording the latter's FD path; all other unreadable arguments remain incomplete. Audited debug events report syscall number/nullness/errno only, never paths or buffers.
- Regression: `linux_null_empty_path_metadata_retains_directory_read_evidence`, fresh clean/repeat environments, nested execs, and real receipt tests run on native Linux. The fixture preserves the failing capability probe and both empty-string/NULL descriptor attempts.
- Removal: use upstream only after these exact kernel operand semantics and redacted diagnostics are supported at the pinned revision.

## Reuse inherited Linux seccomp listeners

- Reason/scope: every root image now installs kernel collection, so preload nested-exec preparation must not install another USER_NOTIF listener. Linux rejects another listener with EBUSY. Keep argv/environment preparation while reusing the filter inherited across fork, posix_spawn, and exec, including a dynamic-to-static transition.
- Regression: native receipt children, FSPY environment descendants, large variadic exec, dynamic inline-syscall and static-child fixtures on Linux; loss-injection cases still report incomplete.
- Removal: upstream must distinguish root installation from inherited collection and pass these tests before dropping the patch.

Linux exec notification handlers passively inspect two header bytes and mark shebang executions unsupported instead of omitting the kernel-opened interpreter chain. Path resolution is shared with open observations; nonregular/unreadable headers fail closed without blocking or cancelling execution. Native raw execve, relative execveat, and descriptor execveat fixtures run from dynamic and static binaries and verify script completion plus inconclusive/failed policy. Remove when upstream binds and records every kernel-selected interpreter without substituting execution.
