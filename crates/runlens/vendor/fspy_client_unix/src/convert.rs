use allocator_api2::{alloc::Allocator, vec::Vec};
use fspy_nostd::{BorrowedFd, CWD};
use fspy_shared::ipc::AccessMode;
use libc::{c_char, c_int};

#[cfg(target_os = "linux")]
fn get_fd_path<A: Allocator>(allocator: A, fd: BorrowedFd<'_>) -> nix::Result<Option<Vec<u8, A>>> {
    if fd.as_raw_fd() == CWD.as_raw_fd() {
        let path = fspy_nostd_alloc::fs::getcwd(allocator)
            .map_err(|errno| nix::errno::Errno::from_raw(errno.raw_os_error()))?
            .into_units();
        return Ok(Some(path));
    }
    let mut path = [0; PROC_FD_PATH_CAPACITY];
    let path = proc_fd_path(fd, &mut path);
    match fspy_nostd_alloc::fs::readlinkat(allocator, CWD, path) {
        Ok(path) => Ok(Some(path)),
        Err(fspy_nostd::Error::BADF | fspy_nostd::Error::NOENT) => Ok(None),
        Err(errno) => Err(nix::errno::Errno::from_raw(errno.raw_os_error())),
    }
}

#[cfg(target_os = "linux")]
const PROC_FD_PATH_CAPACITY: usize =
    b"/proc/self/fd/".len() + <c_int as itoa::Integer>::MAX_STR_LEN + 1;

#[cfg(target_os = "linux")]
fn proc_fd_path<'buf>(
    fd: BorrowedFd<'_>,
    buf: &'buf mut [u8; PROC_FD_PATH_CAPACITY],
) -> fspy_nostd::CStr<'buf, fspy_nostd::Thin> {
    const PREFIX: &[u8] = b"/proc/self/fd/";

    let mut formatted = itoa::Buffer::new();
    let fd = formatted.format(fd.as_raw_fd()).as_bytes();

    buf[..PREFIX.len()].copy_from_slice(PREFIX);
    let end = PREFIX.len() + fd.len();
    buf[PREFIX.len()..end].copy_from_slice(fd);
    buf[end] = 0;

    // SAFETY: the initialized prefix ends in NUL and lives as long as `buf`.
    unsafe { fspy_nostd::CStr::from_ptr(buf.as_ptr().cast()) }
}

#[cfg(target_os = "macos")]
fn get_fd_path<A: Allocator>(allocator: A, fd: BorrowedFd<'_>) -> nix::Result<Option<Vec<u8, A>>> {
    if fd.as_raw_fd() == CWD.as_raw_fd() {
        let path = fspy_nostd_alloc::fs::getcwd(allocator)
            .map_err(|errno| nix::errno::Errno::from_raw(errno.raw_os_error()))?
            .into_units();
        return Ok(Some(path));
    }

    match fspy_nostd_alloc::fs::fcntl_getpath(allocator, fd) {
        Ok(path) => {
            // `F_GETPATH` does not return a length. Count at this caller before
            // converting its allocation into the returned path.
            Ok(Some(path.count().into_units()))
        }
        Err(fspy_nostd::Error::BADF | fspy_nostd::Error::NOENT) => Ok(None),
        Err(errno) => Err(nix::errno::Errno::from_raw(errno.raw_os_error())),
    }
}

pub trait ToAbsolutePath {
    /// Resolves this argument to an absolute path allocated in `allocator`,
    /// after copying intercepted pathname memory into bounded owned storage.
    ///
    /// The result is a C string so that callers forwarding it to an exec —
    /// which needs a terminator — cannot be handed unterminated bytes;
    /// [`as_units`] gives the path without the NUL.
    ///
    /// [`as_units`]: fspy_nostd::CStr::as_units
    ///
    /// # Errors
    ///
    /// Returns the error reported while resolving a descriptor or the current
    /// working directory.
    fn to_absolute_path<'a, A: Allocator>(
        self,
        allocator: &'a A,
    ) -> nix::Result<Option<fspy_nostd::CStr<'a, fspy_nostd::Fat>>>
    where
        Self: 'a;
}

impl ToAbsolutePath for BorrowedFd<'_> {
    fn to_absolute_path<'a, A: Allocator>(
        self,
        allocator: &'a A,
    ) -> nix::Result<Option<fspy_nostd::CStr<'a, fspy_nostd::Fat>>>
    where
        Self: 'a,
    {
        let Some(mut path) = get_fd_path(allocator, self)? else {
            return Ok(None);
        };
        path.push(0);
        // SAFETY: a resolved descriptor path carries no interior NUL, and
        // exactly one was appended above. The storage stays in `allocator`
        // until it is dropped, which for a per-call arena ends the call.
        Ok(Some(unsafe { fspy_nostd::CStr::from_units_with_nul_unchecked(path.leak()) }))
    }
}

/// Untrusted pathname operands. Construction never dereferences caller memory.
pub struct PathAt(pub c_int, pub *const c_char);
impl PathAt {
    /// Keep the native operands until bounded OS-assisted copying. No pointer
    /// validity or descriptor ownership is inferred from an intercepted call.
    #[must_use]
    pub const unsafe fn borrow_raw(fd: c_int, path: *const c_char) -> Self { Self(fd, path) }
}

// A caller can pass invalid memory that the kernel would reject with EFAULT.
// Copy in sub-page chunks to owned storage, stopping at NUL without touching the
// next page. Supported OS page sizes are multiples of 256 bytes. Never scan the
// original pointer, and never let a changing mapping become a Rust reference.
fn copy_cstr(path: *const c_char, output: &mut [u8]) -> nix::Result<usize> {
    let mut used = 0;
    while used < output.len() {
        let address = (path as usize).checked_add(used).ok_or(nix::errno::Errno::EFAULT)?;
        let count = (256 - address % 256).min(output.len() - used);
        let destination = &mut output[used..used + count];
        #[cfg(target_os = "linux")]
        let read = {
            let local = libc::iovec { iov_base: destination.as_mut_ptr().cast(), iov_len: count };
            let remote = libc::iovec { iov_base: address as *mut libc::c_void, iov_len: count };
            // SAFETY: the kernel validates the remote address and copies only
            // into our live destination; raw syscall avoids preload recursion.
            let result = unsafe { libc::syscall(libc::SYS_process_vm_readv, libc::getpid(), &local, 1_usize, &remote, 1_usize, 0_usize) };
            if result <= 0 { return Err(nix::errno::Errno::EFAULT); }
            result as usize
        };
        #[cfg(target_os = "macos")]
        let read = {
            unsafe extern "C" {
                static mach_task_self_: libc::mach_port_t;
                fn mach_vm_read_overwrite(task: libc::mach_port_t, address: u64, size: u64, data: u64, outsize: *mut u64) -> libc::kern_return_t;
            }
            let mut copied = 0;
            // SAFETY: Mach validates the source address; the destination and
            // exact output-size field are valid process-owned storage.
            let result = unsafe { mach_vm_read_overwrite(mach_task_self_, address as u64, count as u64, destination.as_mut_ptr() as u64, &mut copied) };
            if result != 0 || copied == 0 { return Err(nix::errno::Errno::EFAULT); }
            copied as usize
        };
        if read > count { return Err(nix::errno::Errno::EFAULT); }
        if let Some(nul) = destination[..read].iter().position(|byte| *byte == 0) { return Ok(used + nul + 1); }
        used += read;
    }
    Err(nix::errno::Errno::ENAMETOOLONG)
}

impl ToAbsolutePath for PathAt {
    fn to_absolute_path<'a, A: Allocator>(
        self,
        allocator: &'a A,
    ) -> nix::Result<Option<fspy_nostd::CStr<'a, fspy_nostd::Fat>>>
    where
        Self: 'a,
    {
        let mut buffer = [0_u8; libc::PATH_MAX as usize];
        let length = copy_cstr(self.1, &mut buffer)?;
        let mut copied = Vec::new_in(allocator);
        copied.extend_from_slice(&buffer[..length]);
        // SAFETY: only the bounded owned copy is viewed as a C string.
        let counted = unsafe { fspy_nostd::CStr::from_units_with_nul_unchecked(copied.leak()) };
        let pathname = counted.as_units();

        if pathname.starts_with(b"/") {
            // The owned copy is already absolute and NUL-terminated.
            Ok(Some(counted))
        } else {
            if self.0 < 0 && self.0 != CWD.as_raw_fd() { return Err(nix::errno::Errno::EBADF); }
            // SAFETY: a nonnegative raw value or CWD is inspected without taking
            // ownership. The kernel validates whether that descriptor is open.
            let fd = unsafe { BorrowedFd::borrow_raw(self.0) };
            let Some(mut base) = get_fd_path(allocator, fd)? else {
                return Ok(None);
            };
            if !pathname.is_empty() {
                if !base.ends_with(b"/") {
                    base.push(b'/');
                }
                base.extend_from_slice(pathname);
            }
            base.push(0);
            // SAFETY: neither the directory nor the pathname carries an
            // interior NUL — both come from C strings or the kernel — and
            // exactly one was appended above. The storage stays in
            // `allocator` until it is dropped.
            Ok(Some(unsafe { fspy_nostd::CStr::from_units_with_nul_unchecked(base.leak()) }))
        }
    }
}

impl ToAbsolutePath for fspy_nostd::CStr<'_, fspy_nostd::Thin> {
    fn to_absolute_path<'a, A: Allocator>(
        self,
        allocator: &'a A,
    ) -> nix::Result<Option<fspy_nostd::CStr<'a, fspy_nostd::Fat>>>
    where
        Self: 'a,
    {
        PathAt(CWD.as_raw_fd(), self.as_ptr().cast()).to_absolute_path(allocator)
    }
}

pub trait ToAccessMode {
    /// Converts the intercepted operation's mode into an access mode.
    ///
    /// # Safety
    ///
    /// The conversion may inspect a caller address through a bounded OS copy;
    /// unreadable strings return an error without creating a Rust reference.
    unsafe fn to_access_mode(self) -> nix::Result<AccessMode>;
}

impl ToAccessMode for AccessMode {
    unsafe fn to_access_mode(self) -> nix::Result<AccessMode> {
        Ok(self)
    }
}

pub struct OpenFlags(pub c_int);
impl ToAccessMode for OpenFlags {
    unsafe fn to_access_mode(self) -> nix::Result<AccessMode> {
        Ok(fspy_shared_unix::access::open_flags(self.0))
    }
}

pub struct ModeStr(pub *const c_char);
impl ToAccessMode for ModeStr {
    unsafe fn to_access_mode(self) -> nix::Result<AccessMode> {
        let mut buffer = [0_u8; 64];
        let length = copy_cstr(self.0, &mut buffer)?;
        Ok(fspy_shared_unix::access::stream_mode(&buffer[..length - 1]))
    }
}
