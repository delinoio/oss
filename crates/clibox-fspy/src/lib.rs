//! Internal file-access workflows for clibox.
//!
//! A path-access hint from the legacy fspy fork is not an operation result.
//! Record consumers must validate the complete operation stream before
//! analysis.

pub mod cli;
pub mod coverage;
pub mod record;

#[cfg(any(target_os = "linux", target_os = "macos"))]
pub mod watch;

#[cfg(target_os = "linux")]
pub mod repro;

#[cfg(target_os = "linux")]
pub mod linux;

#[cfg(target_os = "macos")]
pub mod macos;

#[cfg(target_os = "windows")]
pub mod windows;
