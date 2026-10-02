// These hooks preserve pnport's C ABI while registering in fspy's Mach-O
// interpose section. Keep the lint scoped to this imported 2021-era hook
// module until each unsafe libc operation is migrated to an explicit block.
#![allow(
    unsafe_op_in_unsafe_fn,
    reason = "the imported C ABI hook bodies still use the Rust 2021 unsafe convention"
)]

use std::{
    cell::Cell,
    collections::HashMap,
    ffi::{CStr, CString, OsStr, OsString},
    fs,
    os::unix::ffi::{OsStrExt, OsStringExt},
    path::{Path, PathBuf},
    ptr,
    sync::{
        Arc, Mutex, OnceLock,
        atomic::{AtomicBool, Ordering},
    },
};

mod fork_mutex;
use fork_mutex::ForkMutex;
#[cfg(test)]
mod fcntl_tests;
#[cfg(target_os = "linux")]
use libc::{__errno_location, RTLD_NEXT, dlsym};
use libc::{
    __error, _exit, AT_FDCWD, AT_SYMLINK_NOFOLLOW, DIR, EBADF, EEXIST, EFAULT, EINVAL, EIO,
    ENAMETOOLONG, ENOENT, ENOMEM, ENOTDIR, ENOTSUP, ERANGE, EROFS, F_GETPATH, FILE, O_APPEND,
    O_CREAT, O_RDWR, O_TRUNC, O_WRONLY, PATH_MAX, S_IFDIR, S_IFLNK, S_IFMT, W_OK, c_char, c_int,
    c_void, dirent, dirfd, fcntl, fileno, free, fstat, getpid, malloc, mode_t, off_t, pid_t,
    posix_spawn_file_actions_t, posix_spawnattr_t, size_t, ssize_t, stat, strcpy,
};
use pnport_core::{
    cache::Cache,
    diagnostic::Code,
    executable::LaunchAdmission,
    graph::{Graph, Snapshot},
    view::{Translation, View},
};

thread_local! { static INSIDE: Cell<bool> = const { Cell::new(false) }; }
// dyld may call interposed libc functions while libSystem is still
// initializing. Rust TLS is unavailable at that point. Enable the recursion
// guard only from our initializer, after dependency initializers have finished.
static TLS_READY: AtomicBool = AtomicBool::new(false);
struct Guard;
impl Guard {
    fn enter() -> Option<Self> {
        if !TLS_READY.load(Ordering::Acquire) {
            return None;
        }
        // Construct lazily: then_some would drop its eagerly created Guard
        // on reentry and clear the outer guard while its runtime lock is held.
        INSIDE.with(|inside| (!inside.replace(true)).then(|| Self))
    }
}
impl Drop for Guard {
    fn drop(&mut self) {
        INSIDE.with(|inside| inside.set(false));
    }
}

// User scandir callbacks may perform virtual filesystem operations. They run
// without our recursion guard; no runtime or native stream lock is held there.
struct CallbackGuard;
impl CallbackGuard {
    fn enter() -> Self {
        INSIDE.with(|inside| inside.set(false));
        Self
    }
}
impl Drop for CallbackGuard {
    fn drop(&mut self) {
        INSIDE.with(|inside| inside.set(true));
    }
}

#[cfg(test)]
mod guard_tests {
    use std::sync::atomic::Ordering;

    use super::{Guard, TLS_READY};

    #[test]
    fn repeated_reentry_keeps_the_outer_guard_active() {
        assert!(TLS_READY.load(Ordering::Acquire));
        let outer = Guard::enter().expect("enter the outer hook");
        assert!(Guard::enter().is_none());
        super::fcntl_tests::check_native_forwarding(|| assert!(Guard::enter().is_none()));
        assert!(Guard::enter().is_none());
        drop(outer);
        assert!(Guard::enter().is_some());
    }
}

struct Runtime {
    view: View,
    descriptors: HashMap<c_int, Translation>,
    cwd: Option<PathBuf>,
    directories: HashMap<usize, Arc<Mutex<DirectoryStream>>>,
}

struct DirectoryStream {
    logical: PathBuf,
    physical: PathBuf,
    emitted: bool,
    virtual_end: bool,
    entry: Box<dirent>,
}

const DIRECTORY_END: libc::c_long = libc::c_long::MAX;

unsafe fn track_directory(dir: *mut DIR, translation: Translation) {
    if let Some(runtime) = RUNTIME.get()
        && let Ok(mut runtime) = runtime.lock()
    {
        runtime.descriptors.insert(dirfd(dir), translation.clone());
        runtime.directories.insert(
            dir as usize,
            Arc::new(Mutex::new(DirectoryStream {
                logical: translation.logical,
                physical: translation.physical,
                emitted: false,
                virtual_end: false,
                entry: Box::new(std::mem::zeroed()),
            })),
        );
    }
}

fn directory_stream(dir: *mut DIR) -> Option<Arc<Mutex<DirectoryStream>>> {
    RUNTIME
        .get()?
        .lock()
        .ok()?
        .directories
        .get(&(dir as usize))
        .cloned()
}

fn directory_eligible(stream: &DirectoryStream) -> std::result::Result<bool, c_int> {
    let runtime = RUNTIME.get().ok_or(EIO)?.lock().map_err(|_| EIO)?;
    let eligible = runtime
        .view
        .virtual_directory_entry(&stream.logical)
        .map(|entry| entry.is_some())
        .map_err(|error| fail(error.code))?;
    drop(runtime);
    if !eligible {
        return Ok(false);
    }
    // ZIP package content may already contain this directory. Its native
    // entry works at every native seek position and needs no appended entry.
    match fs::symlink_metadata(stream.physical.join("node_modules")) {
        Ok(metadata) if metadata.is_dir() => Ok(false),
        Ok(_) => Err(fail(Code::PnportFilesystemConflict)),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(true),
        Err(_) => Err(EIO),
    }
}

fn directory_entry(stream: &mut DirectoryStream) -> *mut dirent {
    let name = b"node_modules\0";
    stream.entry.d_ino = 1;
    stream.entry.d_seekoff = DIRECTORY_END as u64;
    stream.entry.d_type = libc::DT_DIR;
    stream.entry.d_namlen = 12;
    stream.entry.d_reclen =
        u16::try_from((std::mem::offset_of!(dirent, d_name) + name.len()).next_multiple_of(4))
            .expect("The fixed virtual dirent fits its length field");
    for (slot, byte) in stream.entry.d_name.iter_mut().zip(name) {
        *slot = byte.cast_signed();
    }
    stream.emitted = true;
    stream.virtual_end = true;
    &raw mut *stream.entry
}
static RUNTIME: OnceLock<Box<ForkMutex<Runtime>>> = OnceLock::new();
static SESSION: OnceLock<PathBuf> = OnceLock::new();

unsafe extern "C" fn before_fork() {
    // Wait for all runtime operations to finish before libSystem copies the
    // address space. A plain Rust mutex can otherwise retain a vanished owner.
    if let Some(runtime) = RUNTIME.get()
        && runtime.prepare().is_err()
    {
        _exit(125);
    }
}

unsafe extern "C" fn after_fork() {
    if let Some(runtime) = RUNTIME.get() {
        runtime.release();
    }
}

unsafe extern "C" fn in_fork_child() {
    after_fork();
    // A dlopen initializer may fork from inside the outer hook. Its child
    // must start intercepting immediately, including later child callbacks.
    INSIDE.with(|inside| inside.set(false));
}

unsafe fn errno(value: c_int) {
    #[cfg(target_os = "macos")]
    {
        *__error() = value;
    }
    #[cfg(target_os = "linux")]
    {
        *__errno_location() = value;
    }
}
fn fail(code: Code) -> c_int {
    if !matches!(
        code,
        Code::PnportResolutionFailed | Code::PnportCommandNotFound
    ) && let Some(session) = SESSION.get()
    {
        let _ = fs::write(session.join("failure"), code.as_str());
    }
    match code {
        Code::PnportResolutionFailed | Code::PnportCommandNotFound => ENOENT,
        Code::PnportFilesystemConflict => EEXIST,
        Code::PnportUnsupportedOperation => ENOTSUP,
        _ => EIO,
    }
}

unsafe fn path_bytes(path: *const c_char) -> std::result::Result<Vec<u8>, c_int> {
    if path.is_null() {
        return Err(EFAULT);
    }
    #[cfg(target_os = "macos")]
    {
        let mut bytes = Vec::new();
        for index in 0..=PATH_MAX as usize {
            let mut byte = 0u8;
            let mut copied = 0;
            #[expect(deprecated, reason = "the injected client avoids a mach2 dependency")]
            let status = mach_vm_read_overwrite(
                libc::mach_task_self_,
                path.cast::<u8>().add(index) as u64,
                1,
                (&raw mut byte) as u64,
                &raw mut copied,
            );
            if status != libc::KERN_SUCCESS || copied != 1 {
                return Err(EFAULT);
            }
            if byte == 0 {
                return Ok(bytes);
            }
            if index == PATH_MAX as usize {
                return Err(ENAMETOOLONG);
            }
            bytes.push(byte);
        }
        unreachable!("the bounded pathname reader always returns");
    }
    #[cfg(not(target_os = "macos"))]
    {
        Ok(CStr::from_ptr(path).to_bytes().to_vec())
    }
}

unsafe fn path_from(
    path: *const c_char,
    dirfd: c_int,
    runtime: &Runtime,
) -> std::result::Result<PathBuf, c_int> {
    let path_bytes = path_bytes(path)?;
    let path = Path::new(OsStr::from_bytes(&path_bytes));
    if path.as_os_str().is_empty() {
        return Err(ENOENT);
    }
    if path.is_absolute() {
        return Ok(path.to_owned());
    }
    if dirfd != AT_FDCWD {
        // Check the live kernel descriptor before normalizing '..'. Checking
        // only a remembered path would treat a regular file as a directory;
        // live metadata also covers duplicated and untracked descriptors.
        let mut metadata = std::mem::MaybeUninit::<stat>::uninit();
        if fstat(dirfd, metadata.as_mut_ptr()) != 0 {
            return Err(std::io::Error::last_os_error()
                .raw_os_error()
                .unwrap_or(EBADF));
        }
        let metadata = metadata.assume_init();
        if metadata.st_mode & S_IFMT != S_IFDIR {
            return Err(ENOTDIR);
        }
    }
    let base = if dirfd == AT_FDCWD {
        runtime
            .cwd
            .clone()
            .or_else(|| std::env::current_dir().ok())
            .ok_or(EIO)?
    } else if let Some(translation) = runtime.descriptors.get(&dirfd) {
        if translation.readonly {
            translation.logical.clone()
        } else {
            live_directory_path(dirfd)?
        }
    } else {
        live_directory_path(dirfd)?
    };
    Ok(base.join(path))
}

unsafe fn live_directory_path(dirfd: c_int) -> std::result::Result<PathBuf, c_int> {
    #[cfg(target_os = "macos")]
    {
        let mut buffer = [0u8; PATH_MAX as usize];
        if fcntl(dirfd, F_GETPATH, buffer.as_mut_ptr()) < 0 {
            return Err(EBADF);
        }
        return Ok(PathBuf::from(OsStr::from_bytes(
            CStr::from_ptr(buffer.as_ptr().cast()).to_bytes(),
        )));
    }
    #[cfg(target_os = "linux")]
    {
        fs::read_link(format!("/proc/self/fd/{dirfd}")).map_err(|_| EBADF)
    }
}

#[cfg(target_os = "macos")]
unsafe extern "C" fn pnport_fcntl(fd: c_int, command: c_int, mut args: ...) -> c_int {
    let original = libc::fcntl as unsafe extern "C" fn(c_int, c_int, ...) -> c_int;
    let guard = Guard::enter();

    // Only duplication carries logical directory provenance. An inode lookup
    // cannot distinguish an fcntl duplicate from an independent open of shared
    // peer backing, so record the duplicate at the actual duplication call.
    let result = match command {
        libc::F_DUPFD
        | libc::F_DUPFD_CLOEXEC
        | libc::F_SETFD
        | libc::F_SETFL
        | libc::F_RDAHEAD
        | libc::F_NOCACHE
        | libc::F_FREEZE_FS
        | libc::F_THAW_FS
        | libc::F_GLOBAL_NOCACHE
        | libc::F_NODIRECT => {
            let argument = args.arg::<c_int>();
            original(fd, command, argument)
        }
        libc::F_GETLK
        | libc::F_SETLK
        | libc::F_SETLKW
        | libc::F_PREALLOCATE
        | libc::F_RDADVISE
        | libc::F_LOG2PHYS
        | libc::F_LOG2PHYS_EXT
        | libc::F_GETPATH
        | libc::F_GETPATH_NOFIRMLINK
        | libc::F_PUNCHHOLE
        | libc::F_TRIM_ACTIVE_FILE
        | libc::F_SPECULATIVE_READ
        | libc::F_TRANSFEREXTENTS => {
            let argument = args.arg::<*mut c_void>();
            original(fd, command, argument)
        }
        libc::F_GETFD | libc::F_GETFL | libc::F_FULLFSYNC | libc::F_BARRIERFSYNC => {
            original(fd, command)
        }
        // Keep the native ABI for future integer-valued Darwin commands until
        // libc exposes a typed constant for them.
        _ => original(fd, command, args.arg::<c_int>()),
    };
    // Reentry and early dyld calls still need the command's native variadic
    // ABI. Only bypass bookkeeping after forwarding; keep an admitted token
    // alive through the runtime lock and never construct one on rejection.
    let Some(_guard) = guard else {
        return result;
    };
    if result >= 0
        && matches!(command, libc::F_DUPFD | libc::F_DUPFD_CLOEXEC)
        && let Some(runtime) = RUNTIME.get()
        && let Ok(mut runtime) = runtime.lock()
        && let Some(translation) = runtime.descriptors.get(&fd).cloned()
    {
        runtime.descriptors.insert(result, translation);
    }
    result
}

#[cfg(target_os = "macos")]
const _: () = {
    #[used]
    #[unsafe(link_section = "__DATA,__interpose")]
    static mut ENTRY: crate::macros::InterposeEntry = crate::macros::InterposeEntry {
        _new: pnport_fcntl as _,
        _old: libc::fcntl as _,
    };
};

unsafe fn translate(
    path: *const c_char,
    dirfd: c_int,
    write: bool,
) -> std::result::Result<(CString, Translation), c_int> {
    let Some(runtime) = RUNTIME.get() else {
        return Err(EIO);
    };
    let mut runtime = runtime.lock().map_err(|_| EIO)?;
    let path = path_from(path, dirfd, &runtime)?;
    let translation = runtime
        .view
        .translate(&path)
        .map_err(|error| fail(error.code))?;
    if write && translation.readonly {
        return Err(EROFS);
    }
    drop(runtime);
    let physical = CString::new(translation.physical.as_os_str().as_bytes()).map_err(|_| EINVAL)?;
    Ok((physical, translation))
}
unsafe fn track(fd: c_int, translation: Translation) {
    if fd >= 0
        && let Some(runtime) = RUNTIME.get()
        && let Ok(mut runtime) = runtime.lock()
    {
        runtime.descriptors.insert(fd, translation);
    }
}

// Adapted from the pinned fspy Mach-O interpose macro; see vendor/fspy.
macro_rules! hook {
    ($name:ident, $wrapper:ident, ($($arg:ident: $ty:ty),*) -> $ret:ty, $body:block) => {
        unsafe extern "C" fn $wrapper($($arg: $ty),*) -> $ret $body
        #[cfg(target_os = "macos")]
        const _: () = {
            #[used]
            #[unsafe(link_section = "__DATA,__interpose")]
            static mut ENTRY: crate::macros::InterposeEntry = crate::macros::InterposeEntry {
                _new: $wrapper as *const c_void,
                _old: libc::$name as *const c_void,
            };
        };
        #[cfg(target_os = "linux")]
        #[unsafe(export_name = stringify!($name))]
        unsafe extern "C" fn $name($($arg: $ty),*) -> $ret { $wrapper($($arg),*) }
    };
}
// macOS calls from inside an interposing image resolve to the original symbol.
// Linux requires RTLD_NEXT; keep its typed lookup behind the recursion guard.
macro_rules! original {
    ($name:ident, $ty:ty) => {{
        #[cfg(target_os = "macos")]
        {
            libc::$name as $ty
        }
        #[cfg(target_os = "linux")]
        {
            static POINTER: OnceLock<usize> = OnceLock::new();
            let pointer = *POINTER.get_or_init(|| unsafe {
                dlsym(RTLD_NEXT, concat!(stringify!($name), "\0").as_ptr().cast()) as usize
            });
            if pointer == 0 {
                _exit(125);
            }
            std::mem::transmute::<usize, $ty>(pointer)
        }
    }};
}
macro_rules! translated {
    ($path:ident, $dir:expr, $write:expr, $failure:expr) => {
        match translate($path, $dir, $write) {
            Ok(value) => value,
            Err(error) => {
                errno(error);
                return $failure;
            }
        }
    };
}

unsafe extern "C" fn initialize() {
    TLS_READY.store(true, Ordering::Release);
    let Some(_guard) = Guard::enter() else {
        return;
    };
    let Some(session) = std::env::var_os("PNPORT_SESSION") else {
        return;
    };
    let session = PathBuf::from(session);
    SESSION.set(session.clone()).ok();
    let mut injection_env = Vec::new();
    for name in [
        "PNPORT_SESSION",
        "PNPORT_CACHE",
        "DYLD_INSERT_LIBRARIES",
        "LD_PRELOAD",
    ] {
        if let Some(value) = std::env::var_os(name) {
            let mut entry = name.as_bytes().to_vec();
            entry.push(b'=');
            entry.extend_from_slice(value.as_os_str().as_bytes());
            if let Ok(entry) = CString::new(entry) {
                injection_env.push(entry);
            }
        }
    }
    INJECTION_ENV.set(injection_env).ok();
    let result = (|| {
        // Acknowledge entry before graph/cache work that may legitimately wait
        // on another materializer. Only the later ready marker confirms setup.
        fs::create_dir_all(session.join("starting")).ok()?;
        fs::write(session.join("starting").join(getpid().to_string()), b"1").ok()?;
        let snapshot: Snapshot =
            serde_json::from_slice(&fs::read(session.join("graph.json")).ok()?).ok()?;
        let graph = Graph::from_snapshot(snapshot).ok()?;
        let cache = Cache::open(PathBuf::from(std::env::var_os("PNPORT_CACHE")?)).ok()?;
        let view = View::new(graph, cache, session.clone());
        RUNTIME
            .set(
                ForkMutex::new(Runtime {
                    view,
                    descriptors: HashMap::new(),
                    cwd: None,
                    directories: HashMap::new(),
                })
                .ok()?,
            )
            .ok()?;
        if libc::pthread_atfork(Some(before_fork), Some(after_fork), Some(in_fork_child)) != 0 {
            return None;
        }
        fs::create_dir_all(session.join("ready")).ok()?;
        fs::write(session.join("ready").join(getpid().to_string()), b"1").ok()?;
        if let Some(token) = std::env::var_os("PNPORT_LAUNCH_TOKEN") {
            let token = token.to_str()?;
            if !token.starts_with("pnport-")
                || !token
                    .bytes()
                    .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-')
            {
                return None;
            }
            fs::remove_file(session.join("pending").join(token)).ok()?;
        }
        Some(())
    })();
    if result.is_none() {
        let _ = fs::write(session.join("failure"), b"PNPORT_INJECTION_FAILED");
        _exit(125);
    }
}
#[used]
#[cfg_attr(target_os = "macos", unsafe(link_section = "__DATA,__mod_init_func"))]
#[cfg_attr(target_os = "linux", unsafe(link_section = ".init_array"))]
static INITIALIZER: unsafe extern "C" fn() = initialize;

unsafe extern "C" fn pnport_open(path: *const c_char, flags: c_int, mut args: ...) -> c_int {
    let mode = if flags & O_CREAT != 0 {
        args.arg::<c_int>()
    } else {
        0
    };
    let original = original!(
        open,
        unsafe extern "C" fn(*const c_char, c_int, ...) -> c_int
    );
    let Some(_guard) = Guard::enter() else {
        return original(path, flags, mode);
    };
    if RUNTIME.get().is_none() {
        return original(path, flags, mode);
    }
    let (path, translation) = translated!(
        path,
        AT_FDCWD,
        flags & (O_WRONLY | O_RDWR | O_CREAT | O_TRUNC | O_APPEND) != 0,
        -1
    );
    let fd = original(path.as_ptr(), flags, mode);
    track(fd, translation);
    fd
}
#[cfg(target_os = "macos")]
const _: () = {
    #[used]
    #[unsafe(link_section = "__DATA,__interpose")]
    static mut ENTRY: crate::macros::InterposeEntry = crate::macros::InterposeEntry {
        _new: pnport_open as _,
        _old: libc::open as _,
    };
};
#[cfg(target_os = "linux")]
#[unsafe(export_name = "open")]
unsafe extern "C" fn linux_open(path: *const c_char, flags: c_int, mut args: ...) -> c_int {
    let mode = if flags & O_CREAT != 0 {
        args.arg::<c_int>()
    } else {
        0
    };
    pnport_open(path, flags, mode)
}
unsafe extern "C" fn pnport_openat(
    dirfd: c_int,
    path: *const c_char,
    flags: c_int,
    mut args: ...
) -> c_int {
    let mode = if flags & O_CREAT != 0 {
        args.arg::<c_int>()
    } else {
        0
    };
    let original = original!(
        openat,
        unsafe extern "C" fn(c_int, *const c_char, c_int, ...) -> c_int
    );
    let Some(_guard) = Guard::enter() else {
        return original(dirfd, path, flags, mode);
    };
    if RUNTIME.get().is_none() {
        return original(dirfd, path, flags, mode);
    }
    let (path, translation) = translated!(
        path,
        dirfd,
        flags & (O_WRONLY | O_RDWR | O_CREAT | O_TRUNC | O_APPEND) != 0,
        -1
    );
    let fd = original(AT_FDCWD, path.as_ptr(), flags, mode);
    track(fd, translation);
    fd
}
#[cfg(target_os = "macos")]
const _: () = {
    #[used]
    #[unsafe(link_section = "__DATA,__interpose")]
    static mut ENTRY: crate::macros::InterposeEntry = crate::macros::InterposeEntry {
        _new: pnport_openat as _,
        _old: libc::openat as _,
    };
};
#[cfg(target_os = "linux")]
#[unsafe(export_name = "openat")]
unsafe extern "C" fn linux_openat(
    fd: c_int,
    path: *const c_char,
    flags: c_int,
    mut args: ...
) -> c_int {
    let mode = if flags & O_CREAT != 0 {
        args.arg::<c_int>()
    } else {
        0
    };
    pnport_openat(fd, path, flags, mode)
}

macro_rules! path_hook {
    ($name:ident, $wrapper:ident, ($path:ident: *const c_char $(,$arg:ident: $ty:ty)*) -> $ret:ty, $write:expr, $failure:expr) => {
        hook!($name, $wrapper, ($path:*const c_char $(,$arg:$ty)*) -> $ret, {
            let original = original!($name, unsafe extern "C" fn(*const c_char $(,$ty)*) -> $ret);
            let Some(_guard) = Guard::enter() else { return original($path $(,$arg)*); };
            if RUNTIME.get().is_none() { return original($path $(,$arg)*); }
            let (path, _) = translated!($path, AT_FDCWD, $write, $failure);
            original(path.as_ptr() $(,$arg)*)
        });
    };
}
path_hook!(stat, pnport_stat, (path:*const c_char, output:*mut stat) -> c_int, false, -1);

path_hook!(access, pnport_access, (path:*const c_char, mode:c_int) -> c_int, mode & W_OK != 0, -1);

path_hook!(unlink, pnport_unlink, (path:*const c_char) -> c_int, true, -1);
path_hook!(rmdir, pnport_rmdir, (path:*const c_char) -> c_int, true, -1);
path_hook!(mkdir, pnport_mkdir, (path:*const c_char, mode:mode_t) -> c_int, true, -1);
path_hook!(chmod, pnport_chmod, (path:*const c_char, mode:mode_t) -> c_int, true, -1);
path_hook!(truncate, pnport_truncate, (path:*const c_char, length:off_t) -> c_int, true, -1);
hook!(dlopen, pnport_dlopen, (path:*const c_char,flags:c_int) -> *mut c_void, {
    let original = original!(dlopen, unsafe extern "C" fn(*const c_char,c_int)->*mut c_void);
    // NULL requests the process/global symbol namespace, not a filesystem path.
    if path.is_null() { return original(path,flags); }
    let Some(_guard) = Guard::enter() else { return original(path,flags); };
    if RUNTIME.get().is_none() { return original(path,flags); }
    let (path,_) = translated!(path,AT_FDCWD,false,ptr::null_mut());
    original(path.as_ptr(),flags)
});

unsafe fn virtual_link_metadata(output: *mut stat, translation: &Translation) {
    if translation.virtual_link {
        (*output).st_mode = ((*output).st_mode & !S_IFMT) | S_IFLNK;
        (*output).st_size =
            off_t::try_from(translation.logical.as_os_str().as_bytes().len()).unwrap_or(off_t::MAX);
    }
}

hook!(fstatat, pnport_fstatat, (dirfd:c_int,path:*const c_char,output:*mut stat,flags:c_int) -> c_int, {
    let original = original!(fstatat, unsafe extern "C" fn(c_int,*const c_char,*mut stat,c_int)->c_int);
    let Some(_guard) = Guard::enter() else { return original(dirfd,path,output,flags); };
    if RUNTIME.get().is_none() { return original(dirfd,path,output,flags); }
    let (path,translation) = translated!(path,dirfd,false,-1);
    let result = original(AT_FDCWD,path.as_ptr(),output,flags);
    if result == 0 && flags & AT_SYMLINK_NOFOLLOW != 0 { virtual_link_metadata(output,&translation); }
    result
});
hook!(fopen, pnport_fopen, (path:*const c_char, mode:*const c_char) -> *mut FILE, {
    let original = original!(fopen, unsafe extern "C" fn(*const c_char,*const c_char)->*mut FILE);
    let Some(_guard) = Guard::enter() else { return original(path,mode); };
    if RUNTIME.get().is_none() { return original(path,mode); }
    if mode.is_null() { errno(EINVAL); return ptr::null_mut(); }
    let write = CStr::from_ptr(mode).to_bytes().iter().any(|c| matches!(c,b'w'|b'a'|b'+'));
    let (path,translation) = translated!(path,AT_FDCWD,write,ptr::null_mut());
    let file = original(path.as_ptr(),mode);
    if !file.is_null() { track(fileno(file),translation); } file
});
hook!(realpath, pnport_realpath, (path:*const c_char, output:*mut c_char) -> *mut c_char, {
    let original = original!(realpath, unsafe extern "C" fn(*const c_char,*mut c_char)->*mut c_char);
    let Some(_guard) = Guard::enter() else { return original(path,output); };
    if RUNTIME.get().is_none() { return original(path,output); }
    let (physical,translation) = translated!(path,AT_FDCWD,false,ptr::null_mut());
    let actual = original(physical.as_ptr(),ptr::null_mut());
    if actual.is_null() { return actual; }
    if !translation.readonly { if output.is_null() { return actual; } strcpy(output,actual); free(actual.cast()); return output; }
    free(actual.cast());
    let bytes = translation.logical.as_os_str().as_bytes();
    if bytes.len() >= PATH_MAX as usize { errno(ENAMETOOLONG); return ptr::null_mut(); }
    let output = if output.is_null() { malloc(bytes.len()+1).cast::<c_char>() } else { output };
    if output.is_null() { errno(ENOMEM); return output; }
    ptr::copy_nonoverlapping(bytes.as_ptr(),output.cast(),bytes.len()); *output.add(bytes.len())=0; output
});
unsafe extern "C" {
    fn mach_vm_read_overwrite(
        target_task: libc::mach_port_t,
        address: libc::mach_vm_address_t,
        size: libc::mach_vm_size_t,
        data: libc::mach_vm_address_t,
        outsize: *mut libc::mach_vm_size_t,
    ) -> libc::kern_return_t;
}

unsafe fn read_logical_link(
    translation: &Translation,
    output: *mut c_char,
    size: size_t,
) -> ssize_t {
    let bytes = translation.logical.as_os_str().as_bytes();
    let count = size.min(bytes.len());
    // Darwin permits an empty buffer, even an invalid pointer, for a link.
    if count == 0 {
        return 0;
    }
    // Kernel copyout validates the caller's destination, including read-only
    // mappings. A Rust pointer copy would crash instead of returning EFAULT.
    // Keep the same narrow Mach ABI as the generic fspy pointer reader until
    // the preload adopts a shared Mach binding dependency.
    let mut copied = 0;
    #[expect(deprecated, reason = "the injected client avoids a mach2 dependency")]
    let status = mach_vm_read_overwrite(
        libc::mach_task_self_,
        bytes.as_ptr() as u64,
        count as u64,
        output as u64,
        &raw mut copied,
    );
    if status != libc::KERN_SUCCESS || copied != count as u64 {
        errno(EFAULT);
        return -1;
    }
    count.cast_signed()
}

unsafe fn terminal_lookup_suffix(
    path: *const c_char,
) -> std::result::Result<Option<&'static [u8]>, c_int> {
    let bytes = path_bytes(path)?;
    Ok(if bytes.ends_with(b"/.") {
        Some(b"/.")
    } else if bytes.last() == Some(&b'/') {
        Some(b"/")
    } else {
        None
    })
}

fn append_terminal_lookup(
    physical: &CString,
    suffix: &[u8],
) -> std::result::Result<CString, c_int> {
    let mut bytes = physical.as_bytes().to_vec();
    bytes.extend_from_slice(suffix);
    CString::new(bytes).map_err(|_| EINVAL)
}

hook!(readlink, pnport_readlink, (path:*const c_char, output:*mut c_char, size:size_t) -> ssize_t, {
    let original = original!(readlink,unsafe extern "C" fn(*const c_char,*mut c_char,size_t)->ssize_t);
    let Some(_guard) = Guard::enter() else { return original(path,output,size); };
    if RUNTIME.get().is_none() { return original(path,output,size); }
    // Darwin rejects oversized buffers before looking up the pathname.
    if size > c_int::MAX as usize { errno(EINVAL); return -1; }
    let terminal = match terminal_lookup_suffix(path) {
        Ok(value) => value,
        Err(error) => { errno(error); return -1; }
    };
    let (physical,translation) = translated!(path,AT_FDCWD,false,-1);
    if let Some(suffix) = terminal {
        let physical = match append_terminal_lookup(&physical, suffix) {
            Ok(value) => value,
            Err(error) => { errno(error); return -1; }
        };
        return original(physical.as_ptr(), output, size);
    }
    if !translation.virtual_link { return original(physical.as_ptr(),output,size); }
    read_logical_link(&translation, output, size)
});
hook!(readlinkat, pnport_readlinkat, (dirfd:c_int, path:*const c_char, output:*mut c_char, size:size_t) -> ssize_t, {
    let original = original!(readlinkat,unsafe extern "C" fn(c_int,*const c_char,*mut c_char,size_t)->ssize_t);
    let Some(_guard) = Guard::enter() else { return original(dirfd,path,output,size); };
    if RUNTIME.get().is_none() { return original(dirfd,path,output,size); }
    if size > c_int::MAX as usize { errno(EINVAL); return -1; }
    let terminal = match terminal_lookup_suffix(path) {
        Ok(value) => value,
        Err(error) => { errno(error); return -1; }
    };
    let (physical,translation) = translated!(path,dirfd,false,-1);
    if let Some(suffix) = terminal {
        let physical = match append_terminal_lookup(&physical, suffix) {
            Ok(value) => value,
            Err(error) => { errno(error); return -1; }
        };
        return original(AT_FDCWD, physical.as_ptr(), output, size);
    }
    if !translation.virtual_link { return original(AT_FDCWD,physical.as_ptr(),output,size); }
    read_logical_link(&translation, output, size)
});
hook!(chdir, pnport_chdir, (path:*const c_char) -> c_int, {
    let original = original!(chdir, unsafe extern "C" fn(*const c_char)->c_int);
    let Some(_guard) = Guard::enter() else { return original(path); };
    if RUNTIME.get().is_none() { return original(path); }
    let (path,translation) = translated!(path,AT_FDCWD,false,-1);
    let result = original(path.as_ptr());
    if result == 0 && let Ok(mut runtime) = RUNTIME.get().unwrap().lock() { runtime.cwd=Some(translation.logical); } result
});
hook!(getcwd, pnport_getcwd, (buffer:*mut c_char,size:size_t) -> *mut c_char, {
    let original = original!(getcwd,unsafe extern "C" fn(*mut c_char,size_t)->*mut c_char);
    let Some(_guard) = Guard::enter() else { return original(buffer,size); };
    let path = RUNTIME.get().and_then(|r| r.lock().ok()?.cwd.clone());
    let Some(path)=path else { return original(buffer,size); };
    let bytes=path.as_os_str().as_bytes(); let needed=bytes.len()+1;
    if !buffer.is_null() && size<needed { errno(ERANGE);return ptr::null_mut(); }
    let buffer=if buffer.is_null() {malloc(if size==0 {needed} else {if size<needed {errno(ERANGE);return ptr::null_mut();} size}).cast()} else {buffer};
    if buffer.is_null() {errno(ENOMEM);return buffer;}
    ptr::copy_nonoverlapping(bytes.as_ptr(),buffer.cast(),bytes.len());*buffer.add(bytes.len())=0;buffer
});
hook!(close, pnport_close, (fd:c_int) -> c_int, {
    let original=original!(close,unsafe extern "C" fn(c_int)->c_int);
    let Some(_guard)=Guard::enter() else {return original(fd);};
    if let Some(runtime)=RUNTIME.get() && let Ok(mut runtime)=runtime.lock() {runtime.descriptors.remove(&fd);}
    original(fd)
});

hook!(opendir, pnport_opendir, (path:*const c_char) -> *mut DIR, {
    let original=original!(opendir,unsafe extern "C" fn(*const c_char)->*mut DIR);
    let Some(_guard)=Guard::enter() else {return original(path);};
    if RUNTIME.get().is_none() {return original(path);}
    let (path,translation)=translated!(path,AT_FDCWD,false,ptr::null_mut());
    let dir=original(path.as_ptr());if !dir.is_null() {track_directory(dir,translation);} dir
});

hook!(fdopendir, pnport_fdopendir, (fd:c_int) -> *mut DIR, {
    let original=original!(fdopendir,unsafe extern "C" fn(c_int)->*mut DIR);
    let Some(_guard)=Guard::enter() else {return original(fd);};
    if RUNTIME.get().is_none() {return original(fd);}
    let translation=match translate(c".".as_ptr(),fd,false) {Ok((_,value))=>value,Err(code)=>{errno(code);return ptr::null_mut();}};
    let dir=original(fd);if !dir.is_null() {track_directory(dir,translation);} dir
});

hook!(readdir, pnport_readdir, (dir:*mut DIR) -> *mut dirent, {
    let original=original!(readdir,unsafe extern "C" fn(*mut DIR)->*mut dirent);
    let Some(_guard)=Guard::enter() else {return original(dir);};
    let Some(stream)=directory_stream(dir) else {return original(dir);};
    let saved=*__error();
    let Ok(mut stream)=stream.lock() else {errno(EIO);return ptr::null_mut();};
    let eligible=match directory_eligible(&stream) {Ok(value)=>value,Err(code)=>{errno(code);return ptr::null_mut();}};
    if stream.virtual_end {errno(saved);return ptr::null_mut();}
    // Guard remains active while libc holds its stream lock. Any interposed
    // backing operations reenter only the original libc, never RUNTIME.
    errno(0);
    let entry=original(dir);
    if !entry.is_null() {
        if eligible && CStr::from_ptr((*entry).d_name.as_ptr()).to_bytes()==b"node_modules" {stream.emitted=true;}
        errno(saved);return entry;
    }
    if *__error()!=0 {return entry;}
    errno(saved);
    if eligible && !stream.emitted {directory_entry(&mut stream)} else {entry}
});

hook!(readdir_r, pnport_readdir_r, (dir:*mut DIR,entry:*mut dirent,result:*mut *mut dirent) -> c_int, {
    let original=original!(readdir_r,unsafe extern "C" fn(*mut DIR,*mut dirent,*mut *mut dirent)->c_int);
    let Some(_guard)=Guard::enter() else {return original(dir,entry,result);};
    let Some(stream)=directory_stream(dir) else {return original(dir,entry,result);};
    let saved=*__error();
    let Ok(mut stream)=stream.lock() else {return EIO;};
    let eligible=match directory_eligible(&stream) {Ok(value)=>value,Err(code)=>{errno(saved);return code;}};
    if stream.virtual_end {*result=ptr::null_mut();errno(saved);return 0;}
    let code=original(dir,entry,result);
    if code==0 {
        if !(*result).is_null() {
            if eligible && CStr::from_ptr((*entry).d_name.as_ptr()).to_bytes()==b"node_modules" {stream.emitted=true;}
        } else if eligible && !stream.emitted {
            ptr::copy_nonoverlapping(directory_entry(&mut stream),entry,1);*result=entry;
        }
    }
    errno(saved);code
});

hook!(rewinddir, pnport_rewinddir, (dir:*mut DIR) -> (), {
    let original=original!(rewinddir,unsafe extern "C" fn(*mut DIR));
    let Some(_guard)=Guard::enter() else {return original(dir);};
    let stream=directory_stream(dir);
    let mut state=stream.as_ref().and_then(|stream|stream.lock().ok());
    if let Some(state)=state.as_mut() {state.emitted=false;state.virtual_end=false;}
    original(dir);
});
hook!(telldir, pnport_telldir, (dir:*mut DIR) -> libc::c_long, {
    let original=original!(telldir,unsafe extern "C" fn(*mut DIR)->libc::c_long);
    let Some(_guard)=Guard::enter() else {return original(dir);};
    if let Some(stream)=directory_stream(dir) && let Ok(stream)=stream.lock() && stream.virtual_end {return DIRECTORY_END;}
    original(dir)
});
hook!(seekdir, pnport_seekdir, (dir:*mut DIR,position:libc::c_long) -> (), {
    let original=original!(seekdir,unsafe extern "C" fn(*mut DIR,libc::c_long));
    let Some(_guard)=Guard::enter() else {return original(dir,position);};
    let stream=directory_stream(dir);
    let mut state=stream.as_ref().and_then(|stream|stream.lock().ok());
    if let Some(state)=state.as_mut() {
        state.emitted=position==DIRECTORY_END;
        state.virtual_end=state.emitted;
        if state.emitted {return;}
    }
    original(dir,position);
});
hook!(closedir, pnport_closedir, (dir:*mut DIR) -> c_int, {
    let original=original!(closedir,unsafe extern "C" fn(*mut DIR)->c_int);
    let Some(_guard)=Guard::enter() else {return original(dir);};
    if let Some(runtime)=RUNTIME.get() && let Ok(mut runtime)=runtime.lock() {
        runtime.directories.remove(&(dir as usize));runtime.descriptors.remove(&dirfd(dir));
    }
    original(dir)
});

unsafe extern "C" fn pnport_scandir(
    path: *const c_char,
    namelist: *mut c_void,
    select: *const c_void,
    compar: *const c_void,
) -> c_int {
    let Some(_guard) = Guard::enter() else {
        return crate::libc::scandir(path, namelist, select, compar);
    };
    if RUNTIME.get().is_none() {
        return crate::libc::scandir(path, namelist, select, compar);
    }
    let (path, translation) = translated!(path, AT_FDCWD, false, -1);
    let mut stream = DirectoryStream {
        logical: translation.logical,
        physical: translation.physical,
        emitted: false,
        virtual_end: false,
        entry: Box::new(std::mem::zeroed()),
    };
    let eligible = match directory_eligible(&stream) {
        Ok(value) => value,
        Err(code) => {
            errno(code);
            return -1;
        }
    };
    if !eligible {
        let _callback_guard = CallbackGuard::enter();
        return crate::libc::scandir(path.as_ptr(), namelist, select, compar);
    }
    let open = original!(opendir, unsafe extern "C" fn(*const c_char) -> *mut DIR);
    let read = original!(readdir, unsafe extern "C" fn(*mut DIR) -> *mut dirent);
    let close = original!(closedir, unsafe extern "C" fn(*mut DIR) -> c_int);
    let dir = open(path.as_ptr());
    if dir.is_null() {
        return -1;
    }
    let mut entries: Vec<*mut dirent> = Vec::new();
    let result = (|| -> std::result::Result<(), c_int> {
        loop {
            errno(0);
            let mut entry = read(dir);
            if entry.is_null() {
                if *__error() != 0 {
                    return Err(*__error());
                }
                if stream.emitted {
                    break;
                }
                if !directory_eligible(&stream)? {
                    break;
                }
                entry = directory_entry(&mut stream);
            } else if CStr::from_ptr((*entry).d_name.as_ptr()).to_bytes() == b"node_modules" {
                stream.emitted = true;
            }
            let keep = if select.is_null() {
                true
            } else {
                let callback: unsafe extern "C" fn(*const dirent) -> c_int =
                    std::mem::transmute(select);
                let _callback_guard = CallbackGuard::enter();
                callback(entry) != 0
            };
            if keep {
                entries.try_reserve(1).map_err(|_| ENOMEM)?;
                let len = usize::from((*entry).d_reclen);
                let copy = malloc(len).cast::<dirent>();
                if copy.is_null() {
                    return Err(ENOMEM);
                }
                // readdir's buffer contains variable-length records, not a
                // full dirent for every entry. Copy only the declared record.
                ptr::copy_nonoverlapping(entry.cast::<u8>(), copy.cast::<u8>(), len);
                entries.push(copy);
            }
            if stream.virtual_end {
                break;
            }
        }
        Ok(())
    })();
    close(dir);
    if let Err(code) = result {
        for entry in entries {
            free(entry.cast());
        }
        errno(code);
        return -1;
    }
    publish_directory_scan(entries, namelist, compar)
}

unsafe fn publish_directory_scan(
    entries: Vec<*mut dirent>,
    namelist: *mut c_void,
    compar: *const c_void,
) -> c_int {
    let Ok(count) = c_int::try_from(entries.len()) else {
        for entry in entries {
            free(entry.cast());
        }
        errno(ENOMEM);
        return -1;
    };
    let list = malloc(entries.len() * std::mem::size_of::<*mut dirent>()).cast::<*mut dirent>();
    if list.is_null() && !entries.is_empty() {
        for entry in entries {
            free(entry.cast());
        }
        errno(ENOMEM);
        return -1;
    }
    if !entries.is_empty() {
        ptr::copy_nonoverlapping(entries.as_ptr(), list, entries.len());
        if !compar.is_null() {
            let callback: unsafe extern "C" fn(*const c_void, *const c_void) -> c_int =
                std::mem::transmute(compar);
            let _callback_guard = CallbackGuard::enter();
            libc::qsort(
                list.cast(),
                entries.len(),
                std::mem::size_of::<*mut dirent>(),
                Some(callback),
            );
        }
    }
    *namelist.cast::<*mut *mut dirent>() = list;
    count
}

const _: () = {
    #[used]
    #[unsafe(link_section = "__DATA,__interpose")]
    static mut ENTRY: crate::macros::InterposeEntry = crate::macros::InterposeEntry {
        _new: pnport_scandir as *const c_void,
        _old: crate::libc::scandir as *const c_void,
    };
};
hook!(lstat, pnport_lstat, (path:*const c_char,output:*mut stat) -> c_int, {
    let original=original!(lstat,unsafe extern "C" fn(*const c_char,*mut stat)->c_int);
    let Some(_guard)=Guard::enter() else {return original(path,output);};
    if RUNTIME.get().is_none() {return original(path,output);}
    let (path,translation)=translated!(path,AT_FDCWD,false,-1);
    let result=original(path.as_ptr(),output);
    if result==0 {virtual_link_metadata(output,&translation);} result
});
hook!(rename, pnport_rename, (from:*const c_char,to:*const c_char) -> c_int, {
    let original=original!(rename,unsafe extern "C" fn(*const c_char,*const c_char)->c_int);
    let Some(_guard)=Guard::enter() else {return original(from,to);};
    if RUNTIME.get().is_none() {return original(from,to);}
    let (from,_)=translated!(from,AT_FDCWD,true,-1);let (to,_)=translated!(to,AT_FDCWD,true,-1);original(from.as_ptr(),to.as_ptr())
});
hook!(unlinkat, pnport_unlinkat, (fd:c_int,path:*const c_char,flags:c_int) -> c_int, {
    let original=original!(unlinkat,unsafe extern "C" fn(c_int,*const c_char,c_int)->c_int);
    let Some(_guard)=Guard::enter() else {return original(fd,path,flags);};
    if RUNTIME.get().is_none() {return original(fd,path,flags);}
    let (path,_)=translated!(path,fd,true,-1);original(AT_FDCWD,path.as_ptr(),flags)
});
hook!(fchdir, pnport_fchdir, (fd:c_int) -> c_int, {
    let original=original!(fchdir,unsafe extern "C" fn(c_int)->c_int);
    let Some(_guard)=Guard::enter() else {return original(fd);};
    let result=original(fd);
    if result==0 && let Some(runtime)=RUNTIME.get() && let Ok(mut runtime)=runtime.lock() {
        runtime.cwd=runtime.descriptors.get(&fd).and_then(|translation| {
            if translation.readonly {
                Some(translation.logical.clone())
            } else {
                live_directory_path(fd).ok()
            }
        });
    }result
});
hook!(dup, pnport_dup, (fd:c_int) -> c_int, {
    let original=original!(dup,unsafe extern "C" fn(c_int)->c_int);
    let Some(_guard)=Guard::enter() else {return original(fd);};
    let result=original(fd);
    if result>=0 && let Some(runtime)=RUNTIME.get() && let Ok(mut runtime)=runtime.lock() && let Some(t)=runtime.descriptors.get(&fd).cloned() {runtime.descriptors.insert(result,t);} result
});
hook!(dup2, pnport_dup2, (fd:c_int,newfd:c_int) -> c_int, {
    let original=original!(dup2,unsafe extern "C" fn(c_int,c_int)->c_int);
    let Some(_guard)=Guard::enter() else {return original(fd,newfd);};
    let result=original(fd,newfd);
    if result>=0 && let Some(runtime)=RUNTIME.get() && let Ok(mut runtime)=runtime.lock() {let t=runtime.descriptors.get(&fd).cloned();runtime.descriptors.remove(&newfd);if let Some(t)=t {runtime.descriptors.insert(newfd,t);}} result
});

static INJECTION_ENV: OnceLock<Vec<CString>> = OnceLock::new();
fn admitted_program(path: &Path) -> std::result::Result<(CString, LaunchAdmission), c_int> {
    let admission = LaunchAdmission::new(path).map_err(|error| fail(error.code))?;
    let canonical = CString::new(admission.path.as_os_str().as_bytes()).map_err(|_| EINVAL)?;
    Ok((canonical, admission))
}
struct ChildImage {
    path: CString,
    admission: LaunchAdmission,
    script_argv: Option<Vec<CString>>,
}

fn launch_marker(env: &mut Vec<CString>) -> std::result::Result<PathBuf, c_int> {
    let session = SESSION.get().ok_or(EIO)?;
    let pending = session.join("pending");
    fs::create_dir_all(&pending).map_err(|_| fail(Code::PnportInjectionFailed))?;
    // Publish before the syscall. An image that drops DYLD injection cannot
    // acknowledge this token, so the supervisor rejects its partial trace.
    let marker = tempfile::Builder::new()
        .prefix("pnport-")
        .tempfile_in(&pending)
        .map_err(|_| fail(Code::PnportInjectionFailed))?;
    let token = marker
        .path()
        .file_name()
        .and_then(OsStr::to_str)
        .ok_or_else(|| fail(Code::PnportInjectionFailed))?;
    let entry = CString::new(format!("PNPORT_LAUNCH_TOKEN={token}"))
        .map_err(|_| fail(Code::PnportInjectionFailed))?;
    let path = marker
        .into_temp_path()
        .keep()
        .map_err(|_| fail(Code::PnportInjectionFailed))?;
    env.push(entry);
    Ok(path)
}

unsafe fn prepare_child_image(
    path: *const c_char,
    argv: *const *const c_char,
    env: &[CString],
) -> std::result::Result<ChildImage, c_int> {
    if argv.is_null() {
        return Err(EFAULT);
    }
    let mut original_args = Vec::new();
    let mut index = 0;
    while !(*argv.add(index)).is_null() {
        original_args.push(OsString::from_vec(
            CStr::from_ptr(*argv.add(index)).to_bytes().to_vec(),
        ));
        index += 1;
    }
    let user_args = original_args.get(1..).unwrap_or_default();
    let search_path = env.iter().find_map(|entry| {
        entry
            .to_bytes()
            .strip_prefix(b"PATH=")
            .map(OsStr::from_bytes)
    });
    let runtime = RUNTIME.get().ok_or(EIO)?;
    let logical = {
        let runtime = runtime.lock().map_err(|_| EIO)?;
        path_from(path, AT_FDCWD, &runtime)?
    };
    let prepared = pnport_core::executable::prepare_with_translation(
        &logical,
        user_args,
        search_path,
        |path| {
            runtime
                .lock()
                .map_err(|_| {
                    pnport_core::diagnostic::Error::new(
                        Code::PnportInjectionFailed,
                        "The native interception state is unavailable.",
                    )
                })?
                .view
                .translate(path)
        },
    )
    .map_err(|error| fail(error.code))?;
    let (admitted, admission) = admitted_program(&prepared.program)?;
    // Native exec preserves the caller's argv[0]. The kernel replaces it for
    // a shebang script, so only the script case needs a rebuilt argv vector.
    let script_argv = if prepared.args.len() == user_args.len() {
        None
    } else {
        let mut rewritten_args = Vec::with_capacity(prepared.args.len() + 1);
        rewritten_args.push(admitted.clone());
        for argument in prepared.args {
            rewritten_args.push(CString::new(argument.into_vec()).map_err(|_| EINVAL)?);
        }
        Some(rewritten_args)
    };
    Ok(ChildImage {
        path: admitted,
        admission,
        script_argv,
    })
}
unsafe fn child_env(envp: *const *const c_char) -> std::result::Result<Vec<CString>, c_int> {
    if envp.is_null() {
        return Err(EFAULT);
    }
    let mut result = Vec::new();
    let mut i = 0;
    while !(*envp.add(i)).is_null() {
        let value = CStr::from_ptr(*envp.add(i));
        if ![
            b"PNPORT_SESSION=".as_slice(),
            b"PNPORT_CACHE=",
            b"PNPORT_LAUNCH_TOKEN=",
            b"DYLD_INSERT_LIBRARIES=",
            b"LD_PRELOAD=",
        ]
        .iter()
        .any(|prefix| value.to_bytes().starts_with(prefix))
        {
            result.push(value.to_owned());
        }
        i += 1;
    }
    if let Some(env) = INJECTION_ENV.get() {
        result.extend(env.iter().cloned());
    } else {
        return Err(EIO);
    }
    Ok(result)
}
hook!(execve,pnport_execve,(path:*const c_char,argv:*const *const c_char,envp:*const *const c_char)->c_int,{
    let original=original!(execve,unsafe extern "C" fn(*const c_char,*const *const c_char,*const *const c_char)->c_int);
    let Some(_guard)=Guard::enter() else {return original(path,argv,envp);};
    if RUNTIME.get().is_none() {return original(path,argv,envp);}
    let mut env=match child_env(envp) {Ok(env)=>env,Err(code)=>{errno(code);return -1;}};
    let image=match prepare_child_image(path,argv,&env) {Ok(image)=>image,Err(code)=>{errno(code);return -1;}};
    let marker=match launch_marker(&mut env) {Ok(marker)=>marker,Err(code)=>{errno(code);return -1;}};
    let script_argv=image.script_argv.as_ref().map(|args| {let mut pointers:Vec<_>=args.iter().map(|arg|arg.as_ptr()).collect();pointers.push(ptr::null());pointers});
    let argv=script_argv.as_ref().map_or(argv,Vec::as_ptr);
    let mut pointers:Vec<_>=env.iter().map(|e|e.as_ptr()).collect();pointers.push(ptr::null());
    if let Err(error)=image.admission.verify_at_launch() {let _=fs::remove_file(&marker);errno(fail(error.code));return -1;}
    let result=original(image.path.as_ptr(),argv,pointers.as_ptr());
    let saved_errno=*__error();
    let _=fs::remove_file(&marker);
    errno(saved_errno);
    result
});
unsafe fn spawn_admitted(
    pid: *mut pid_t,
    path: *const c_char,
    actions: *const posix_spawn_file_actions_t,
    attributes: *const posix_spawnattr_t,
    argv: *const *mut c_char,
    envp: *const *mut c_char,
) -> c_int {
    let original = original!(
        posix_spawn,
        unsafe extern "C" fn(
            *mut pid_t,
            *const c_char,
            *const posix_spawn_file_actions_t,
            *const posix_spawnattr_t,
            *const *mut c_char,
            *const *mut c_char,
        ) -> c_int
    );
    // Opaque file actions can change the child's cwd before resolving a
    // relative image. Until the actions can be inspected, reject this shape
    // instead of resolving and launching a different image in the parent cwd.
    if !actions.is_null()
        && !path.is_null()
        && !Path::new(OsStr::from_bytes(CStr::from_ptr(path).to_bytes())).is_absolute()
    {
        return fail(Code::PnportUnsupportedOperation);
    }
    let mut env = match child_env(envp.cast()) {
        Ok(env) => env,
        Err(code) => return code,
    };
    let image = match prepare_child_image(path, argv.cast(), &env) {
        Ok(image) => image,
        Err(code) => return code,
    };
    let marker = match launch_marker(&mut env) {
        Ok(marker) => marker,
        Err(code) => return code,
    };
    let script_argv = image.script_argv.as_ref().map(|args| {
        let mut pointers: Vec<_> = args.iter().map(|arg| arg.as_ptr().cast_mut()).collect();
        pointers.push(ptr::null_mut());
        pointers
    });
    let argv = script_argv.as_ref().map_or(argv, Vec::as_ptr);
    let mut pointers: Vec<_> = env.iter().map(|e| e.as_ptr().cast_mut()).collect();
    pointers.push(ptr::null_mut());
    if let Err(error) = image.admission.verify_at_launch() {
        let _ = fs::remove_file(&marker);
        return fail(error.code);
    }
    let result = original(
        pid,
        image.path.as_ptr(),
        actions,
        attributes,
        argv,
        pointers.as_ptr(),
    );
    if result != 0 {
        let _ = fs::remove_file(&marker);
    }
    result
}

hook!(posix_spawn,pnport_spawn,(pid:*mut pid_t,path:*const c_char,actions:*const posix_spawn_file_actions_t,attributes:*const posix_spawnattr_t,argv:*const *mut c_char,envp:*const *mut c_char)->c_int,{
    let original=original!(posix_spawn,unsafe extern "C" fn(*mut pid_t,*const c_char,*const posix_spawn_file_actions_t,*const posix_spawnattr_t,*const *mut c_char,*const *mut c_char)->c_int);
    let Some(_guard)=Guard::enter() else {return original(pid,path,actions,attributes,argv,envp);};
    if RUNTIME.get().is_none() {return original(pid,path,actions,attributes,argv,envp);}
    spawn_admitted(pid,path,actions,attributes,argv,envp)
});

unsafe fn spawn_path(
    file: *const c_char,
    actions: *const posix_spawn_file_actions_t,
) -> std::result::Result<CString, c_int> {
    if file.is_null() {
        return Err(EFAULT);
    }
    let file = CStr::from_ptr(file).to_bytes();
    if file.is_empty() {
        return Err(ENOENT);
    }
    if file.contains(&b'/') {
        return CString::new(file).map_err(|_| EINVAL);
    }
    // Darwin's posix_spawnp searches the parent's PATH, even when envp replaces
    // the child's environment. Its absent-PATH default comes from paths.h.
    let inherited = libc::getenv(c"PATH".as_ptr());
    let search = if inherited.is_null() {
        b"/usr/bin:/bin".as_slice()
    } else {
        CStr::from_ptr(inherited).to_bytes()
    };
    let mut denied = false;
    let runtime = RUNTIME.get().ok_or(EIO)?;
    for directory in std::env::split_paths(OsStr::from_bytes(search)) {
        if !actions.is_null() && !directory.is_absolute() {
            // File actions execute before a relative candidate is looked up.
            // Inspecting it in the parent could select a different child image.
            return Err(fail(Code::PnportUnsupportedOperation));
        }
        let candidate = CString::new(
            directory
                .join(OsStr::from_bytes(file))
                .as_os_str()
                .as_bytes(),
        )
        .map_err(|_| EINVAL)?;
        let logical = {
            let runtime = runtime.lock().map_err(|_| EIO)?;
            path_from(candidate.as_ptr(), AT_FDCWD, &runtime)?
        };
        let translation = runtime.lock().map_err(|_| EIO)?.view.translate(&logical);
        let translation = match translation {
            Ok(translation) => translation,
            Err(error)
                if matches!(
                    error.code,
                    Code::PnportResolutionFailed | Code::PnportCommandNotFound
                ) =>
            {
                continue;
            }
            Err(error) => return Err(fail(error.code)),
        };
        match fs::metadata(&translation.physical) {
            Ok(metadata) => {
                let physical = CString::new(translation.physical.as_os_str().as_bytes())
                    .map_err(|_| EINVAL)?;
                if metadata.is_file() && libc::access(physical.as_ptr(), libc::X_OK) == 0 {
                    return CString::new(logical.as_os_str().as_bytes()).map_err(|_| EINVAL);
                }
                denied = true;
            }
            Err(error) => match error.raw_os_error().unwrap_or(EIO) {
                ENOENT | ENOTDIR => {}
                libc::EACCES => denied = true,
                code => return Err(code),
            },
        }
    }
    Err(if denied { libc::EACCES } else { ENOENT })
}

hook!(posix_spawnp,pnport_spawnp,(pid:*mut pid_t,path:*const c_char,actions:*const posix_spawn_file_actions_t,attributes:*const posix_spawnattr_t,argv:*const *mut c_char,envp:*const *mut c_char)->c_int,{
    let original=original!(posix_spawnp,unsafe extern "C" fn(*mut pid_t,*const c_char,*const posix_spawn_file_actions_t,*const posix_spawnattr_t,*const *mut c_char,*const *mut c_char)->c_int);
    let Some(_guard)=Guard::enter() else {return original(pid,path,actions,attributes,argv,envp);};
    if RUNTIME.get().is_none() {return original(pid,path,actions,attributes,argv,envp);}
    let path=match spawn_path(path,actions) {Ok(path)=>path,Err(code)=>return code};
    spawn_admitted(pid,path.as_ptr(),actions,attributes,argv,envp)
});
