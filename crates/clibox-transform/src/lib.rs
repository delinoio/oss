//! Internal offline command implementation for the clibox executable.

mod base64;
mod cli;
mod hash;
mod io;
mod publication;
mod text;
mod time;
mod transform;
mod transform_error;

pub use cli::TransformCommand as Command;

/// Execute one transformation and report only sanitized failures.
pub fn execute(command: Command) -> u8 {
    match transform::execute(command) {
        Ok(status) => status,
        Err(error) => {
            transform_error::report(error);
            error.exit
        }
    }
}

/// Report a process panic without exposing its payload or location.
pub fn report_runtime_failure() {
    transform_error::report(transform_error::Error::runtime(
        transform_error::Code::Runtime,
    ));
}
