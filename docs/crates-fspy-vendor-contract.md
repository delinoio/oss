# fspy source fork contract

## Source and ownership

This repository contains a source fork of [vite-task at `3aac49e31fba6905bb0b3d0e29d7755493241e9c`](https://github.com/voidzero-dev/vite-task/tree/3aac49e31fba6905bb0b3d0e29d7755493241e9c/crates/fspy). The imported Cargo workspace members are `fspy`, `fspy_shared`, `fspy_shared_unix`, `fspy_preload_unix`, `fspy_preload_windows`, `fspy_detours_sys`, `fspy_seccomp_unotify`, `fspy_client_unix`, `fspy_shm`, `fspy_nostd`, `fspy_nostd_alloc`, `fspy_ipc_str`, `materialized_artifact`, `materialized_artifact_macros`, `vt_path`, and `vt_str`. Every imported crate is a private workspace member with `publish = false`. Upstream test-only crates, benchmarks and E2E tools are outside this fork. Registry and external Git dependencies remain in `Cargo.lock`; they are not copied into `crates/`.

The VoidZero MIT text from the pinned revision is copied in full to each imported crate's `LICENSE`. `crates/fspy_detours_sys/detours` materializes the upstream Microsoft Detours submodule at [`9764cebcb1a75940e68fa83d6730ffaf0f669401`](https://github.com/microsoft/Detours/tree/9764cebcb1a75940e68fa83d6730ffaf0f669401), with its separate Microsoft MIT license retained as `LICENSE` and `LICENSE.md`. The source inventory and update procedure also appear in `crates/fspy/PROVENANCE.md`. The repository root Apache license does not replace either copyright notice.

## Local changes

The fork joins the root Cargo workspace, uses its pinned nightly and dependency graph, and removes upstream test-only targets and subprocess fixtures. Workspace `anyhow` stays at 1.0.102 and `quote` uses wincode's minimum compatible 1.0.45 so the protected DevHud mobile dependency closure retains its existing hashes; no fspy or pnport crate enters that closure. The root formatter normalizes imported source, and one nested Linux assembly macro has a scoped rustfmt skip because the pinned formatter changes its indentation on successive passes. Upstream `disallowed_*` lint expectations depend on its `.clippy.toml`, which is not applied repository-wide here; scoped allowances in `vt_str`, `vt_path`, and `fspy_nostd` preserve the permitted interop without failing this repository's `-D warnings` gate. Restore those expectations if an equivalent lint table is adopted. The upstream Linux trampoline's `invalid_runtime_symbol_definitions` allowance is removed because that lint does not exist in the pinned nightly. The macOS Oils/uutils download/build path and protected executable substitution are removed. macOS injection admission resolves symlinks and parent path components before checking the protected executable list; failed resolution also prevents launch. A macOS executable that cannot accept injection fails explicitly; the fork must not report its trace as complete. The pinned nightly requires the preload crate's `c_variadic` feature and `VaList::arg` calls.

`pnport-core` owns the graph, cache, virtual path and executable admission code shared by pnport and the preload. On macOS, the `pnport` feature of `fspy_preload_unix` compiles pnport's virtualizing hooks through fspy's Mach-O interpose entry layout. It preserves the pnport preload ABI marker, descriptor tracking, read-only checks, child propagation and constructor entry/ready/failure signals. The pnport supervisor retains exit, cancellation and input-watch behavior. Linux and Windows continue to use `pnport-preload` in this change; no new runtime claim is made for those platforms.

Both macOS fspy and pnport command admission use `pnport-core` to validate the selected Mach-O slice, offline signature, and hardened-runtime entitlements before injection. The supervisor and pnport descendant `execve`/`posix_spawn` hooks launch the canonical path returned by admission, so a mutable path alias is not resolved a second time at launch. A signed image without the required DYLD-environment and disabled-library-validation entitlements is rejected before spawn.

The generic fspy runner creates a random, user-private preload directory for each process instead of using a fixed shared temporary path. The directory remains owned for the runner's lifetime. `materialized_artifact` checks the bytes of any pre-existing regular artifact without following Unix symlinks before returning its path; a collision with different bytes fails initialization.

On Windows, Detours requires an ANSI DLL path. The runner round-trips the materialized path through the active ANSI code page, then tries the same file's short path if the original loses characters. If neither path round-trips exactly, initialization fails before spawning a child.

Windows descendant hooks terminate and close a newly created suspended child when payload propagation or resumption fails inside the Detours callback; the original Win32 error remains available to the caller.

Windows preload attachment and detachment check the returned Detours `LONG` status from each transaction operation. A failed attach prevents DLL initialization from reporting a complete trace.

Windows access classification treats deletion, file metadata and extended-attribute mutation, directory-child deletion, and security-owner/DACL mutation rights as writes. Combined read/write masks remain both inputs and outputs; maximum-allowed access is classified conservatively.

On Linux, a seccomp notification whose access cannot be recorded still resumes the target syscall. The supervisor retains the recording error and fails collection after the target exits, so callers cannot treat the partial trace as complete.

The Linux preload hooks the fixed-arity libc `statx` entry point. It does not interpose libc's generic variadic `syscall` entry point: extracting a fixed six arguments from lower-arity calls is undefined behavior. A direct `syscall(SYS_statx, ...)` in a dynamically linked process is outside the preload trace; callers needing that access must use the seccomp path.

At root-process exit, the seccomp supervisor seals the trace and stops listener acceptance. Active handlers return their already collected accesses and recording errors without waiting for surviving descendants. Each existing notification listener continues to answer inherited-filter syscalls with `CONTINUE` until its final filtered task exits; those later accesses do not extend the sealed trace.

The Unix preload forwards an intercepted filesystem call after a path-resolution error and marks the shared channel incomplete first. The receiver rejects that channel at seal time instead of reporting a partial trace as complete.

## Maintenance and validation

For an update, select an exact upstream commit, compare all imported trees and external Git revisions, update licenses and provenance, then reapply and review every local change. Verify the locked Cargo graph, formatting, Clippy and root `cargo test`. Build the macOS pnport-mode fspy preload before pnport fixture and TypeScript tests. Run Linux and Windows build checks on their native CI hosts. Inspect npm/native package inventories, ABI marker, installed license notices and the six-host release gate. No GitHub repository fork, crates.io publication or pnport release is part of this source fork.
