# fspy source provenance

- Source repository: https://github.com/voidzero-dev/vite-task
- Source revision: `3aac49e31fba6905bb0b3d0e29d7755493241e9c`
- Source paths: the `fspy*`, `materialized_artifact*`, `vt_path`, and `vt_str` crate directories copied into this workspace.
- Source license: MIT, copied verbatim into each imported crate's `LICENSE` file.
- Windows Detours source: https://github.com/microsoft/Detours at `9764cebcb1a75940e68fa83d6730ffaf0f669401`; its own MIT license files are retained in `crates/fspy_detours_sys/detours`.

The initial source import preserves the upstream crate trees, except that the Detours Git submodule is materialized as ordinary repository files and each crate receives a copy of the upstream MIT license. Follow-up local changes to build integration and macOS interception are tracked in this repository's Git history and documented in `docs/crates-fspy-vendor-contract.md`.

To update this fork, inspect a specific upstream commit, compare each imported crate and the Detours submodule revision, retain copyright and license notices, reapply documented local changes, then rerun the full Rust and pnport validation gates.
