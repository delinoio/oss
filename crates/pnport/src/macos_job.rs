// SPDX-License-Identifier: Apache-2.0
//! Foreground terminal ownership uses the caller's existing controlling tty.

use std::{
    fs::{File, OpenOptions},
    io, mem,
    os::{fd::AsRawFd, unix::process::CommandExt},
    process::Command,
};

use pnport::diagnostic::{Code, Error, Result};

fn failure() -> Error {
    Error::new(
        Code::PnportCleanupFailed,
        "The macOS command could not retain terminal job control.",
    )
}

struct Terminal {
    file: File,
    caller_group: i32,
    previous_mask: libc::sigset_t,
}

pub struct Job {
    terminal: Option<Terminal>,
    group: i32,
}

impl Job {
    pub fn start(group: i32, command: &mut Command) -> Result<Self> {
        // A pipeline's input producer can still be reading the caller's tty.
        // Transferring it to a command with redirected stdin would stop that
        // producer. Broader pipeline job control remains a release gate.
        if unsafe { libc::isatty(libc::STDIN_FILENO) } == 0 {
            return Ok(Self {
                terminal: None,
                group,
            });
        }
        let file = match OpenOptions::new().read(true).write(true).open("/dev/tty") {
            Ok(file) => file,
            Err(error)
                if matches!(
                    error.raw_os_error(),
                    Some(libc::ENXIO | libc::ENOENT | libc::ENOTTY)
                ) =>
            {
                return Ok(Self {
                    terminal: None,
                    group,
                });
            }
            Err(_) => return Err(failure()),
        };
        let mut mask = unsafe { mem::zeroed() };
        let mut previous_mask = unsafe { mem::zeroed() };
        unsafe {
            libc::sigemptyset(&mut mask);
            libc::sigaddset(&mut mask, libc::SIGTTOU);
            if libc::pthread_sigmask(libc::SIG_BLOCK, &mask, &mut previous_mask) != 0 {
                return Err(failure());
            }
            // Only the supervisor needs to write diagnostics while its command
            // owns the tty. Do not pass this temporary blocked signal to it.
            command.pre_exec(move || {
                let code =
                    libc::pthread_sigmask(libc::SIG_SETMASK, &previous_mask, std::ptr::null_mut());
                if code != 0 {
                    return Err(io::Error::from_raw_os_error(code));
                }
                Ok(())
            });
        }
        let job = Self {
            terminal: Some(Terminal {
                file,
                caller_group: unsafe { libc::getpgrp() },
                previous_mask,
            }),
            group,
        };
        job.claim()?;
        Ok(job)
    }

    fn transfer(&self, from: i32, to: i32) -> Result<()> {
        let Some(terminal) = &self.terminal else {
            return Ok(());
        };
        let fd = terminal.file.as_raw_fd();
        let foreground = unsafe { libc::tcgetpgrp(fd) };
        if foreground < 0 {
            // A hung-up tty cannot be restored; its normal SIGHUP remains the
            // shutdown authority. Other failures must not silently lose control.
            return if io::Error::last_os_error().raw_os_error() == Some(libc::ENOTTY) {
                Ok(())
            } else {
                Err(failure())
            };
        }
        if foreground == from && unsafe { libc::tcsetpgrp(fd, to) } != 0 {
            return Err(failure());
        }
        Ok(())
    }

    fn claim(&self) -> Result<()> {
        match &self.terminal {
            Some(terminal) => self.transfer(terminal.caller_group, self.group),
            None => Ok(()),
        }
    }

    pub fn restore(&self) -> Result<()> {
        match &self.terminal {
            Some(terminal) => self.transfer(self.group, terminal.caller_group),
            None => Ok(()),
        }
    }

    pub fn poll_stop(&self, pid: i32) -> Result<()> {
        let mut event = unsafe { mem::zeroed::<libc::siginfo_t>() };
        // Deliberately omit WEXITED: Child owns root reaping and its exit status.
        let result = unsafe {
            libc::waitid(
                libc::P_PID,
                pid as libc::id_t,
                &mut event,
                libc::WSTOPPED | libc::WNOHANG,
            )
        };
        if result != 0 {
            return if io::Error::last_os_error().raw_os_error() == Some(libc::EINTR) {
                Ok(())
            } else {
                Err(failure())
            };
        }
        if event.si_pid == pid && event.si_code == libc::CLD_STOPPED {
            self.restore()?;
            tracing::debug!(action = "macos_job_stopped", "Owned command stopped");
            // The shell must observe a stopped pnport job, including a command
            // SIGSTOP and background SIGTTIN. SIGCONT resumes this exact point.
            if unsafe { libc::kill(libc::getpid(), libc::SIGSTOP) } != 0 {
                return Err(failure());
            }
            self.claim()?;
            if unsafe { libc::kill(-self.group, libc::SIGCONT) } != 0 {
                return Err(failure());
            }
            tracing::debug!(action = "macos_job_resumed", "Owned command resumed");
        }
        Ok(())
    }
}

impl Drop for Job {
    fn drop(&mut self) {
        let _ = self.restore();
        if let Some(terminal) = &self.terminal {
            unsafe {
                libc::pthread_sigmask(
                    libc::SIG_SETMASK,
                    &terminal.previous_mask,
                    std::ptr::null_mut(),
                );
            }
        }
    }
}
