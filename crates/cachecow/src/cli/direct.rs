// SPDX-License-Identifier: Apache-2.0
//! Transparent pass-through to the real tool.

use std::ffi::OsString;

use crate::process;

/// Replaces this process with the real tool (Unix `execve`). Returns only on
/// failure, with the execution exit code.
///
/// The tool receives `args` byte for byte after `arg0` and inherits the process
/// environment unchanged, so non-UTF-8 values are not altered.
#[cfg(unix)]
pub(crate) fn direct(
    path: &str,
    arg0: &str,
    args: &[OsString],
    dir: &str,
) -> Result<i32, (i32, anyhow::Error)> {
    use std::os::unix::process::CommandExt;
    let mut cmd = std::process::Command::new(path);
    cmd.arg0(arg0).args(args).current_dir(dir);
    let error = anyhow::Error::new(cmd.exec());
    Err((process::execution_code(&error), error))
}

/// Runs the real tool with inherited streams and returns its exit code.
///
/// Windows keeps the text based command construction (batch wrapper quoting
/// needs it), so arguments and environment values that are not valid UTF-8 are
/// rejected instead of being changed.
#[cfg(windows)]
pub(crate) fn direct(
    path: &str,
    arg0: &str,
    args: &[OsString],
    dir: &str,
) -> Result<i32, (i32, anyhow::Error)> {
    let mut argv = vec![arg0.to_string()];
    for arg in args {
        let Some(text) = arg.to_str() else {
            let error = anyhow::anyhow!("arguments must be valid UTF-8");
            return Err((2, error));
        };
        argv.push(text.to_string());
    }
    let command = process::Command {
        noop: false,
        sccache: false,
        path: path.to_string(),
        args: argv,
        dir: dir.to_string(),
        env: super::environ(),
    };
    let mut cmd =
        process::new_exec_command(&command).map_err(|e| (process::execution_code(&e), e))?;
    match cmd.status() {
        Ok(status) => Ok(process::exit_code(status)),
        Err(e) => {
            let error = anyhow::Error::new(e);
            Err((process::execution_code(&error), error))
        }
    }
}
