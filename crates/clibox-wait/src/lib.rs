//! Internal readiness command implementation for the clibox executable.

mod cli;
mod probe;
mod wait;
mod wait_command;

pub use cli::Wait as Command;
pub use wait_command::execute;
