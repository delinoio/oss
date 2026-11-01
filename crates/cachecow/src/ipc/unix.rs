// SPDX-License-Identifier: Apache-2.0
//! Unix domain socket transport in a private `/tmp/<prefix>XXXXXX` directory.

use std::{
    ffi::CString,
    io::{self, Read, Write},
    os::{
        fd::{AsFd, AsRawFd, BorrowedFd, FromRawFd, OwnedFd, RawFd},
        unix::net::{UnixListener, UnixStream},
    },
    sync::{
        Mutex,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};

use anyhow::{Result, anyhow};

use super::closed_error;
use crate::process::ProcessError;

pub struct Listener {
    socket: UnixListener,
    directory: String,
    wake_read: OwnedFd,
    wake_write: OwnedFd,
    closed: AtomicBool,
}

fn pipe() -> io::Result<(OwnedFd, OwnedFd)> {
    let mut fds = [0 as RawFd; 2];
    // SAFETY: `fds` has room for the two descriptors pipe(2) writes.
    if unsafe { libc::pipe(fds.as_mut_ptr()) } != 0 {
        return Err(io::Error::last_os_error());
    }
    // SAFETY: pipe(2) succeeded, so both descriptors are open and owned here.
    let pair = unsafe { (OwnedFd::from_raw_fd(fds[0]), OwnedFd::from_raw_fd(fds[1])) };
    for fd in [&pair.0, &pair.1] {
        // SAFETY: `fd` is an open descriptor owned by `pair`.
        unsafe { libc::fcntl(fd.as_raw_fd(), libc::F_SETFD, libc::FD_CLOEXEC) };
    }
    Ok(pair)
}

fn private_temp_dir(prefix: &str) -> Result<String> {
    let template = CString::new(format!("/tmp/{prefix}XXXXXX"))?;
    let mut raw = template.into_bytes_with_nul();
    // SAFETY: `raw` is a writable NUL-terminated template ending in XXXXXX.
    let result = unsafe { libc::mkdtemp(raw.as_mut_ptr().cast()) };
    if result.is_null() {
        return Err(io::Error::last_os_error().into());
    }
    raw.pop();
    String::from_utf8(raw).map_err(|_| anyhow!("temporary IPC directory is not UTF-8"))
}

impl Listener {
    pub fn bind(prefix: &str) -> Result<(Self, String)> {
        let directory = private_temp_dir(prefix)?;
        let name = if prefix.starts_with("ccow-p") {
            "control"
        } else {
            "ipc"
        };
        let endpoint = format!("{directory}/{name}");
        let socket = match UnixListener::bind(&endpoint) {
            Ok(s) => s,
            Err(e) => {
                let _ = std::fs::remove_dir_all(&directory);
                return Err(e.into());
            }
        };
        socket.set_nonblocking(true)?;
        let (wake_read, wake_write) = pipe()?;
        Ok((
            Self {
                socket,
                directory,
                wake_read,
                wake_write,
                closed: AtomicBool::new(false),
            },
            endpoint,
        ))
    }

    pub fn accept(&self) -> Result<Stream> {
        loop {
            if self.closed.load(Ordering::SeqCst) {
                return Err(closed_error().into());
            }
            let mut fds = [
                libc::pollfd {
                    fd: self.socket.as_raw_fd(),
                    events: libc::POLLIN,
                    revents: 0,
                },
                libc::pollfd {
                    fd: self.wake_read.as_raw_fd(),
                    events: libc::POLLIN,
                    revents: 0,
                },
            ];
            // SAFETY: `fds` is a valid array of two pollfd structures.
            let n = unsafe { libc::poll(fds.as_mut_ptr(), 2, -1) };
            if n < 0 {
                let e = io::Error::last_os_error();
                if e.kind() == io::ErrorKind::Interrupted {
                    continue;
                }
                return Err(e.into());
            }
            if fds[1].revents != 0 || self.closed.load(Ordering::SeqCst) {
                return Err(closed_error().into());
            }
            match self.socket.accept() {
                Ok((stream, _)) => {
                    stream.set_nonblocking(false)?;
                    return Ok(Stream::new(stream));
                }
                Err(e)
                    if matches!(
                        e.kind(),
                        io::ErrorKind::WouldBlock | io::ErrorKind::Interrupted
                    ) => {}
                Err(e) => return Err(e.into()),
            }
        }
    }

    pub fn close(&self) {
        if self.closed.swap(true, Ordering::SeqCst) {
            return;
        }
        let byte = [1u8];
        // SAFETY: writes one byte from a valid buffer to the owned pipe.
        unsafe { libc::write(self.wake_write.as_raw_fd(), byte.as_ptr().cast(), 1) };
        let _ = std::fs::remove_dir_all(&self.directory);
    }
}

pub struct Stream {
    socket: UnixStream,
    deadline: Mutex<Option<Instant>>,
    closed: AtomicBool,
}

impl Stream {
    fn new(socket: UnixStream) -> Self {
        Self {
            socket,
            deadline: Mutex::new(None),
            closed: AtomicBool::new(false),
        }
    }

    pub fn dial(endpoint: &str, _timeout: Duration) -> Result<Self> {
        // Local socket connects complete or fail immediately.
        Ok(Self::new(UnixStream::connect(endpoint)?))
    }

    fn arm(&self) -> io::Result<()> {
        if self.closed.load(Ordering::SeqCst) {
            return Err(closed_error());
        }
        let deadline = *self
            .deadline
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        let timeout = match deadline {
            None => None,
            Some(d) => {
                let now = Instant::now();
                if now >= d {
                    return Err(io::Error::new(
                        io::ErrorKind::TimedOut,
                        "IPC deadline exceeded",
                    ));
                }
                Some(d - now)
            }
        };
        self.socket.set_read_timeout(timeout)?;
        self.socket.set_write_timeout(timeout)
    }

    pub fn read(&self, buf: &mut [u8]) -> io::Result<usize> {
        loop {
            self.arm()?;
            match (&self.socket).read(buf) {
                Err(e) if e.kind() == io::ErrorKind::Interrupted => {}
                Err(e) if e.kind() == io::ErrorKind::WouldBlock => {
                    return Err(io::Error::new(
                        io::ErrorKind::TimedOut,
                        "IPC deadline exceeded",
                    ));
                }
                other => {
                    if self.closed.load(Ordering::SeqCst) {
                        return Err(closed_error());
                    }
                    return other;
                }
            }
        }
    }

    pub fn write_all(&self, mut buf: &[u8]) -> io::Result<()> {
        while !buf.is_empty() {
            self.arm()?;
            match (&self.socket).write(buf) {
                Ok(0) => return Err(io::Error::from(io::ErrorKind::WriteZero)),
                Ok(n) => buf = &buf[n..],
                Err(e) if e.kind() == io::ErrorKind::Interrupted => {}
                Err(e) if e.kind() == io::ErrorKind::WouldBlock => {
                    return Err(io::Error::new(
                        io::ErrorKind::TimedOut,
                        "IPC deadline exceeded",
                    ));
                }
                Err(e) => return Err(e),
            }
        }
        Ok(())
    }

    pub fn set_deadline(&self, deadline: Option<Instant>) -> io::Result<()> {
        *self
            .deadline
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner) = deadline;
        Ok(())
    }

    pub fn close(&self) {
        self.closed.store(true, Ordering::SeqCst);
        let _ = self.socket.shutdown(std::net::Shutdown::Both);
    }

    #[cfg(target_os = "linux")]
    pub fn peer_pid(&self) -> io::Result<i64> {
        let mut cred: libc::ucred = libc::ucred {
            pid: 0,
            uid: 0,
            gid: 0,
        };
        let mut len = std::mem::size_of::<libc::ucred>() as libc::socklen_t;
        // SAFETY: `cred` and `len` describe a valid output buffer.
        let r = unsafe {
            libc::getsockopt(
                self.socket.as_raw_fd(),
                libc::SOL_SOCKET,
                libc::SO_PEERCRED,
                (&raw mut cred).cast(),
                &mut len,
            )
        };
        if r != 0 {
            return Err(io::Error::last_os_error());
        }
        Ok(i64::from(cred.pid))
    }

    #[cfg(target_os = "macos")]
    pub fn peer_pid(&self) -> io::Result<i64> {
        let mut pid: libc::pid_t = 0;
        let mut len = std::mem::size_of::<libc::pid_t>() as libc::socklen_t;
        // SAFETY: `pid` and `len` describe a valid output buffer.
        let r = unsafe {
            libc::getsockopt(
                self.socket.as_raw_fd(),
                libc::SOL_LOCAL,
                libc::LOCAL_PEERPID,
                (&raw mut pid).cast(),
                &mut len,
            )
        };
        if r != 0 {
            return Err(io::Error::last_os_error());
        }
        Ok(i64::from(pid))
    }

    #[cfg(not(any(target_os = "linux", target_os = "macos")))]
    pub fn peer_pid(&self) -> io::Result<i64> {
        Err(io::Error::new(
            io::ErrorKind::Unsupported,
            "peer credentials are unavailable",
        ))
    }

    pub fn send_fds(&self, fds: [BorrowedFd<'_>; 3]) -> io::Result<()> {
        self.arm()?;
        let raw: [RawFd; 3] = [fds[0].as_raw_fd(), fds[1].as_raw_fd(), fds[2].as_raw_fd()];
        let payload = [1u8];
        let mut iov = libc::iovec {
            iov_base: payload.as_ptr() as *mut _,
            iov_len: 1,
        };
        // SAFETY: CMSG_SPACE only computes a size.
        let space = unsafe { libc::CMSG_SPACE(std::mem::size_of_val(&raw) as u32) } as usize;
        let mut control = vec![0u8; space];
        // SAFETY: zero-initialized msghdr is a valid starting value.
        let mut msg: libc::msghdr = unsafe { std::mem::zeroed() };
        msg.msg_iov = &raw mut iov;
        msg.msg_iovlen = 1;
        msg.msg_control = control.as_mut_ptr().cast();
        msg.msg_controllen = space as _;
        // SAFETY: `msg` points at `control`, which has room for one header
        // carrying three descriptors.
        unsafe {
            let header = libc::CMSG_FIRSTHDR(&raw const msg);
            (*header).cmsg_level = libc::SOL_SOCKET;
            (*header).cmsg_type = libc::SCM_RIGHTS;
            (*header).cmsg_len = libc::CMSG_LEN(std::mem::size_of_val(&raw) as u32) as _;
            std::ptr::copy_nonoverlapping(raw.as_ptr(), libc::CMSG_DATA(header).cast::<RawFd>(), 3);
        }
        loop {
            // SAFETY: `msg` and its buffers stay alive for the call.
            let n = unsafe { libc::sendmsg(self.socket.as_raw_fd(), &raw const msg, 0) };
            if n >= 0 {
                return Ok(());
            }
            let e = io::Error::last_os_error();
            if e.kind() != io::ErrorKind::Interrupted {
                return Err(e);
            }
        }
    }

    pub fn recv_fds(&self) -> Result<[OwnedFd; 3]> {
        self.arm()?;
        let mut payload = [0u8; 1];
        let mut iov = libc::iovec {
            iov_base: payload.as_mut_ptr().cast(),
            iov_len: 1,
        };
        // SAFETY: CMSG_SPACE only computes a size.
        let space = unsafe { libc::CMSG_SPACE((3 * std::mem::size_of::<RawFd>()) as u32) } as usize;
        let mut control = vec![0u8; space];
        // SAFETY: zero-initialized msghdr is a valid starting value.
        let mut msg: libc::msghdr = unsafe { std::mem::zeroed() };
        msg.msg_iov = &raw mut iov;
        msg.msg_iovlen = 1;
        msg.msg_control = control.as_mut_ptr().cast();
        msg.msg_controllen = space as _;
        #[cfg(target_os = "linux")]
        let flags = libc::MSG_CMSG_CLOEXEC;
        #[cfg(not(target_os = "linux"))]
        let flags = 0;
        let n = loop {
            // SAFETY: `msg` and its buffers stay alive for the call.
            let n = unsafe { libc::recvmsg(self.socket.as_raw_fd(), &raw mut msg, flags) };
            if n >= 0 {
                break n;
            }
            let e = io::Error::last_os_error();
            if e.kind() != io::ErrorKind::Interrupted {
                return Err(e.into());
            }
        };
        let mut received: Vec<OwnedFd> = Vec::new();
        // SAFETY: the kernel filled `control` with well-formed headers bounded
        // by `msg_controllen`; CMSG_NXTHDR stays inside that buffer.
        unsafe {
            let mut header = libc::CMSG_FIRSTHDR(&raw const msg);
            while !header.is_null() {
                if (*header).cmsg_level == libc::SOL_SOCKET
                    && (*header).cmsg_type == libc::SCM_RIGHTS
                {
                    let data = libc::CMSG_DATA(header).cast::<RawFd>();
                    let bytes = (*header).cmsg_len as usize - (data as usize - header as usize);
                    for i in 0..bytes / std::mem::size_of::<RawFd>() {
                        received.push(OwnedFd::from_raw_fd(std::ptr::read_unaligned(data.add(i))));
                    }
                }
                header = libc::CMSG_NXTHDR(&raw const msg, header);
            }
        }
        if n != 1 || msg.msg_flags & libc::MSG_CTRUNC != 0 || received.len() != 3 {
            return Err(anyhow::Error::new(ProcessError::Unavailable));
        }
        for fd in &received {
            // SAFETY: `fd` is an open descriptor owned by `received`.
            unsafe { libc::fcntl(fd.as_fd().as_raw_fd(), libc::F_SETFD, libc::FD_CLOEXEC) };
        }
        let mut it = received.into_iter();
        match (it.next(), it.next(), it.next()) {
            (Some(a), Some(b), Some(c)) => Ok([a, b, c]),
            _ => Err(anyhow::Error::new(ProcessError::Unavailable)),
        }
    }
}
