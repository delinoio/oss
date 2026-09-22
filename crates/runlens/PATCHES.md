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

Linux seccomp now registers the six extended-attribute read/list syscalls and resolves named files or descriptors as READ attempts. Metadata dependency reads previously escaped both preload interception and snapshots for external files. Dynamic/static native fixtures cover present/missing paths and write-only descriptor variants with read-denial policies and attribute-value canaries. Remove when upstream supplies equivalent read/list coverage without persisting attribute content.

Unix preload session/group hooks and posix_spawn attribute checks mark lifecycle coverage lost before detach-capable operations. Linux seccomp additionally observes raw setsid/setpgid calls. The group-only wait cannot prove completion of escaped descendants; unsupported detachment therefore never yields complete verification. Native macOS/Linux fixtures and static Linux fork fixtures exercise both session and group changes and delayed external descriptor writes. Remove when upstream binds the complete descendant lifecycle independently of group changes.

Windows NtCreateUserProcess consults the existing thread-local CreateProcess guard before forwarding. Direct calls previously observed only the image while launching an uninstrumented child; they now mark collection incomplete. Guard tests cover nested wrapper calls and concurrent thread isolation, and a real NT child fixture checks external writes and the offline policy result on Windows CI. Remove when upstream injects at the native boundary or implements equivalent transaction-aware loss reporting.

Linux seccomp registers statfs/fstatfs as reads to close metadata input gaps for raw syscalls and libc statvfs wrappers. Native dynamic/static external-path and write-only-descriptor fixtures assert deny-read failures, including missing named files. Remove when upstream covers filesystem-statistics inputs with the same attempted-access semantics.

Linux libc execveat forwards its original descriptor, pathname, environment and flags to the kernel collector. Converting it to execve discarded no-follow/empty-path/invalid-flag semantics. Native direct-versus-traced tests cover ELOOP, EINVAL, relative execution and descriptor execution. Remove only when upstream adaptation preserves every execveat operand and these tests pass.

Unix PATH exec hooks preserve the libc ENOEXEC shell fallback for executable text without a shebang. The shell goes through ordinary image preparation, so protected shells remain incomplete on macOS without aborting execution. Native execvp/execlp and Linux execvpe tests compare PATH/relative/missing cases, argv and exit status with direct execution. Remove when upstream supports libc fallback with identical collection-loss semantics.

Unix variadic exec adapters allocate checked argv storage without an artificial 4096-pointer ceiling; the kernel validates its actual ARG_MAX strings/environment budget. Allocation cleanup preserves exec errno. A compiled native C fixture passes 4099 arguments through execl/execlp/execle and checks genuine E2BIG against direct execution on macOS/Linux. Remove when upstream preserves these supported argument counts and failure semantics.

Windows Detours DLL names now require an exact UTF-16 → active-code-page → UTF-16 round trip, including best-fit mappings. If needed, try the OS-provided short pathname and apply the same check; without a representable alias return Unsupported before creating a child. Native Windows CI tests ASCII, representable accented paths, unrepresentable/best-fit names and UTF-8 ACP behavior. Remove when upstream uses Unicode injection or supplies equivalent exact identity validation.

Windows namespace normalization translates NT/verbatim UNC roots to ordinary \\server\share roots instead of stripping them into relative UNC paths. The internal prefix API carries owned normalized paths where needed. Native Windows IPC tests retain absolute external identity and strip an equivalent UNC base without accepting a local-worktree base. Remove when upstream handles UNC normalization through both external conversion and prefix comparison.

Windows spawn removes the unused duplicated process handle after ResumeThread. Job assignment already happens while suspended and the wait task owns the original handle; a late DuplicateHandle error could falsely claim the command never started. Existing native receipt, initialization-failure and timeout/lingering-child regressions cover the ownership boundary. Remove this patch only if upstream keeps all fallible setup before resumption and retains job-wide reaping.

The pre-resume DetourCopyPayloadToProcess call retains the original suspended
child's process handle as its first argument. Removing the unused post-resume
duplicate must not remove this required injection input. Both Windows native CI
builds type-check the four-argument ABI before the receipt tests exercise payload
delivery. Remove this repair only with an upstream spawn implementation that
preserves the same suspended-child payload and ownership sequence.

Unix chdir/fchdir interception and Linux seccomp record directory metadata read
attempts before the working directory changes. The syscall's result is preserved;
unresolvable kernel operands remain incomplete. Native libc and static raw-syscall
fixtures cover absolute/relative names, missing paths, and descriptors whose
directory was renamed after opening, proving fchdir's current-path read independently
of the original open. Remove when upstream observes both families before mutation
with equivalent read-boundary behavior on both architectures.

macOS registers a dyld image callback after client attachment. It records existing and future loaded-library paths using thread-safe dladdr, skipping the owned tracer and separately bound main executable. Only images marked MH_DYLIB_IN_CACHE and confirmed by the active cache avoid loader-loss classification; all loose/custom images make collection incomplete because prior loader reads/initializers are unbound. Bounded path lookup failures also mark loss. A native C executable linked to an external dylib runs against two changed library implementations, preserves both outputs, and fails the read policy without claiming complete collection. Remove when upstream binds pre-attachment loader dependencies and initialization accesses with equivalent coverage.

Linux seccomp registers io_uring_setup, io_uring_enter, and io_uring_register as unsupported collection without changing the syscall. Ring operations bypass ordinary file notifications, and SQPOLL need not enter again. Native dynamic/static fixtures preserve setup/SQPOLL success or errno and independent enter/register failures while forbidding verification success. Remove only when upstream observes all ring filesystem operations and their asynchronous lifetimes, including SQPOLL and registered rings.

Linux seccomp observes inotify_add_watch as a read attempt on its path before forwarding, including failed registrations. Watches previously allowed external dependencies to bypass literal read policies. Dynamic/static native fixtures cover files, directories and missing paths without retaining event or file contents. Remove when upstream supplies equivalent watch-path observation.

Windows file-object interception copies OBJECT_ATTRIBUTES, nested UNICODE_STRING,
and bounded UTF-16 bytes with ReadProcessMemory before inspecting them. Invalid
addresses, lengths, or embedded NULs mark collection incomplete and preserve the
original NT call. The native malformed-file-attributes regression compares exact
NT statuses with an untraced process and verifies child continuation; the preload
unit test covers each pointer layer and malformed lengths. Remove this local
patch only when upstream provides equivalent bounded, non-dereferencing handling.

Linux static exec removes only the tracer-owned LD_PRELOAD entry, preserving
caller preload bytes and dynamic-descendant loader behavior. The regression
compares static environment output and a real caller-library symbol in a dynamic
descendant against untraced execution. Removal: upstream must preserve caller
preloads across static exec with equivalent unit and native coverage.

Windows NtDeleteFile records a write attempt before forwarding the original
object attributes, including missing external paths. The native regression
compares NT results and deletion behavior with untraced execution and requires
a deny-write violation. Remove this local interception patch when upstream
provides equivalent native deletion coverage with safe attribute copying.

macOS readlink/readlinkat interception records attempted reads of the link path
before forwarding, including missing and directory-relative paths. It never
records returned link text as another input. Native fixtures compare byte counts,
text and errors with untraced execution and enforce external read policies.
Remove when upstream supplies equivalent macOS link-read coverage. Linux keeps
its existing seccomp hooks to avoid recursive descriptor-path resolution.

macOS extended-attribute value/list read hooks use the Darwin ssize_t ABI and preserve position/options and caller buffers. Native macos_xattr_reads_preserve_native_results_and_deny_external_inputs compares direct/traced success, errors and size probes and checks external read denial plus metadata canaries. Remove these hooks when upstream provides equivalent macOS coverage without retaining attribute names or values.

The Linux seccomp argument decoder adds the five-operand tuple needed by fanotify_mark on both supported 64-bit ABIs. The fanotify_mark handler records watched objects as read attempts, handles dirfd/NULL path semantics, and skips path-independent FAN_MARK_FLUSH. Dynamic/static native fixtures compare untraced results and require external deny-read failures for files, directories and missing targets, including hosts denying fanotify group creation. Remove when upstream provides equivalent bounded notification-registration path coverage.

The macOS execvP interposer supplies the caller search-path bytes to the existing exec adapter, preserving Darwin argv pointer ABI, PATH-independent selection, shell fallback and protected-image coverage loss. macos_execvp_custom_search_tracks_children_and_protected_fallbacks compares direct and traced native, missing, protected and text-file children. Remove when upstream intercepts execvP with equivalent custom-path and coverage semantics.

Relative Windows OBJECT_ATTRIBUTES conversion now reports global collection loss when GetFinalPathNameByHandleW cannot resolve its root. The native unresolved_windows_relative_roots_preserve_child_and_fail_closed fixture compares unchanged NT errors/continuation using a non-directory handle and requires incomplete policy evidence. Remove when upstream propagates all relative-root resolution failures without suppressing the original operation.

Linux seccomp observes name_to_handle_at through the shared path/descriptor resolver. Dynamic and static native fixtures compare failed/sizing results on file, directory and missing targets and enforce deny-read policy. Remove when upstream supplies equivalent file-handle lookup evidence without retaining returned handles or mount IDs.

The macOS posix_spawn_file_actions_addopen interposer reports coverage loss before constructing the action, preserving all operands and native results. This conservative classification also covers unused actions without degrading ordinary pipe close/dup actions. macos_spawn_open_actions_preserve_native_io_and_fail_closed covers both spawn variants, successful descriptor consumption, failed opens, and body canaries; the existing child receipt test preserves ordinary child coverage. Remove when upstream observes pre-injection spawn opens at execution time with equivalent coverage.

macOS syscall() gains architecture-specific argument-preserving tail-call adapters that report unsupported collection without decoding or retaining raw arguments. fork() reports loss before creation because the child can detach via inline kernel operations. Native macos_raw_syscalls_preserve_variadic_operands_results_and_errno exercises zero/one/six operands and errno; detached_descendants_cannot_certify_a_complete_lifecycle includes libc raw session/group changes and fork plus inline setsid. Remove when upstream supplies inherited kernel lifecycle/operation observation without requiring these conservative markers.

Darwin attribute-list interception covers getattrlist/getattrlistat/fgetattrlist as reads and getattrlistbulk as an incomplete directory read. Attribute lists and returned buffers remain untouched. macos_attrlist_reads_preserve_results_and_enforce_external_boundaries compares native results and failed calls across all four functions and forbids external read-policy success. Remove when upstream covers these APIs and can bind every bulk child metadata dependency.

Darwin clonefile, clonefileat and fclonefileat interposers retain source READ and destination WRITE before forwarding all flags and operands. macos_clones_preserve_native_results_and_record_both_boundaries covers successful and failed pathname/relative/descriptor clones and both literal policies. Remove when upstream supplies equivalent clone observation without capturing file bodies.

Unix PathAt now retains raw scalar operands until bounded OS-assisted copying into owned storage; ordinary pathname hooks no longer construct borrowed C strings from caller memory. Stream modes use the same checked copy, and observation restores errno. Linux uses a raw self process_vm_readv syscall to avoid preload recursion; macOS uses Mach read-overwrite. unreadable_unix_pathnames_preserve_native_errors_without_faulting compares open/openat/stat/access for null, invalid, protected, unterminated, long and valid page-boundary strings. Remove when upstream provides equivalent fault-safe operand copying and native result preservation.

Linux namespace handlers reject complete path identity for classic/new mount APIs, namespace joins/unsharing and root changes; clone/clone3 inspect only namespace flags, preserving ordinary threads. The kernel still receives every call unchanged. linux_namespace_and_root_changes_cannot_certify_path_identity compares 15 operations for dynamic/static callers; linux_private_mount_remapping_retains_incomplete_evidence checks real bind remapping when enabled with RUNLENS_REQUIRE_NAMESPACE_REMAP. Remove when upstream binds path evidence to each caller mount/root namespace and complete managed lifetime.

Descriptor fstat: retain metadata reads separately from write-only opens in Linux seccomp and macOS preload. Native dynamic/static fixtures compare successful metadata, EBADF, EFAULT and anonymous-pipe behavior and enforce external read denial without persisting body bytes. Remove when upstream provides equivalent coverage and descriptor classification.

Opaque Linux handle opens: register open_by_handle_at and report collection loss before forwarding, including invalid/denied requests. Dynamic/static read/write/error fixtures preserve results and file bytes while policy stays inconclusive. Remove when upstream binds returned objects and modes to sound path evidence.

Darwin filesystem statistics: interpose statfs/fstatfs with libc-selected inode ABI and statvfs/fstatvfs with their native structures. Observe only paths/descriptors and preserve results; valid/missing and write-only-descriptor fixtures enforce metadata input policy. Remove when upstream covers these functions with equivalent native ABI and privacy tests.

Windows Job liveness: replace accounting counts with bounded membership/handle liveness checks and a confirming snapshot after an empty scan. ActiveProcesses can count terminated objects with retained references (Microsoft JOBOBJECT_BASIC_ACCOUNTING_INFORMATION contract), causing spurious incomplete Git/target lifetimes. Native Windows regression keeps exited process handles alive across ownership completion; existing timeout/lingering tests preserve real cleanup. Remove when upstream supplies equivalent race-aware liveness and fail-closed query handling.

Windows descriptor metadata: interpose NtQueryInformationFile as READ independently of creation access flags; unresolved file handles fail closed, caller buffers stay untouched, and collector path resolution suppresses its own recursive queries. The native windows_handle_metadata_reads_preserve_results_and_policy_evidence fixture covers size/attributes, invalid buffers/handles and anonymous pipes. Remove when upstream covers handle metadata with equivalent recursion, native-result and privacy guarantees.

Ancestor identity: add private PATH_MUTATION evidence for Unix rename/removal and Windows handle/path deletion or information mutation attempts, separately from ordinary writes/mkdir. The redacted bounded execution index invalidates descendant scope even after a directory is restored. Native restored_symlink_ancestors_cannot_certify_workspace_only_reads exercises dynamic and Linux static rename/symlink/read/restore, while stable_directory_ancestors_preserve_missing_leaf_and_output_scope protects ordinary output creation. Remove when upstream binds every observation to ordered resolved ancestor identities with equivalent restoration coverage.

Windows EA queries: interpose NtQueryEaFile through the recursion-guarded handle-read observer, preserving every operand and native result. The windows_handle_metadata_reads_preserve_results_and_policy_evidence regression now includes EA success/error/pipe cases. Remove when upstream supplies equivalent EA observation and privacy guarantees.

Darwin symlink flags: add lchflags to pathname mutation interposition, preserving the lexical link identity and native operands. macos_lchflags_preserves_results_and_observes_the_link checks successful/missing paths, target flag preservation and policy denial. Remove when upstream covers no-follow flag mutations equivalently.

Linux deleted descriptor names: reject the procfs ` (deleted)` suffix as ESTALE so collection retains uncertainty without rewriting an ambiguous identity. deleted_descriptor_identity_cannot_pass_exact_read_policy covers unlinked files and real suffix-bearing names with dynamic/static fstat. Remove when upstream binds descriptors to trustworthy identities across unlink and rename.

Linux creat: register x86_64 syscall 85 through the existing open classifier with O_CREAT | O_WRONLY | O_TRUNC. linux_creat_records_external_creation_truncation_and_failure covers dynamic and static callers, using openat on arm64 where creat has no syscall number. Remove when upstream covers creat with equivalent attempted-write semantics.

Linux socket nodes: register bind and decode bounded AF_UNIX pathname addresses before forwarding; ignore non-filesystem families/abstract/autobind names and fail closed on unreadable operands. linux_socket_binds_retain_path_writes_and_preserve_native_results covers dynamic/static absolute, relative, failed and abstract calls. Remove when upstream has equivalent socket-node mutation coverage.

Darwin attribute mutation: interpose setattrlist/setattrlistat/fsetattrlist as writes with PATH_MUTATION because attribute buffers remain opaque. macos_attribute_mutations_preserve_native_results_and_write_boundaries tests native success/errors, permissions and privacy. Remove when upstream provides equivalent mutation and identity coverage.

No-follow input identity: add READ_NOFOLLOW as an independent private bit, preserve it across Linux/preload readlink/lstat/flagged stat and O_NOFOLLOW opens, and intersect scope for every merged observation. nofollow_symlink_reads_keep_leaf_identity_without_certifying_following_accesses covers dynamic/static reads, empty-path Linux descriptors, stat variants, link ancestors and mixed reads. Remove when upstream preserves equivalent leaf-resolution evidence through aggregation.

Windows non-filesystem handles: share kernel type classification across metadata reads, relative NT roots and information mutations. Confirmed pipe/character handles require no DOS path; unknown handles still fail closed, and type lookup preserves last-error and suppresses its own queries. windows_handle_metadata_reads_preserve_results_and_policy_evidence now isolates pipe creation, relative operations and pipe-mode mutation from metadata queries. Remove when upstream provides equivalent non-filesystem classification at every observation boundary.
