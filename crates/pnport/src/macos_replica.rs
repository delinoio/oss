// SPDX-License-Identifier: Apache-2.0
//! Bounded admission history retained independently of supervisor scheduling.
use std::{
    cell::UnsafeCell,
    fs::File,
    io, mem,
    os::{fd::AsRawFd, unix::fs::MetadataExt},
    ptr::NonNull,
    sync::atomic::{AtomicU32, Ordering},
};

use pnport_core::macos_process::Identity;

pub const CAPACITY: usize = 65536;
const FRAME: usize = 1024;

#[repr(C)]
struct Slot {
    length: UnsafeCell<u32>,
    bytes: UnsafeCell<[u8; FRAME]>,
}

#[repr(C)]
struct History {
    published: AtomicU32,
    slots: [Slot; CAPACITY],
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Access {
    Reader,
    Writer,
}

pub struct Replica {
    file: File,
    history: NonNull<History>,
    access: Access,
    cursor: usize,
}

fn invalid() -> io::Error {
    io::Error::other("Private admission history is unavailable")
}

impl Replica {
    pub fn create() -> io::Result<Self> {
        // tempfile() unlinks the backing file before returning. No workload
        // pathname can delete/truncate history, and only the guardian inherits
        // its descriptor. Reserve the complete bounded history before launch.
        let file = tempfile::tempfile()?;
        file.set_len(mem::size_of::<History>() as u64)?;
        let mut replica = Self::map(file, Access::Writer)?;
        unsafe {
            (&raw mut (*replica.history.as_ptr()).published).write(AtomicU32::new(0));
            if libc::mprotect(
                replica.history.as_ptr().cast(),
                mem::size_of::<History>(),
                libc::PROT_READ,
            ) != 0
            {
                return Err(io::Error::last_os_error());
            }
        }
        replica.access = Access::Reader;
        Ok(replica)
    }

    // Called only after authenticating the inherited control socket's native
    // parent credentials and same executable. This descriptor is never supplied
    // by a user command, and its unlinked backing is checked before mapping.
    pub fn writer(file: File) -> io::Result<Self> {
        Self::map(file, Access::Writer)
    }

    fn map(file: File, access: Access) -> io::Result<Self> {
        let metadata = file.metadata()?;
        if !metadata.is_file()
            || metadata.nlink() != 0
            || metadata.uid() != unsafe { libc::getuid() }
            || metadata.mode() & 0o777 != 0o600
            || metadata.len() != mem::size_of::<History>() as u64
        {
            return Err(invalid());
        }
        if unsafe { libc::fcntl(file.as_raw_fd(), libc::F_SETFD, libc::FD_CLOEXEC) } < 0 {
            return Err(io::Error::last_os_error());
        }
        let mapped = unsafe {
            libc::mmap(
                std::ptr::null_mut(),
                mem::size_of::<History>(),
                libc::PROT_READ | libc::PROT_WRITE,
                libc::MAP_SHARED,
                file.as_raw_fd(),
                0,
            )
        };
        if mapped == libc::MAP_FAILED {
            return Err(io::Error::last_os_error());
        }
        Ok(Self {
            file,
            history: NonNull::new(mapped.cast()).ok_or_else(invalid)?,
            access,
            cursor: 0,
        })
    }

    pub fn descriptor(&self) -> i32 {
        self.file.as_raw_fd()
    }

    pub fn append(&mut self, identity: Identity) -> io::Result<()> {
        if self.access != Access::Writer {
            return Err(invalid());
        }
        let bytes = serde_json::to_vec(&identity)?;
        let history = unsafe { self.history.as_ref() };
        let index = history.published.load(Ordering::Acquire) as usize;
        if index >= CAPACITY || bytes.is_empty() || bytes.len() > FRAME {
            return Err(invalid());
        }
        let slot = &history.slots[index];
        unsafe {
            std::ptr::copy_nonoverlapping(bytes.as_ptr(), slot.bytes.get().cast(), bytes.len());
            slot.length.get().write(bytes.len() as u32);
        }
        // Exactly one authenticated guardian writes. A release publishes the
        // entire immutable slot before the signed admission ACK. No reader ACK,
        // socket capacity or active supervisor scheduling is required.
        history
            .published
            .store((index + 1) as u32, Ordering::Release);
        Ok(())
    }

    pub fn next(&mut self) -> io::Result<Option<Identity>> {
        if self.access != Access::Reader {
            return Err(invalid());
        }
        let history = unsafe { self.history.as_ref() };
        let count = history.published.load(Ordering::Acquire) as usize;
        if count > CAPACITY || self.cursor > count {
            return Err(invalid());
        }
        if self.cursor == count {
            return Ok(None);
        }
        let slot = &history.slots[self.cursor];
        // The acquired count includes only complete immutable slots. A killed
        // guardian's partial final slot cannot grant admission or ownership.
        let length = unsafe { *slot.length.get() } as usize;
        if length == 0 || length > FRAME {
            return Err(invalid());
        }
        let bytes = unsafe { std::slice::from_raw_parts(slot.bytes.get().cast(), length) };
        let identity = serde_json::from_slice(bytes)?;
        self.cursor += 1;
        Ok(Some(identity))
    }
}

impl Drop for Replica {
    fn drop(&mut self) {
        unsafe {
            libc::munmap(self.history.as_ptr().cast(), mem::size_of::<History>());
        }
    }
}
