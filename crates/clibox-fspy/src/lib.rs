//! Internal, versioned file-access trace model and validation.
//!
//! The record reader is deliberately strict: analysis must never mistake a
//! truncated or unsupported trace for a complete execution.

mod assetcov;
mod autowatch;
pub mod cli;
mod fbreak;
mod latencylab;
mod minrepro;
mod output;
mod selector;
#[cfg(target_os = "linux")]
mod snapshot;
pub mod trace;

#[cfg(target_os = "linux")]
pub mod linux;
