// SPDX-License-Identifier: Apache-2.0
//! Private process-group ownership survives abrupt supervisor termination.
//!
//! The guardian creates and pins the group before any command is spawned. Its
//! authenticated socket is owned only by the supervisor, so EOF also covers
//! SIGKILL without relying on a signal handler or a destructor in that process.
use std::{
    ffi::OsStr,
    fs,
    io::{self, Read, Write},
    mem,
    os::{
        fd::{AsRawFd, FromRawFd},
        unix::{
            fs::MetadataExt,
            net::UnixStream,
            process::{CommandExt, ExitStatusExt},
        },
    },
    path::Path,
    process::{Child, Command, Stdio},
    time::{Duration, Instant},
};

use pnport::diagnostic::{Code, Error, Result};

const MODE: &str = "__pnport_macos_owner";
const SOCKET_ENV: &str = "PNPORT_MACOS_OWNER_FD";
const DEADLINE: Duration = Duration::from_secs(7);

fn failure() -> Error {
    Error::new(
        Code::PnportCleanupFailed,
        "The macOS process owner could not complete owned-tree cleanup.",
    )
}

pub struct Owner {
    child: Child,
    socket: UnixStream,
    finished: bool,
}

impl Owner {
    pub fn start() -> Result<Self> {
        let (socket, helper) = UnixStream::pair().map_err(|_| failure())?;
        socket
            .set_read_timeout(Some(DEADLINE))
            .map_err(|_| failure())?;
        socket
            .set_write_timeout(Some(DEADLINE))
            .map_err(|_| failure())?;
        let fd = helper.as_raw_fd();
        let mut command = Command::new(std::env::current_exe().map_err(|_| failure())?);
        #[cfg(not(test))]
        command.arg(MODE);
        #[cfg(test)]
        command.args(["macos_owner::tests::private_owner_entrypoint", "--exact"]);
        command
            .env_clear()
            .env(SOCKET_ENV, fd.to_string())
            .process_group(0)
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        unsafe {
            command.pre_exec(move || {
                if libc::fcntl(fd, libc::F_SETFD, 0) < 0 {
                    return Err(io::Error::last_os_error());
                }
                Ok(())
            });
        }
        let child = command.spawn().map_err(|_| failure())?;
        drop(helper);
        let mut owner = Self {
            child,
            socket,
            finished: false,
        };
        let mut ready = [0];
        owner.socket.read_exact(&mut ready).map_err(|_| failure())?;
        if ready != *b"R" {
            return Err(failure());
        }
        tracing::debug!(
            action = "macos_owner_started",
            "Private process-group ownership is ready"
        );
        Ok(owner)
    }

    pub fn group(&self) -> i32 {
        self.child.id() as i32
    }

    pub fn check(&self) -> Result<()> {
        let mut event = libc::pollfd {
            fd: self.socket.as_raw_fd(),
            events: libc::POLLIN,
            revents: 0,
        };
        let observed = unsafe { libc::poll(&mut event, 1, 0) };
        if observed < 0 && io::Error::last_os_error().raw_os_error() != Some(libc::EINTR)
            || event.revents != 0
        {
            return Err(failure());
        }
        Ok(())
    }

    pub fn stop(&mut self, signal: i32) -> Result<()> {
        let result = (|| {
            self.socket
                .write_all(&[signal as u8])
                .map_err(|_| failure())?;
            let mut completed = [0];
            self.socket
                .read_exact(&mut completed)
                .map_err(|_| failure())?;
            match completed[0] {
                b'C' => Ok(false),
                b'K' => Ok(true),
                _ => Err(failure()),
            }
        })();
        if !matches!(&result, Ok(false)) {
            // An escalation acknowledgement requests the same group-wide kill
            // from the parent too: helper death alone cannot prove delivery.
            // Do not reap the guardian first; its unreaped PID pins the group
            // identity through escalation or a failed helper handshake.
            unsafe {
                libc::kill(-self.group(), libc::SIGKILL);
            }
        }
        let deadline = Instant::now() + DEADLINE;
        loop {
            if let Some(status) = self.child.try_wait().map_err(|_| failure())? {
                self.finished = true;
                return match result {
                    Ok(false) if status.success() => Ok(()),
                    Ok(true) if status.signal() == Some(libc::SIGKILL) => Ok(()),
                    _ => Err(failure()),
                };
            }
            if Instant::now() >= deadline {
                unsafe {
                    libc::kill(-self.group(), libc::SIGKILL);
                }
                let _ = self.child.kill();
                let _ = self.child.wait();
                self.finished = true;
                return Err(failure());
            }
            std::thread::sleep(Duration::from_millis(10));
        }
    }
}

impl Drop for Owner {
    fn drop(&mut self) {
        if !self.finished {
            let _ = self.stop(libc::SIGTERM);
        }
    }
}

fn image(pid: i32) -> Option<fs::Metadata> {
    let mut bytes = [0u8; libc::PROC_PIDPATHINFO_MAXSIZE as usize];
    let count = unsafe { libc::proc_pidpath(pid, bytes.as_mut_ptr().cast(), bytes.len() as u32) };
    if count <= 0 {
        return None;
    }
    let end = bytes.iter().position(|byte| *byte == 0)?;
    use std::os::unix::ffi::OsStrExt;
    fs::metadata(Path::new(OsStr::from_bytes(&bytes[..end]))).ok()
}

fn authenticated_socket() -> Option<UnixStream> {
    let fd = std::env::var(SOCKET_ENV)
        .ok()?
        .parse::<i32>()
        .ok()
        .filter(|fd| *fd >= 3)?;
    let mut peer = 0i32;
    let mut size = mem::size_of::<i32>() as libc::socklen_t;
    let mut uid = 0;
    let mut gid = 0;
    if unsafe {
        libc::getsockopt(
            fd,
            0,
            libc::LOCAL_PEERPID,
            (&mut peer as *mut i32).cast(),
            &mut size,
        )
    } != 0
        || size as usize != mem::size_of::<i32>()
        || peer != unsafe { libc::getppid() }
        || unsafe { libc::getpeereid(fd, &mut uid, &mut gid) } != 0
        || uid != unsafe { libc::getuid() }
        || gid != unsafe { libc::getgid() }
    {
        return None;
    }
    let parent = image(peer)?;
    let current = fs::metadata(std::env::current_exe().ok()?).ok()?;
    if parent.dev() != current.dev() || parent.ino() != current.ino() {
        return None;
    }
    if unsafe { libc::getpgrp() } != unsafe { libc::getpid() } {
        return None;
    }
    if unsafe { libc::fcntl(fd, libc::F_SETFD, libc::FD_CLOEXEC) } < 0 {
        return None;
    }
    Some(unsafe { UnixStream::from_raw_fd(fd) })
}

fn members(group: i32) -> io::Result<Vec<i32>> {
    let mut pids = vec![0i32; 64];
    loop {
        let bytes = (pids.len() * mem::size_of::<i32>()) as i32;
        let count = unsafe { libc::proc_listpgrppids(group, pids.as_mut_ptr().cast(), bytes) };
        if count <= 0 || count as usize > pids.len() {
            return Err(io::Error::last_os_error());
        }
        if count as usize == pids.len() {
            if pids.len() >= 4096 {
                return Err(io::Error::other("Owned group inventory is unavailable"));
            }
            pids.resize(pids.len() * 2, 0);
            continue;
        }
        pids.truncate(count as usize);
        return Ok(pids);
    }
}

fn running(pid: i32, group: i32) -> io::Result<bool> {
    if pid == group || pid <= 0 {
        return Ok(false);
    }
    let mut info = mem::MaybeUninit::<libc::proc_bsdinfo>::zeroed();
    let size = mem::size_of::<libc::proc_bsdinfo>() as i32;
    let count = unsafe {
        libc::proc_pidinfo(
            pid,
            libc::PROC_PIDTBSDINFO,
            1,
            info.as_mut_ptr().cast(),
            size,
        )
    };
    if count == size {
        let info = unsafe { info.assume_init() };
        return Ok(info.pbi_pgid == group as u32 && info.pbi_status != 5);
    }
    // A process may disappear between the group inventory and this query.
    // Other failures cannot establish that it stopped and must fail closed.
    let error = io::Error::last_os_error();
    if error.raw_os_error() == Some(libc::ESRCH) {
        Ok(false)
    } else {
        Err(error)
    }
}

#[derive(Clone, Copy)]
enum Cleanup {
    Complete,
    Escalate,
}

fn stop_group(group: i32, signal: i32) -> io::Result<Cleanup> {
    unsafe {
        libc::kill(-group, signal);
    }
    let live = || -> io::Result<Vec<i32>> {
        members(group)?
            .into_iter()
            .filter_map(|pid| match running(pid, group) {
                Ok(true) => Some(Ok(pid)),
                Ok(false) => None,
                Err(error) => Some(Err(error)),
            })
            .collect()
    };
    let deadline = Instant::now() + Duration::from_secs(5);
    while !live()?.is_empty() && Instant::now() < deadline {
        std::thread::sleep(Duration::from_millis(25));
    }
    Ok(if live()?.is_empty() {
        Cleanup::Complete
    } else {
        Cleanup::Escalate
    })
}

pub fn dispatch_helper() {
    let mut args = std::env::args_os();
    args.next();
    if args.next().as_deref() != Some(OsStr::new(MODE)) {
        return;
    }
    if args.next().is_some() {
        unsafe {
            libc::_exit(125);
        }
    }
    run_helper();
}

fn run_helper() -> ! {
    let result = (|| -> Option<()> {
        let mut socket = authenticated_socket()?;
        unsafe {
            for signal in [libc::SIGINT, libc::SIGTERM, libc::SIGHUP] {
                libc::signal(signal, libc::SIG_IGN);
            }
        }
        socket.write_all(b"R").ok()?;
        let mut command = [0];
        let signal = match socket.read(&mut command) {
            Ok(1)
                if matches!(
                    i32::from(command[0]),
                    libc::SIGINT | libc::SIGTERM | libc::SIGHUP
                ) =>
            {
                i32::from(command[0])
            }
            _ => libc::SIGTERM,
        };
        let stopped = stop_group(unsafe { libc::getpgrp() }, signal);
        match stopped {
            Ok(Cleanup::Complete) => {
                socket.write_all(b"C").ok();
            }
            result => {
                // Kill the pinned group atomically, including its guardian,
                // rather than signalling a reusable PID from an inventory.
                // K permits the parent to accept this deliberate helper death;
                // F preserves an inventory failure as cleanup failure.
                socket
                    .write_all(if result.is_ok() { b"K" } else { b"F" })
                    .ok();
                unsafe {
                    libc::kill(-libc::getpgrp(), libc::SIGKILL);
                    libc::_exit(125);
                }
            }
        }
        Some(())
    })();
    unsafe {
        libc::_exit(if result.is_some() { 0 } else { 125 });
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn private_owner_entrypoint() {
        // Unit scenarios run the supervisor inside a fresh copy of the test
        // executable. Use that same image for the authenticated guardian too.
        if std::env::var_os(super::SOCKET_ENV).is_some() {
            super::run_helper();
        }
    }
}
