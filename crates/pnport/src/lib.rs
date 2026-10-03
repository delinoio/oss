//! Private pnport interface retained for the CLI and existing integration
//! tests.
#[cfg(unix)]
pub use pnport_core::native_path;
pub use pnport_core::{cache, diagnostic, executable, graph, view};
