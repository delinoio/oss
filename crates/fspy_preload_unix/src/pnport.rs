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
        Mutex, OnceLock,
        atomic::{AtomicBool, Ordering},
    },
};

#[cfg(target_os = "linux")]
use libc::{__errno_location, RTLD_NEXT, dlsym};
use libc::{
    __error, _exit, AT_FDCWD, AT_SYMLINK_NOFOLLOW, DIR, EBADF, EEXIST, EFAULT, EINVAL, EIO,
    ENAMETOOLONG, ENOENT, ENOMEM, ENOTDIR, ENOTSUP, ERANGE, EROFS, F_GETPATH, FILE, O_APPEND,
    O_CREAT, O_RDWR, O_TRUNC, O_WRONLY, PATH_MAX, S_IFDIR, S_IFLNK, S_IFMT, W_OK, c_char, c_int,
    c_void, dirfd, fcntl, fileno, free, fstat, getpid, malloc, mode_t, off_t, pid_t,
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
        INSIDE.with(|inside| (!inside.replace(true)).then_some(Self))
    }
}
impl Drop for Guard {
    fn drop(&mut self) {
        INSIDE.with(|inside| inside.set(false));
    }
}

struct Runtime {
    view: View,
    descriptors: HashMap<c_int, Translation>,
    cwd: Option<PathBuf>,
}
static RUNTIME: OnceLock<Mutex<Runtime>> = OnceLock::new();
static SESSION: OnceLock<PathBuf> = OnceLock::new();

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
unsafe fn path_from(
    path: *const c_char,
    dirfd: c_int,
    runtime: &Runtime,
) -> std::result::Result<PathBuf, c_int> {
    if path.is_null() {
        return Err(EFAULT);
    }
    let path = Path::new(OsStr::from_bytes(CStr::from_ptr(path).to_bytes()));
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
        if metadata.assume_init().st_mode & S_IFMT != S_IFDIR {
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
        translation.logical.clone()
    } else {
        #[cfg(target_os = "macos")]
        {
            let mut buffer = [0u8; PATH_MAX as usize];
            if fcntl(dirfd, F_GETPATH, buffer.as_mut_ptr()) < 0 {
                return Err(EBADF);
            }
            PathBuf::from(OsStr::from_bytes(
                CStr::from_ptr(buffer.as_ptr().cast()).to_bytes(),
            ))
        }
        #[cfg(target_os = "linux")]
        {
            fs::read_link(format!("/proc/self/fd/{dirfd}")).map_err(|_| EBADF)?
        }
    };
    Ok(base.join(path))
}

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
            .set(Mutex::new(Runtime {
                view,
                descriptors: HashMap::new(),
                cwd: None,
            }))
            .ok()?;
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
hook!(readlink, pnport_readlink, (path:*const c_char, output:*mut c_char, size:size_t) -> ssize_t, {
    let original = original!(readlink,unsafe extern "C" fn(*const c_char,*mut c_char,size_t)->ssize_t);
    let Some(_guard) = Guard::enter() else { return original(path,output,size); };
    if RUNTIME.get().is_none() { return original(path,output,size); }
    let (physical,translation) = translated!(path,AT_FDCWD,false,-1);
    if !translation.virtual_link { return original(physical.as_ptr(),output,size); }
    if size == 0 { errno(EINVAL); return -1; }
    if output.is_null() { errno(EFAULT); return -1; }
    let bytes = translation.logical.as_os_str().as_bytes(); let count = size.min(bytes.len());
    ptr::copy_nonoverlapping(bytes.as_ptr(),output.cast(),count); count.cast_signed()
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
    let dir=original(path.as_ptr());if !dir.is_null() {track(dirfd(dir),translation);} dir
});
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
    if result==0 && let Some(runtime)=RUNTIME.get() && let Ok(mut runtime)=runtime.lock() {runtime.cwd=runtime.descriptors.get(&fd).map(|t|t.logical.clone());}result
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
hook!(posix_spawn,pnport_spawn,(pid:*mut pid_t,path:*const c_char,actions:*const posix_spawn_file_actions_t,attributes:*const posix_spawnattr_t,argv:*const *mut c_char,envp:*const *mut c_char)->c_int,{
    let original=original!(posix_spawn,unsafe extern "C" fn(*mut pid_t,*const c_char,*const posix_spawn_file_actions_t,*const posix_spawnattr_t,*const *mut c_char,*const *mut c_char)->c_int);
    let Some(_guard)=Guard::enter() else {return original(pid,path,actions,attributes,argv,envp);};
    if RUNTIME.get().is_none() {return original(pid,path,actions,attributes,argv,envp);}
    // Opaque file actions can change the child's cwd before resolving a
    // relative image. Until the actions can be inspected, reject this shape
    // instead of resolving and launching a different image in the parent cwd.
    if !actions.is_null() && !path.is_null() && !Path::new(OsStr::from_bytes(CStr::from_ptr(path).to_bytes())).is_absolute() {
        return fail(Code::PnportUnsupportedOperation);
    }
    let mut env=match child_env(envp.cast()) {Ok(env)=>env,Err(code)=>return code};
    let image=match prepare_child_image(path,argv.cast(),&env) {Ok(image)=>image,Err(code)=>return code};
    let marker=match launch_marker(&mut env) {Ok(marker)=>marker,Err(code)=>return code};
    let script_argv=image.script_argv.as_ref().map(|args| {let mut pointers:Vec<_>=args.iter().map(|arg|arg.as_ptr().cast_mut()).collect();pointers.push(ptr::null_mut());pointers});
    let argv=script_argv.as_ref().map_or(argv,Vec::as_ptr);
    let mut pointers:Vec<_>=env.iter().map(|e|e.as_ptr().cast_mut()).collect();pointers.push(ptr::null_mut());
    if let Err(error)=image.admission.verify_at_launch() {let _=fs::remove_file(&marker);return fail(error.code);}
    let result=original(pid,image.path.as_ptr(),actions,attributes,argv,pointers.as_ptr());
    if result!=0 {let _=fs::remove_file(&marker);}
    result
});
