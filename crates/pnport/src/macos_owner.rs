// SPDX-License-Identifier: Apache-2.0
//! Private process-group ownership survives abrupt supervisor termination.
//!
//! The guardian's live/unreaped PID reserves the command group identifier. An
//! inert child keeps that group present while the guardian moves to a private
//! group before user launch. Command-group SIGSTOP cannot freeze EOF recovery.
use std::{
    ffi::OsStr,
    fs,
    io::{self, Read, Write},
    mem,
    os::{
        fd::{AsRawFd, FromRawFd},
        unix::{fs::MetadataExt, net::UnixStream, process::CommandExt},
    },
    path::Path,
    process::{Child, Command, Stdio},
    time::{Duration, Instant},
};

use pnport::diagnostic::{Code, Error, Result};

const MODE: &str = "__pnport_macos_owner";
const SOCKET_ENV: &str = "PNPORT_MACOS_OWNER_FD";
#[cfg(test)]
const ROLE_ENV: &str = "PNPORT_MACOS_OWNER_ROLE";
const DEADLINE: Duration = Duration::from_secs(7);

#[derive(Clone, Copy)]
enum Role {
    Guardian,
    Anchor,
    Bootstrap,
}

impl Role {
    fn name(self) -> &'static str {
        match self {
            Self::Guardian => "guardian",
            Self::Anchor => "anchor",
            Self::Bootstrap => "bootstrap",
        }
    }

    fn parse(value: &OsStr) -> Option<Self> {
        match value.to_str()? {
            "guardian" => Some(Self::Guardian),
            "anchor" => Some(Self::Anchor),
            "bootstrap" => Some(Self::Bootstrap),
            _ => None,
        }
    }
}

fn launch(role: Role) -> Result<(Child, UnixStream)> {
    let (socket, helper) = UnixStream::pair().map_err(|_| failure())?;
    socket
        .set_read_timeout(Some(DEADLINE))
        .map_err(|_| failure())?;
    socket
        .set_write_timeout(Some(DEADLINE))
        .map_err(|_| failure())?;
    let fd = helper.as_raw_fd();
    let mut command = Command::new(std::env::current_exe().map_err(|_| failure())?);
    command.env_clear();
    #[cfg(not(test))]
    command.args([MODE, role.name()]);
    #[cfg(test)]
    command
        .args(["macos_owner::tests::private_owner_entrypoint", "--exact"])
        .env(ROLE_ENV, role.name());
    command
        .env(SOCKET_ENV, fd.to_string())
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    if !matches!(role, Role::Anchor) {
        command.process_group(0);
    }
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
    Ok((child, socket))
}

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
        let (child, socket) = launch(Role::Guardian)?;
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
        tracing::debug!(
            action = "macos_owner_cleanup_requested",
            signal,
            "Stopping the owned command group"
        );
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
        tracing::debug!(
            action = "macos_owner_cleanup_acknowledged",
            escalated = matches!(&result, Ok(true)),
            failed = result.is_err(),
            "Private process owner reported its cleanup outcome"
        );
        if !matches!(&result, Ok(false)) {
            // Repeat escalation before reaping the direct guardian child. Its
            // PID reserves the command PGID even though it runs outside it.
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
                    Ok(true) if status.success() => Ok(()),
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

fn authenticated_socket(role: Role) -> Option<UnixStream> {
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
    let expected_group = match role {
        Role::Anchor => peer,
        Role::Guardian | Role::Bootstrap => unsafe { libc::getpid() },
    };
    if unsafe { libc::getpgrp() } != expected_group {
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

fn running(pid: i32, group: i32, anchor: i32) -> io::Result<bool> {
    if pid == anchor || pid <= 0 {
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

fn stop_group(group: i32, anchor: i32, signal: i32) -> io::Result<Cleanup> {
    unsafe {
        if libc::kill(-group, signal) != 0 {
            return Err(io::Error::last_os_error());
        }
        // Queue termination before resuming stopped members so their handlers
        // receive the original signal during the unchanged five-second grace.
        if libc::kill(-group, libc::SIGCONT) != 0 {
            return Err(io::Error::last_os_error());
        }
    }
    let live = || -> io::Result<Vec<i32>> {
        members(group)?
            .into_iter()
            .filter_map(|pid| match running(pid, group, anchor) {
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
    let role = args.next().and_then(|value| Role::parse(&value));
    if role.is_none() || args.next().is_some() {
        unsafe {
            libc::_exit(125);
        }
    }
    run_helper(role.unwrap());
}

struct Member {
    child: Child,
    socket: UnixStream,
    reaped: bool,
}

impl Member {
    fn start(role: Role) -> Result<Self> {
        let (child, socket) = launch(role)?;
        let mut member = Self {
            child,
            socket,
            reaped: false,
        };
        let mut ready = [0];
        member
            .socket
            .read_exact(&mut ready)
            .map_err(|_| failure())?;
        if ready != *b"R" {
            return Err(failure());
        }
        Ok(member)
    }
}

impl Drop for Member {
    fn drop(&mut self) {
        // This is our direct, unreaped child, never an inventory PID. It is
        // safe to reap only after the group's final signal has been issued.
        if !self.reaped {
            let _ = self.child.kill();
            let _ = self.child.wait();
        }
    }
}

fn independent_group() -> Result<Member> {
    let anchor = Member::start(Role::Anchor)?;
    let mut bootstrap = Member::start(Role::Bootstrap)?;
    // setpgid cannot create a new group with another live PID. The bootstrap
    // supplies an existing private group in the same session; the anchor
    // preserves the original group until user processes can join it.
    if unsafe { libc::setpgid(0, bootstrap.child.id() as i32) } != 0 {
        return Err(failure());
    }
    bootstrap.socket.write_all(b"X").map_err(|_| failure())?;
    let status = bootstrap.child.wait().map_err(|_| failure())?;
    bootstrap.reaped = true;
    if !status.success() {
        return Err(failure());
    }
    Ok(anchor)
}

fn cleanup_signal(socket: &mut UnixStream, anchor: &Member) -> io::Result<i32> {
    let mut events = [
        libc::pollfd {
            fd: socket.as_raw_fd(),
            events: libc::POLLIN,
            revents: 0,
        },
        libc::pollfd {
            fd: anchor.socket.as_raw_fd(),
            events: libc::POLLIN,
            revents: 0,
        },
    ];
    loop {
        if unsafe { libc::poll(events.as_mut_ptr(), events.len() as _, -1) } < 0 {
            let error = io::Error::last_os_error();
            if error.raw_os_error() == Some(libc::EINTR) {
                continue;
            }
            return Err(error);
        }
        if events[1].revents != 0 {
            return Err(io::Error::other("Private group anchor failed"));
        }
        if events[0].revents != 0 {
            let mut command = [0];
            return Ok(match socket.read(&mut command) {
                Ok(1)
                    if matches!(
                        i32::from(command[0]),
                        libc::SIGINT | libc::SIGTERM | libc::SIGHUP
                    ) =>
                {
                    i32::from(command[0])
                }
                _ => libc::SIGTERM,
            });
        }
    }
}

fn run_helper(role: Role) -> ! {
    let result = (|| -> Option<()> {
        let mut socket = authenticated_socket(role)?;
        unsafe {
            for signal in [
                libc::SIGINT,
                libc::SIGTERM,
                libc::SIGHUP,
                libc::SIGTSTP,
                libc::SIGTTIN,
                libc::SIGTTOU,
            ] {
                libc::signal(signal, libc::SIG_IGN);
            }
        }
        if !matches!(role, Role::Guardian) {
            socket.write_all(b"R").ok()?;
            socket.set_read_timeout(None).ok()?;
            let mut command = [0];
            // EOF also releases a bootstrap/anchor after startup failure or
            // guardian death. The guardian PID stays reserved until its parent
            // has issued its fallback group signal and reaped that child.
            return match socket.read(&mut command) {
                Ok(0) => Some(()),
                Ok(1) if command == *b"X" => Some(()),
                _ => None,
            };
        }
        let group = unsafe { libc::getpid() };
        let anchor = independent_group().ok()?;
        socket.write_all(b"R").ok()?;
        let signal = cleanup_signal(&mut socket, &anchor);
        let stopped = stop_group(
            group,
            anchor.child.id() as i32,
            *signal.as_ref().unwrap_or(&libc::SIGTERM),
        );
        // The guardian is outside this group and retains its PID reservation
        // through the final group signal, member reaping and acknowledgement.
        let killed = unsafe { libc::kill(-group, libc::SIGKILL) } == 0;
        drop(anchor);
        let completed = match (&signal, &stopped, killed) {
            (Ok(_), Ok(Cleanup::Complete), true) => b'C',
            (Ok(_), Ok(Cleanup::Escalate), true) => b'K',
            _ => b'F',
        };
        socket.write_all(&[completed]).ok();
        (completed != b'F').then_some(())
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
            let role = std::env::var_os(super::ROLE_ENV)
                .and_then(|value| super::Role::parse(&value))
                .unwrap();
            super::run_helper(role);
        }
    }
}
