// SPDX-License-Identifier: Apache-2.0
//! CacheCow (`ccow`): independent copy-on-write build caches for Git worktrees.
//!
//! The binary entry point is `src/main.rs`. Every module keeps the behavior
//! contracts in `docs/crates-cachecow-foundation.md` and the complete
//! requirements in `docs/crates-cachecow-requirements.md`.

pub mod cli;
pub mod clone;
pub mod context;
pub mod error;
pub mod ipc;
pub mod jsonx;
pub mod lease;
pub mod lifecycle;
pub mod paths;
pub mod preset;
pub mod process;
pub mod repository;
pub mod session;
pub mod shell;
pub mod shim;
pub mod signals;
pub mod state;
#[cfg(windows)]
pub mod winsec;

pub use context::Context;

/// Version reported by `ccow --version`. Release builds set `CCOW_VERSION`.
pub const VERSION: &str = match option_env!("CCOW_VERSION") {
    Some(v) => v,
    None => env!("CARGO_PKG_VERSION"),
};
