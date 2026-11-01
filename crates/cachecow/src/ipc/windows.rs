// SPDX-License-Identifier: Apache-2.0
//! Named-pipe transport restricted to the current user's SID. Every operation
//! uses overlapped I/O so deadlines and cross-thread close can interrupt it.

use std::{
    ffi::OsStr,
    io,
    os::windows::ffi::OsStrExt,
    sync::{
        Mutex,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};

use anyhow::Result;
use windows_sys::Win32::{
    Foundation::{
        CloseHandle, ERROR_BROKEN_PIPE, ERROR_HANDLE_EOF, ERROR_IO_PENDING, ERROR_PIPE_BUSY,
        ERROR_PIPE_CONNECTED, ERROR_PIPE_NOT_CONNECTED, GENERIC_READ, GENERIC_WRITE, GetLastError,
        HANDLE, INVALID_HANDLE_VALUE, LocalFree, WAIT_OBJECT_0, WAIT_TIMEOUT,
    },
    Security::{
        Authorization::{ConvertStringSecurityDescriptorToSecurityDescriptorW, SDDL_REVISION_1},
        PSECURITY_DESCRIPTOR, SECURITY_ATTRIBUTES,
    },
    Storage::FileSystem::{
        CreateFileW, FILE_FLAG_FIRST_PIPE_INSTANCE, FILE_FLAG_OVERLAPPED, OPEN_EXISTING,
        PIPE_ACCESS_DUPLEX, ReadFile, SECURITY_IDENTIFICATION, SECURITY_SQOS_PRESENT, WriteFile,
    },
    System::{
        IO::{CancelIoEx, GetOverlappedResult, OVERLAPPED},
        Pipes::{
            ConnectNamedPipe, CreateNamedPipeW, DisconnectNamedPipe, GetNamedPipeClientProcessId,
            PIPE_READMODE_BYTE, PIPE_REJECT_REMOTE_CLIENTS, PIPE_TYPE_BYTE,
            PIPE_UNLIMITED_INSTANCES, PIPE_WAIT, WaitNamedPipeW,
        },
        Threading::{CreateEventW, INFINITE, SetEvent, WaitForMultipleObjects},
    },
};

use super::closed_error;

/// A raw handle that may cross threads; access is coordinated by its owner.
#[derive(Clone, Copy)]
struct Raw(HANDLE);
// SAFETY: kernel handles are process-wide values; owners serialize lifetime.
unsafe impl Send for Raw {}
// SAFETY: as above; concurrent overlapped I/O on one handle is supported.
unsafe impl Sync for Raw {}

fn wide(s: &str) -> Vec<u16> {
    OsStr::new(s).encode_wide().chain(Some(0)).collect()
}

fn event() -> io::Result<Raw> {
    // SAFETY: creates an unnamed manual-reset event with default security.
    let h = unsafe { CreateEventW(std::ptr::null(), 1, 0, std::ptr::null()) };
    if h.is_null() {
        return Err(io::Error::last_os_error());
    }
    Ok(Raw(h))
}

fn pipe_sddl() -> Result<String> {
    let user = crate::winsec::current_user()?.to_string_sid()?;
    Ok(format!("D:P(A;;GA;;;{user})"))
}

pub struct Listener {
    name: Vec<u16>,
    sddl: Vec<u16>,
    pending: Mutex<Option<Raw>>,
    close_event: Raw,
    closed: AtomicBool,
}

impl Listener {
    fn instance(&self, first: bool) -> io::Result<Raw> {
        let mut descriptor: PSECURITY_DESCRIPTOR = std::ptr::null_mut();
        // SAFETY: `sddl` is NUL-terminated; the descriptor is freed below.
        if unsafe {
            ConvertStringSecurityDescriptorToSecurityDescriptorW(
                self.sddl.as_ptr(),
                SDDL_REVISION_1,
                &mut descriptor,
                std::ptr::null_mut(),
            )
        } == 0
        {
            return Err(io::Error::last_os_error());
        }
        let attributes = SECURITY_ATTRIBUTES {
            nLength: std::mem::size_of::<SECURITY_ATTRIBUTES>() as u32,
            lpSecurityDescriptor: descriptor,
            bInheritHandle: 0,
        };
        let mut open = PIPE_ACCESS_DUPLEX | FILE_FLAG_OVERLAPPED;
        if first {
            open |= FILE_FLAG_FIRST_PIPE_INSTANCE;
        }
        // SAFETY: `name` is NUL-terminated and `attributes` is valid for the
        // call.
        let h = unsafe {
            CreateNamedPipeW(
                self.name.as_ptr(),
                open,
                PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT | PIPE_REJECT_REMOTE_CLIENTS,
                PIPE_UNLIMITED_INSTANCES,
                65536,
                65536,
                0,
                &attributes,
            )
        };
        let error = io::Error::last_os_error();
        // SAFETY: `descriptor` was allocated by the conversion above.
        unsafe { LocalFree(descriptor) };
        if h == INVALID_HANDLE_VALUE {
            return Err(error);
        }
        Ok(Raw(h))
    }

    pub fn bind(prefix: &str) -> Result<(Self, String)> {
        let endpoint = format!(r"\\.\pipe\{prefix}{}", uuid::Uuid::now_v7());
        let listener = Self {
            name: wide(&endpoint),
            sddl: wide(&pipe_sddl()?),
            pending: Mutex::new(None),
            close_event: event()?,
            closed: AtomicBool::new(false),
        };
        let first = listener.instance(true)?;
        *listener
            .pending
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner) = Some(first);
        Ok((listener, endpoint))
    }

    pub fn accept(&self) -> Result<Stream> {
        if self.closed.load(Ordering::SeqCst) {
            return Err(closed_error().into());
        }
        let taken = self
            .pending
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .take();
        let pipe = match taken {
            Some(p) => p,
            None => self.instance(false)?,
        };
        let done = event()?;
        // SAFETY: zeroed OVERLAPPED is valid; it lives until the operation
        // ends.
        let mut ov: OVERLAPPED = unsafe { std::mem::zeroed() };
        ov.hEvent = done.0;
        // SAFETY: `pipe` is a server pipe instance and `ov` outlives the I/O.
        let connected = unsafe { ConnectNamedPipe(pipe.0, &mut ov) } != 0;
        // SAFETY: reads the calling thread's last error.
        let error = unsafe { GetLastError() };
        let result = if connected || error == ERROR_PIPE_CONNECTED {
            Ok(())
        } else if error != ERROR_IO_PENDING {
            Err(io::Error::from_raw_os_error(error as i32))
        } else {
            let handles = [done.0, self.close_event.0];
            // SAFETY: both handles are live events.
            let wait = unsafe { WaitForMultipleObjects(2, handles.as_ptr(), 0, INFINITE) };
            if wait != WAIT_OBJECT_0 {
                // SAFETY: cancels this instance's pending connect.
                unsafe { CancelIoEx(pipe.0, &ov) };
            }
            let mut n = 0u32;
            // SAFETY: waits for the (possibly cancelled) operation to finish
            // before `ov` leaves scope.
            let ok = unsafe { GetOverlappedResult(pipe.0, &ov, &mut n, 1) } != 0;
            if wait != WAIT_OBJECT_0 {
                Err(closed_error())
            } else if ok {
                Ok(())
            } else {
                Err(io::Error::last_os_error())
            }
        };
        // SAFETY: `done` was created above and is no longer used.
        unsafe { CloseHandle(done.0) };
        if let Err(e) = result {
            // SAFETY: the failed instance is owned here and closed once.
            unsafe { CloseHandle(pipe.0) };
            return Err(e.into());
        }
        if !self.closed.load(Ordering::SeqCst)
            && let Ok(next) = self.instance(false)
        {
            *self
                .pending
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner) = Some(next);
        }
        Ok(Stream::new(pipe, true)?)
    }

    pub fn close(&self) {
        if self.closed.swap(true, Ordering::SeqCst) {
            return;
        }
        // SAFETY: `close_event` is a live event owned by this listener.
        unsafe { SetEvent(self.close_event.0) };
    }
}

impl Drop for Listener {
    fn drop(&mut self) {
        if let Some(p) = self
            .pending
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .take()
        {
            // SAFETY: the unused instance is owned here and closed once.
            unsafe { CloseHandle(p.0) };
        }
        // SAFETY: the event is owned here and closed once.
        unsafe { CloseHandle(self.close_event.0) };
    }
}

pub struct Stream {
    handle: Raw,
    close_event: Raw,
    server: bool,
    deadline: Mutex<Option<Instant>>,
    closed: AtomicBool,
}

impl Stream {
    fn new(handle: Raw, server: bool) -> io::Result<Self> {
        let close_event = match event() {
            Ok(e) => e,
            Err(e) => {
                // SAFETY: `handle` is owned here and closed once on failure.
                unsafe { CloseHandle(handle.0) };
                return Err(e);
            }
        };
        Ok(Self {
            handle,
            close_event,
            server,
            deadline: Mutex::new(None),
            closed: AtomicBool::new(false),
        })
    }

    pub fn dial(endpoint: &str, timeout: Duration) -> Result<Self> {
        let name = wide(endpoint);
        let deadline = Instant::now() + timeout;
        loop {
            // SAFETY: `name` is NUL-terminated; the handle is owned on success.
            let h = unsafe {
                CreateFileW(
                    name.as_ptr(),
                    GENERIC_READ | GENERIC_WRITE,
                    0,
                    std::ptr::null(),
                    OPEN_EXISTING,
                    FILE_FLAG_OVERLAPPED | SECURITY_SQOS_PRESENT | SECURITY_IDENTIFICATION,
                    std::ptr::null_mut(),
                )
            };
            if h != INVALID_HANDLE_VALUE {
                return Ok(Self::new(Raw(h), false)?);
            }
            // SAFETY: reads the calling thread's last error.
            let error = unsafe { GetLastError() };
            if error != ERROR_PIPE_BUSY {
                return Err(io::Error::from_raw_os_error(error as i32).into());
            }
            let now = Instant::now();
            if now >= deadline {
                return Err(io::Error::new(io::ErrorKind::TimedOut, "named pipe busy").into());
            }
            let ms = u32::try_from((deadline - now).as_millis())
                .unwrap_or(u32::MAX)
                .max(1);
            // SAFETY: `name` is NUL-terminated.
            unsafe { WaitNamedPipeW(name.as_ptr(), ms) };
        }
    }

    fn overlapped(&self, start: impl FnOnce(*mut OVERLAPPED) -> i32) -> io::Result<u32> {
        if self.closed.load(Ordering::SeqCst) {
            return Err(closed_error());
        }
        let deadline = *self
            .deadline
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        let timeout = match deadline {
            None => INFINITE,
            Some(d) => {
                let now = Instant::now();
                if now >= d {
                    return Err(io::Error::new(
                        io::ErrorKind::TimedOut,
                        "IPC deadline exceeded",
                    ));
                }
                u32::try_from((d - now).as_millis())
                    .unwrap_or(INFINITE - 1)
                    .max(1)
            }
        };
        let done = event()?;
        // SAFETY: zeroed OVERLAPPED is valid; it lives until the operation
        // ends.
        let mut ov: OVERLAPPED = unsafe { std::mem::zeroed() };
        ov.hEvent = done.0;
        let ok = start(&mut ov) != 0;
        // SAFETY: reads the calling thread's last error.
        let error = unsafe { GetLastError() };
        if !ok && error != ERROR_IO_PENDING {
            // SAFETY: `done` is owned here.
            unsafe { CloseHandle(done.0) };
            return Err(io::Error::from_raw_os_error(error as i32));
        }
        let handles = [done.0, self.close_event.0];
        // SAFETY: both handles are live events.
        let wait = unsafe { WaitForMultipleObjects(2, handles.as_ptr(), 0, timeout) };
        if wait != WAIT_OBJECT_0 {
            // SAFETY: cancels only this operation.
            unsafe { CancelIoEx(self.handle.0, &ov) };
        }
        let mut n = 0u32;
        // SAFETY: waits for completion before `ov` leaves scope.
        let finished = unsafe { GetOverlappedResult(self.handle.0, &ov, &mut n, 1) } != 0;
        // SAFETY: reads the calling thread's last error.
        let result_error = unsafe { GetLastError() };
        // SAFETY: `done` is owned here.
        unsafe { CloseHandle(done.0) };
        if wait == WAIT_TIMEOUT {
            return Err(io::Error::new(
                io::ErrorKind::TimedOut,
                "IPC deadline exceeded",
            ));
        }
        if wait != WAIT_OBJECT_0 {
            return Err(closed_error());
        }
        if !finished {
            return Err(io::Error::from_raw_os_error(result_error as i32));
        }
        Ok(n)
    }

    pub fn read(&self, buf: &mut [u8]) -> io::Result<usize> {
        let len = u32::try_from(buf.len()).unwrap_or(u32::MAX);
        let ptr = buf.as_mut_ptr();
        let handle = self.handle.0;
        // SAFETY: `buf` stays borrowed until `overlapped` returns.
        match self.overlapped(|ov| unsafe { ReadFile(handle, ptr, len, std::ptr::null_mut(), ov) })
        {
            Ok(n) => Ok(n as usize),
            Err(e)
                if matches!(
                    e.raw_os_error().map(|c| c as u32),
                    Some(ERROR_BROKEN_PIPE | ERROR_PIPE_NOT_CONNECTED | ERROR_HANDLE_EOF)
                ) =>
            {
                Ok(0)
            }
            Err(e) => Err(e),
        }
    }

    pub fn write_all(&self, mut buf: &[u8]) -> io::Result<()> {
        while !buf.is_empty() {
            let len = u32::try_from(buf.len()).unwrap_or(u32::MAX);
            let ptr = buf.as_ptr();
            let handle = self.handle.0;
            // SAFETY: `buf` stays borrowed until `overlapped` returns.
            let n = self.overlapped(|ov| unsafe {
                WriteFile(handle, ptr, len, std::ptr::null_mut(), ov)
            })?;
            if n == 0 {
                return Err(io::Error::from(io::ErrorKind::WriteZero));
            }
            buf = &buf[n as usize..];
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
        if self.closed.swap(true, Ordering::SeqCst) {
            return;
        }
        // SAFETY: the event and pipe handle stay open until drop.
        unsafe {
            SetEvent(self.close_event.0);
            CancelIoEx(self.handle.0, std::ptr::null());
            if self.server {
                DisconnectNamedPipe(self.handle.0);
            }
        }
    }

    pub fn peer_pid(&self) -> io::Result<i64> {
        let mut pid = 0u32;
        // SAFETY: `pid` is a valid output location for a server pipe handle.
        if unsafe { GetNamedPipeClientProcessId(self.handle.0, &mut pid) } == 0 {
            return Err(io::Error::last_os_error());
        }
        Ok(i64::from(pid))
    }
}

impl Drop for Stream {
    fn drop(&mut self) {
        // SAFETY: both handles are owned by this stream and closed once.
        unsafe {
            CloseHandle(self.handle.0);
            CloseHandle(self.close_event.0);
        }
    }
}
