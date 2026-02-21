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

    let exit_code = status_code(status);

    info!(
        command_path = command_path_key,
        executable = %command_path.display(),
        exit_code,
        "Delegated process finished"
    );

    Ok(exit_code)
}

fn status_code(status: ExitStatus) -> i32 {
    status.code().unwrap_or(1)
}
