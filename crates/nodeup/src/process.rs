use std::{
    ffi::OsString,
    path::Path,
    process::{Command, ExitStatus},
};

use tracing::info;

use crate::errors::{NodeupError, Result};

pub fn run_command(command_path: &Path, args: &[OsString], command_path_key: &str) -> Result<i32> {
    info!(
        command_path = command_path_key,
        executable = %command_path.display(),
        args_len = args.len(),
        "Spawning delegated process"
    );

    let status = Command::new(command_path)
        .args(args)
        .status()
        .map_err(|error| {
            NodeupError::not_found(format!(
                "Failed to execute {}: {error}",
                command_path.display()
            ))
        })?;

    let termination = status_details(status);

    info!(
        command_path = command_path_key,
        executable = %command_path.display(),
        exit_code = termination.exit_code,
        signal = ?termination.signal,
        "Delegated process finished"
    );

    Ok(termination.exit_code)
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct ProcessTermination {
    exit_code: i32,
    signal: Option<i32>,
}

fn status_details(status: ExitStatus) -> ProcessTermination {
    #[cfg(unix)]
    {
        use std::os::unix::process::ExitStatusExt;

        ProcessTermination {
            exit_code: status.code().unwrap_or(1),
            signal: status.signal(),
        }
    }

    #[cfg(not(unix))]
    {
        ProcessTermination {
            exit_code: status.code().unwrap_or(1),
            signal: None,
        }
    }
}
