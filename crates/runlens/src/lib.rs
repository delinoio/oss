#![cfg_attr(windows, feature(windows_by_handle))]

pub mod analysis;
pub mod clean;
pub mod config;
mod declarations;
pub mod entries;
pub mod error;
pub mod execute;
// One bounded parser serves both preflight and nested macOS exec interception.
#[path = "../vendor/fspy_shared/src/macho.rs"]
pub mod macho;
pub mod model;
pub mod platform;
pub mod privacy;
pub mod report;
pub mod snapshot;
pub mod temporary;
