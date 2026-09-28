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
pub use transform_error::Code as PublicationErrorCode;

/// Publish file bytes through the same native replacement path as transform
/// commands.
pub fn publish_file_bytes(
    destination: &std::path::Path,
    replace: bool,
    bytes: &[u8],
) -> Result<(), PublicationErrorCode> {
    use std::io::Write;

    let (publication, file) =
        publication::Publication::prepare(Some(destination.to_path_buf()), replace)
            .map_err(|error| error.code)?;
    let mut file = file.ok_or(PublicationErrorCode::WriteFailed)?;
    file.write_all(bytes)
        .map_err(|_| PublicationErrorCode::WriteFailed)?;
    drop(file);
    publication.publish(|| Ok(())).map_err(|error| error.code)
}

/// Execute one transformation and report only sanitized failures.
pub fn execute(command: Command) -> u8 {
    let operation = command.operation();
    match transform::execute(command) {
        Ok(status) => status,
        Err(error) => {
            transform_error::report(error, operation);
            error.exit
        }
    }
}

/// Report a process panic without exposing its payload or location.
pub fn report_runtime_failure() {
    transform_error::report(
        transform_error::Error::runtime(transform_error::Code::Runtime),
        "runtime",
    );
}
