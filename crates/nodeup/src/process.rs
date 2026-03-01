use std::{
    ffi::OsString,
    path::Path,
    process::{Command, ExitStatus, Stdio},
};

use tracing::info;

use crate::errors::{NodeupError, Result};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DelegatedStdioPolicy {
    Inherit,
    StdoutToStderr,
}

impl DelegatedStdioPolicy {
    fn as_str(self) -> &'static str {
        match self {
            Self::Inherit => "inherit",
            Self::StdoutToStderr => "stdout-to-stderr",
        }
    }
}

pub fn run_command(
    command_path: &Path,
    args: &[OsString],
    stdio_policy: DelegatedStdioPolicy,
    command_path_key: &str,
) -> Result<i32> {
    info!(
        command_path = command_path_key,
        executable = %command_path.display(),
        args_len = args.len(),
        stdio_policy = stdio_policy.as_str(),
        "Spawning delegated process"
    );

    let mut command = Command::new(command_path);
    command.args(args);

    match stdio_policy {
        DelegatedStdioPolicy::Inherit => {
            command.stdout(Stdio::inherit()).stderr(Stdio::inherit());
        }
        DelegatedStdioPolicy::StdoutToStderr => {
            command.stdout(Stdio::from(std::io::stderr()));
            command.stderr(Stdio::inherit());
        }
    }

    let status = command.status().map_err(|error| {
        NodeupError::not_found(format!(
            "Failed to execute {}: {error}",
            command_path.display()
        ))
    })?;

    let termination = status_details(status);

    info!(
        command_path = command_path_key,
        executable = %command_path.display(),
        stdio_policy = stdio_policy.as_str(),
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
