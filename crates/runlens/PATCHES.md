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
