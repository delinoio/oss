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
