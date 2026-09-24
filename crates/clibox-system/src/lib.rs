//! Internal OS command implementation for the clibox executable.

mod clipboard;
mod cpus;
mod environment;
mod error;
mod open;
mod port;
mod run;
mod runtime;
mod system;

pub use system::Action as Command;

/// Execute one OS command and preserve its native child/signal exit behavior.
pub fn execute(command: Command, leading_separator: bool, raw: &[std::ffi::OsString]) -> ! {
    let operation = command.operation();
    let result = (|| {
        #[cfg(unix)]
        runtime::configure_installed_terminal_interrupt_acknowledgement()?;
        runtime::install_signals()?;
        tracing::debug!(operation, "operation_started");
        system::execute(command, leading_separator, raw)
    })();
    let status = match result {
        Ok(status) => status,
        Err(error) => {
            error.report(operation);
            if error.code == error::Code::InvalidInput {
                2
            } else {
                1
            }
        }
    };
    runtime::finish(status);
}
