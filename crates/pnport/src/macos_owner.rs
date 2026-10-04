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
    path::{Path, PathBuf},
    process::{Child, Command, Stdio},
    time::{Duration, Instant},
};

use pnport::diagnostic::{Code, Error, Result};
use pnport_core::macos_process::Identity;

use crate::macos_registry::{FailureStage, Registry};

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
    registry: Registry,
}

impl Owner {
    pub fn start(session: &Path) -> Result<Self> {
        use std::os::unix::ffi::OsStrExt;
        let mut key = [0; 32];
        fs::File::open("/dev/urandom")
            .and_then(|mut file| file.read_exact(&mut key))
            .map_err(|_| failure())?;
        let registry = Registry::create(session, key).map_err(|_| failure())?;
        let (child, socket) = launch(Role::Guardian)?;
        let mut owner = Self {
            child,
            socket,
            finished: false,
            registry,
        };
        let path = session.as_os_str().as_bytes();
        owner
            .socket
            .write_all(&(path.len() as u32).to_be_bytes())
            .map_err(|_| failure())?;
        owner.socket.write_all(path).map_err(|_| failure())?;
        owner.socket.write_all(&key).map_err(|_| failure())?;
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

    pub fn admit_root(&mut self, pid: i32) -> Result<()> {
        let identity = Identity::capture(pid).map_err(|_| failure())?;
        // Only the trusted supervisor control socket can seed a root birth.
        // Preload files cannot promote an unrelated process to a root.
        self.socket.write_all(b"A").map_err(|_| failure())?;
        self.socket
            .write_all(&pid.to_be_bytes())
            .map_err(|_| failure())?;
        self.socket
            .write_all(&identity.birth.to_be_bytes())
            .map_err(|_| failure())?;
        let mut acknowledged = [0];
        self.socket
            .read_exact(&mut acknowledged)
            .map_err(|_| failure())?;
        if acknowledged != *b"A" {
            return Err(failure());
        }
        self.registry.admit(identity).map_err(|_| failure())?;
        self.registry.recover().map_err(|_| failure())?;
        tracing::debug!(
            action = "macos_root_admitted",
            "Native root ownership is committed"
        );
        Ok(())
    }

    pub fn verification_key(&self) -> String {
        pnport_core::macos_process::encode_public_key(&self.registry.verification_key())
    }

    pub fn owns_group(&mut self, group: i32) -> Result<bool> {
        // EOF does not reap the guardian: retain the original group reservation
        // while authenticated parent recovery verifies additional tty ownership.
        if self.check().is_err() {
            self.registry.recover().map_err(|_| failure())?;
            return self.registry.owns_group(group).map_err(|_| failure());
        }
        self.socket.write_all(b"T").map_err(|_| failure())?;
        self.socket
            .write_all(&group.to_be_bytes())
            .map_err(|_| failure())?;
        let mut response = [0];
        self.socket
            .read_exact(&mut response)
            .map_err(|_| failure())?;
        match response[0] {
            b'Y' => Ok(true),
            b'N' => Ok(false),
            _ => Err(failure()),
        }
    }

    pub fn resume(&mut self, group: i32) -> Result<()> {
        self.socket.write_all(b"J").map_err(|_| failure())?;
        self.socket
            .write_all(&group.to_be_bytes())
            .map_err(|_| failure())?;
        let mut reply = [0];
        self.socket.read_exact(&mut reply).map_err(|_| failure())?;
        if reply != *b"J" {
            return Err(failure());
        }
        Ok(())
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
            tracing::debug!(action = "macos_owner_unavailable", stage = ?self.registry.failure_stage(), "Native process owner became unavailable");
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
        if result.is_err() {
            let recovered = self.registry.recover();
            let stopped = self.registry.stop(self.group(), signal);
            tracing::debug!(
                action = "macos_owner_recovery",
                records_valid = recovered.is_ok(),
                cleaned = stopped.is_ok(),
                "Attempted authenticated owner recovery"
            );
        }
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

fn cleanup_signal(
    socket: &mut UnixStream,
    anchor: &Member,
    registry: &mut Registry,
) -> io::Result<i32> {
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
        if unsafe { libc::poll(events.as_mut_ptr(), events.len() as _, 10) } < 0 {
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
            let count = socket.read(&mut command)?;
            if count == 1 && command == *b"T" {
                let mut group = [0; 4];
                socket.read_exact(&mut group)?;
                let group = i32::from_be_bytes(group);
                if group <= 0 {
                    return Err(io::Error::other("Native foreground is invalid"));
                }
                socket.write_all(if registry.owns_group(group)? {
                    b"Y"
                } else {
                    b"N"
                })?;
                continue;
            }
            if count == 1 && command == *b"J" {
                let mut group = [0; 4];
                socket.read_exact(&mut group)?;
                let group = i32::from_be_bytes(group);
                if group <= 0 {
                    return Err(io::Error::other("Native job resume failed"));
                }
                registry.resume(unsafe { libc::getpid() }, group)?;
                socket.write_all(b"J")?;
                continue;
            }
            if count == 1 && command == *b"A" {
                let mut pid = [0; 4];
                let mut birth = [0; 8];
                socket.read_exact(&mut pid)?;
                socket.read_exact(&mut birth)?;
                let identity = Identity::capture(i32::from_be_bytes(pid))?;
                if identity.birth != u64::from_be_bytes(birth) || !registry.empty() {
                    return Err(io::Error::other("Native root admission failed"));
                }
                registry.admit(identity)?;
                socket.write_all(b"A")?;
                continue;
            }
            return Ok(match count {
                1 if matches!(
                    i32::from(command[0]),
                    libc::SIGINT | libc::SIGTERM | libc::SIGHUP
                ) =>
                {
                    i32::from(command[0])
                }
                _ => libc::SIGTERM,
            });
        }
        if let Err(error) = registry.requests() {
            registry.record_failure(FailureStage::Registration);
            return Err(error);
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
        use std::os::unix::ffi::OsStringExt;
        let mut size = [0; 4];
        socket.read_exact(&mut size).ok()?;
        let size = u32::from_be_bytes(size) as usize;
        if size == 0 || size > 4096 {
            return None;
        }
        let mut path = vec![0; size];
        let mut key = [0; 32];
        socket.read_exact(&mut path).ok()?;
        socket.read_exact(&mut key).ok()?;
        let session = PathBuf::from(std::ffi::OsString::from_vec(path));
        let mut registry = Registry::create(&session, key).ok()?;
        let group = unsafe { libc::getpid() };
        let anchor = independent_group().ok()?;
        fs::write(session.join("owner/alive"), b"1").ok()?;
        socket.write_all(b"R").ok()?;
        let signal = cleanup_signal(&mut socket, &anchor, &mut registry);
        let stopped = registry.stop(group, *signal.as_ref().unwrap_or(&libc::SIGTERM));
        if stopped.is_err() {
            registry.record_failure(FailureStage::Cleanup);
        }
        let _ = fs::remove_file(session.join("owner/alive"));
        // The guardian is outside this group and retains its PID reservation
        // through the final group signal, member reaping and acknowledgement.
        let killed = unsafe { libc::kill(-group, libc::SIGKILL) } == 0;
        drop(anchor);
        let completed = match (&signal, &stopped, killed) {
            (Ok(_), Ok(false), true) => b'C',
            (Ok(_), Ok(true), true) => b'K',
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
