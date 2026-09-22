//! Internal configuration command implementation for the clibox executable.

mod config_command;
mod config_publication;
mod config_runtime;
mod dotenv;
mod yaml;

pub use config_command::{execute, Configuration as Command};

/// Report a process panic without exposing its payload or location.
pub fn report_runtime_failure() {
    config_runtime::Error::from(config_runtime::Failure::Internal).report("runtime");
}
