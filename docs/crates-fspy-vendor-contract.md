# fspy source fork contract

## Source and ownership

This repository contains a source fork of [vite-task at `3aac49e31fba6905bb0b3d0e29d7755493241e9c`](https://github.com/voidzero-dev/vite-task/tree/3aac49e31fba6905bb0b3d0e29d7755493241e9c/crates/fspy). The imported Cargo workspace members are `fspy`, `fspy_shared`, `fspy_shared_unix`, `fspy_preload_unix`, `fspy_preload_windows`, `fspy_detours_sys`, `fspy_seccomp_unotify`, `fspy_client_unix`, `fspy_shm`, `fspy_nostd`, `fspy_nostd_alloc`, `fspy_ipc_str`, `materialized_artifact`, `materialized_artifact_macros`, `vt_path`, and `vt_str`. Every imported crate is a private workspace member with `publish = false`. Upstream test-only crates, benchmarks and E2E tools are outside this fork. Registry and external Git dependencies remain in `Cargo.lock`; they are not copied into `crates/`.

The VoidZero MIT text from the pinned revision is copied in full to each imported crate's `LICENSE`. `crates/fspy_detours_sys/detours` materializes the upstream Microsoft Detours submodule at [`9764cebcb1a75940e68fa83d6730ffaf0f669401`](https://github.com/microsoft/Detours/tree/9764cebcb1a75940e68fa83d6730ffaf0f669401), with its separate Microsoft MIT license retained as `LICENSE` and `LICENSE.md`. The source inventory and update procedure also appear in `crates/fspy/PROVENANCE.md`. The repository root Apache license does not replace either copyright notice.

## Local changes

The fork joins the root Cargo workspace, uses its pinned nightly and dependency graph, and removes upstream test-only targets and subprocess fixtures. The root formatter normalizes imported source, and one nested Linux assembly macro has a scoped rustfmt skip because the pinned formatter changes its indentation on successive passes. The macOS Oils/uutils download/build path and protected executable substitution are removed. A macOS executable that cannot accept injection fails explicitly; the fork must not report its trace as complete. The pinned nightly requires the preload crate's `c_variadic` feature and `VaList::arg` calls.

`pnport-core` owns the graph, cache, virtual path and executable admission code shared by pnport and the preload. On macOS, the `pnport` feature of `fspy_preload_unix` compiles pnport's virtualizing hooks through fspy's Mach-O interpose entry layout. It preserves the pnport preload ABI marker, descriptor tracking, read-only checks, child propagation and constructor entry/ready/failure signals. The pnport supervisor retains exit, cancellation and input-watch behavior. Linux and Windows continue to use `pnport-preload` in this change; no new runtime claim is made for those platforms.

## Maintenance and validation

For an update, select an exact upstream commit, compare all imported trees and external Git revisions, update licenses and provenance, then reapply and review every local change. Verify the locked Cargo graph, formatting, Clippy and root `cargo test`. Build the macOS pnport-mode fspy preload before pnport fixture and TypeScript tests. Run Linux and Windows build checks on their native CI hosts. Inspect npm/native package inventories, ABI marker, installed license notices and the six-host release gate. No GitHub repository fork, crates.io publication or pnport release is part of this source fork.
