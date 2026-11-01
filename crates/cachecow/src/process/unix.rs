// SPDX-License-Identifier: Apache-2.0
//! Process helpers shared by the Linux and macOS backends.

use std::{
    fs, io,
    os::unix::{fs::MetadataExt, process::CommandExt},
    sync::mpsc,
};

use anyhow::{Result, bail};

use super::Command;

/// The process state root must be a directory owned by this user with no
/// group or other permissions.
pub fn private_root(_path: &str, info: &fs::Metadata) -> Result<()> {
    // SAFETY: getuid has no preconditions.
    let uid = unsafe { libc::getuid() };
    if info.uid() != uid || info.mode() & 0o077 != 0 {
        bail!("process state root must be owned by this user with mode 0700");
    }
    Ok(())
}

pub fn sync_directory(path: &str) -> io::Result<()> {
    fs::File::open(path)?.sync_all()
}

/// Unix commands keep the exact argv\[0\].
pub fn base_command(c: &Command) -> Result<std::process::Command> {
    let mut cmd = std::process::Command::new(&c.path);
    cmd.arg0(&c.args[0]).args(&c.args[1..]);
    Ok(cmd)
}

/// Starts the root in its own process group and reports its exit code.
pub fn start(mut cmd: std::process::Command) -> Result<mpsc::Receiver<i32>> {
    cmd.process_group(0);
    let mut child = cmd.spawn()?;
    let (tx, rx) = mpsc::channel();
    std::thread::Builder::new()
        .name("ccow-root-wait".into())
        .spawn(move || {
            let code = child.wait().map_or(125, super::exit_code);
            let _ = tx.send(code);
        })?;
    Ok(rx)
}

pub fn request_termination(owner_pid: i64) -> Result<()> {
    let pid = libc::pid_t::try_from(owner_pid)?;
    if pid <= 0 {
        bail!("invalid supervisor process ID");
    }
    // SAFETY: kill has no memory-safety preconditions.
    if unsafe { libc::kill(pid, libc::SIGTERM) } != 0 {
        return Err(io::Error::last_os_error().into());
    }
    Ok(())
}

/// Whether `pid` currently names a process (any owner).
pub fn alive(pid: i64) -> bool {
    let Ok(pid) = libc::pid_t::try_from(pid) else {
        return false;
    };
    if pid <= 0 {
        return false;
    }
    // SAFETY: signal 0 only checks existence and permission.
    if unsafe { libc::kill(pid, 0) } == 0 {
        return true;
    }
    io::Error::last_os_error().raw_os_error() == Some(libc::EPERM)
}
