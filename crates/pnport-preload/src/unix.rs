use std::{
    cell::Cell,
    collections::HashMap,
    ffi::{CStr, CString, OsStr},
    fs,
    os::unix::ffi::OsStrExt,
    path::{Path, PathBuf},
    ptr,
    sync::{
        atomic::{AtomicBool, Ordering},
        Mutex, OnceLock,
    },
};

use libc::*;
use pnport_core::{
    cache::Cache,
    diagnostic::Code,
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
    ) {
        if let Some(session) = SESSION.get() {
            let _ = fs::write(session.join("failure"), code.as_str());
        }
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
    let physical = CString::new(translation.physical.as_os_str().as_bytes()).map_err(|_| EINVAL)?;
    Ok((physical, translation))
}
unsafe fn track(fd: c_int, translation: Translation) {
    if fd >= 0 {
        if let Some(runtime) = RUNTIME.get() {
            if let Ok(mut runtime) = runtime.lock() {
                runtime.descriptors.insert(fd, translation);
            }
        }
    }
}

// Adapted from the pinned fspy Mach-O interpose macro; see vendor/fspy.
macro_rules! hook {
    ($name:ident, $wrapper:ident, ($($arg:ident: $ty:ty),*) -> $ret:ty, $body:block) => {
        unsafe extern "C" fn $wrapper($($arg: $ty),*) -> $ret $body
        #[cfg(target_os = "macos")]
        const _: () = {
            #[repr(C)] struct Entry { replacement: *const c_void, original: *const c_void }
            #[used]
            #[link_section = "__DATA,__interpose"]
            static mut ENTRY: Entry = Entry { replacement: $wrapper as *const c_void, original: libc::$name as *const c_void };
        };
        #[cfg(target_os = "linux")]
        #[export_name = stringify!($name)]
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
        Some(())
    })();
    if result.is_none() {
        let _ = fs::write(session.join("failure"), b"PNPORT_INJECTION_FAILED");
        _exit(125);
    }
}
#[used]
#[cfg_attr(target_os = "macos", link_section = "__DATA,__mod_init_func")]
#[cfg_attr(target_os = "linux", link_section = ".init_array")]
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
    #[repr(C)]
    struct Entry(*const c_void, *const c_void);
    #[used]
    #[link_section = "__DATA,__interpose"]
    static mut ENTRY: Entry = Entry(pnport_open as _, libc::open as _);
};
#[cfg(target_os = "linux")]
#[export_name = "open"]
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
    #[repr(C)]
    struct Entry(*const c_void, *const c_void);
    #[used]
    #[link_section = "__DATA,__interpose"]
    static mut ENTRY: Entry = Entry(pnport_openat as _, libc::openat as _);
};
#[cfg(target_os = "linux")]
#[export_name = "openat"]
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
        (*output).st_size = translation.logical.as_os_str().as_bytes().len() as off_t;
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
    ptr::copy_nonoverlapping(bytes.as_ptr(),output.cast(),count); count as ssize_t
});
hook!(chdir, pnport_chdir, (path:*const c_char) -> c_int, {
    let original = original!(chdir, unsafe extern "C" fn(*const c_char)->c_int);
    let Some(_guard) = Guard::enter() else { return original(path); };
    if RUNTIME.get().is_none() { return original(path); }
    let (path,translation) = translated!(path,AT_FDCWD,false,-1);
    let result = original(path.as_ptr());
    if result == 0 { if let Ok(mut runtime) = RUNTIME.get().unwrap().lock() { runtime.cwd=Some(translation.logical); } } result
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
    if let Some(runtime)=RUNTIME.get() {if let Ok(mut runtime)=runtime.lock() {runtime.descriptors.remove(&fd);}}
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
    if result==0 {if let Some(runtime)=RUNTIME.get() {if let Ok(mut runtime)=runtime.lock() {runtime.cwd=runtime.descriptors.get(&fd).map(|t|t.logical.clone());}}}result
});
hook!(dup, pnport_dup, (fd:c_int) -> c_int, {
    let original=original!(dup,unsafe extern "C" fn(c_int)->c_int);
    let Some(_guard)=Guard::enter() else {return original(fd);};
    let result=original(fd);
    if result>=0 {if let Some(runtime)=RUNTIME.get() {if let Ok(mut runtime)=runtime.lock() {if let Some(t)=runtime.descriptors.get(&fd).cloned() {runtime.descriptors.insert(result,t);}}}} result
});
hook!(dup2, pnport_dup2, (fd:c_int,newfd:c_int) -> c_int, {
    let original=original!(dup2,unsafe extern "C" fn(c_int,c_int)->c_int);
    let Some(_guard)=Guard::enter() else {return original(fd,newfd);};
    let result=original(fd,newfd);
    if result>=0 {if let Some(runtime)=RUNTIME.get() {if let Ok(mut runtime)=runtime.lock() {let t=runtime.descriptors.get(&fd).cloned();runtime.descriptors.remove(&newfd);if let Some(t)=t {runtime.descriptors.insert(newfd,t);}}}} result
});

static INJECTION_ENV: OnceLock<Vec<CString>> = OnceLock::new();
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
    let (path,translation)=translated!(path,AT_FDCWD,false,-1);
    if let Err(error)=pnport_core::executable::validate(&translation.physical) {errno(fail(error.code));return -1;}
    let env=match child_env(envp) {Ok(env)=>env,Err(code)=>{errno(code);return -1;}};
    let mut pointers:Vec<_>=env.iter().map(|e|e.as_ptr()).collect();pointers.push(ptr::null());
    original(path.as_ptr(),argv,pointers.as_ptr())
});
hook!(posix_spawn,pnport_spawn,(pid:*mut pid_t,path:*const c_char,actions:*const posix_spawn_file_actions_t,attributes:*const posix_spawnattr_t,argv:*const *mut c_char,envp:*const *mut c_char)->c_int,{
    let original=original!(posix_spawn,unsafe extern "C" fn(*mut pid_t,*const c_char,*const posix_spawn_file_actions_t,*const posix_spawnattr_t,*const *mut c_char,*const *mut c_char)->c_int);
    let Some(_guard)=Guard::enter() else {return original(pid,path,actions,attributes,argv,envp);};
    if RUNTIME.get().is_none() {return original(pid,path,actions,attributes,argv,envp);}
    let (path,translation)=match translate(path,AT_FDCWD,false) {Ok(value)=>value,Err(code)=>return code};
    if let Err(error)=pnport_core::executable::validate(&translation.physical) {return fail(error.code);}
    let env=match child_env(envp.cast()) {Ok(env)=>env,Err(code)=>return code};
    let mut pointers:Vec<_>=env.iter().map(|e|e.as_ptr() as *mut c_char).collect();pointers.push(ptr::null_mut());
    original(pid,path.as_ptr(),actions,attributes,argv,pointers.as_ptr())
});
