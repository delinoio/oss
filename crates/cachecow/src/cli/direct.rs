// SPDX-License-Identifier: Apache-2.0
//! Transparent pass-through to the real tool.

use crate::process;

/// Replaces this process with the real tool (Unix `execve`). Returns only on
/// failure, with the execution exit code.
#[cfg(unix)]
pub(crate) fn direct(command: &process::Command) -> Result<i32, (i32, anyhow::Error)> {
    use std::os::unix::process::CommandExt;
    let mut cmd = std::process::Command::new(&command.path);
    if let Some((first, rest)) = command.args.split_first() {
        cmd.arg0(first).args(rest);
    }
    cmd.env_clear().current_dir(&command.dir);
    for (key, value) in env_pairs(&command.env) {
        cmd.env(key, value);
    }
    let error = anyhow::Error::new(cmd.exec());
    Err((process::execution_code(&error), error))
}

/// Runs the real tool with inherited streams and returns its exit code.
#[cfg(windows)]
pub(crate) fn direct(command: &process::Command) -> Result<i32, (i32, anyhow::Error)> {
    let mut cmd =
        process::new_exec_command(command).map_err(|e| (process::execution_code(&e), e))?;
    match cmd.status() {
        Ok(status) => Ok(process::exit_code(status)),
        Err(e) => {
            let error = anyhow::Error::new(e);
            Err((process::execution_code(&error), error))
        }
    }
}

/// Splits `KEY=value` entries. A leading `=` belongs to the key, as in
/// Windows drive-directory variables.
#[cfg_attr(
    windows,
    expect(dead_code, reason = "Windows uses process::new_exec_command")
)]
pub(crate) fn env_pairs(env: &[String]) -> Vec<(&str, &str)> {
    env.iter()
        .filter_map(|entry| {
            let start = usize::from(entry.starts_with('='));
            entry[start..]
                .find('=')
                .map(|i| (&entry[..start + i], &entry[start + i + 1..]))
        })
        .collect()
}
