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
    io::Write,
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
    diagnostic::{Code, Error, ExecFailureKind, InitializationStage, ProcessGroupOperation},
    executable::LaunchAdmission,
    graph::{Graph, Snapshot},
    native_path::{Lookup as NativeLookup, SymlinkPolicy},
    view::{DirectoryEntry, PathKind, Translation, View},
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
    position: usize,
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
                position: 0,
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

fn directory_entries(stream: &DirectoryStream) -> std::result::Result<Vec<DirectoryEntry>, c_int> {
    let runtime = RUNTIME.get().ok_or(EIO)?.lock().map_err(|_| EIO)?;
    runtime
        .view
        .directory_entries(&stream.logical, &stream.physical)
        .map_err(|error| fail(error.code))
}

fn directory_cookie(position: usize, count: usize) -> libc::c_long {
    let count =
        libc::c_long::try_from(count).expect("Allocated directory entries fit a native cookie");
    let position =
        libc::c_long::try_from(position).expect("Directory position fits a native cookie");
    DIRECTORY_END - count + position
}

fn directory_entry(
    stream: &mut DirectoryStream,
    entries: &[DirectoryEntry],
) -> std::result::Result<*mut dirent, c_int> {
    let Some(entry) = entries.get(stream.position) else {
        return Ok(ptr::null_mut());
    };
    let name = entry.name.as_bytes();
    if name.len() >= stream.entry.d_name.len() {
        return Err(ENAMETOOLONG);
    }
    stream.entry.d_ino = 1;
    stream.entry.d_seekoff =
        u64::try_from(directory_cookie(stream.position + 1, entries.len())).map_err(|_| EIO)?;
    stream.entry.d_type = if entry.directory {
        libc::DT_DIR
    } else {
        libc::DT_LNK
    };
    stream.entry.d_namlen = u16::try_from(name.len()).map_err(|_| ENAMETOOLONG)?;
    stream.entry.d_reclen =
        u16::try_from((std::mem::offset_of!(dirent, d_name) + name.len() + 1).next_multiple_of(4))
            .map_err(|_| ENAMETOOLONG)?;
    stream.entry.d_name.fill(0);
    for (slot, byte) in stream.entry.d_name.iter_mut().zip(name) {
        *slot = byte.cast_signed();
    }
    stream.position += 1;
    stream.virtual_end = stream.position == entries.len();
    Ok(&raw mut *stream.entry)
}
static RUNTIME: OnceLock<Box<ForkMutex<Runtime>>> = OnceLock::new();
static SESSION: OnceLock<PathBuf> = OnceLock::new();
static OWNER_KEY: OnceLock<[u8; 32]> = OnceLock::new();
static OWNED_GROUP: OnceLock<pid_t> = OnceLock::new();

unsafe extern "C" fn before_fork() {
    if RUNTIME.get().is_some() && register_owned_process(libc::getpid()).is_err() {
        if let Some(session) = SESSION.get() {
            record_initialization_failure(session, InitializationStage::OwnedGroup);
            record_failure(session, Code::PnportInjectionFailed);
        }
        _exit(125);
    }
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
fn record_failure(session: &Path, code: Code) {
    // The supervisor may read concurrently with any injected process. Publish
    // complete bytes once; truncation or collateral failures must not replace
    // the first diagnostic with an empty or different failure code.
    record_bytes(session, "failure", code.as_str().as_bytes());
}

fn record_bytes(session: &Path, name: &'static str, bytes: &[u8]) {
    if let Ok(mut file) = tempfile::NamedTempFile::new_in(session)
        && file.write_all(bytes).is_ok()
    {
        let _ = file.persist_noclobber(session.join(name));
    }
}

fn record_initialization_failure(session: &Path, stage: InitializationStage) {
    // The constructor cannot initialize a process-wide tracing subscriber in
    // the user's executable. Publish only a closed enum for supervisor logs.
    // Preserve the first observed stage atomically, like the failure code.
    if let Ok(bytes) = serde_json::to_vec(&stage) {
        record_bytes(session, "initialization-failure", &bytes);
    }
}

fn register_owned_process(pid: pid_t) -> std::io::Result<()> {
    register_owned_identity(pnport_core::macos_process::Identity::capture(pid)?)
}

fn register_owned_identity(identity: pnport_core::macos_process::Identity) -> std::io::Result<()> {
    let _guard = Guard::enter();
    let session = SESSION
        .get()
        .ok_or_else(|| std::io::Error::other("Native owner context is missing"))?;
    let public = OWNER_KEY
        .get()
        .ok_or_else(|| std::io::Error::other("Native verification context is missing"))?;
    pnport_core::macos_process::registration(session, identity, public)
}

fn admit_group_change(
    pid: pid_t,
    operation: ProcessGroupOperation,
) -> std::result::Result<(), c_int> {
    admit_group_identity(
        pnport_core::macos_process::Identity::capture(pid),
        operation,
    )
}

fn admit_group_identity(
    identity: std::io::Result<pnport_core::macos_process::Identity>,
    operation: ProcessGroupOperation,
) -> std::result::Result<(), c_int> {
    identity.and_then(register_owned_identity).map_err(|_| {
        if let Some(session) = SESSION.get()
            && let Ok(bytes) = serde_json::to_vec(&operation)
        {
            record_bytes(session, "process-group-failure", &bytes);
        }
        fail(Code::PnportInjectionFailed)
    })
}

#[cfg(test)]
mod failure_tests {
    use std::{fs, sync::Barrier};

    use super::{Code, record_failure};

    #[test]
    fn concurrent_failures_preserve_the_complete_first_diagnostic() {
        let session = tempfile::tempdir().unwrap();
        record_failure(session.path(), Code::PnportCommandNotExecutable);
        let barrier = Barrier::new(9);
        std::thread::scope(|scope| {
            for _ in 0..8 {
                scope.spawn(|| {
                    barrier.wait();
                    for _ in 0..64 {
                        record_failure(session.path(), Code::PnportInjectionFailed);
                    }
                });
            }
            barrier.wait();
            for _ in 0..512 {
                assert_eq!(
                    fs::read(session.path().join("failure")).unwrap(),
                    Code::PnportCommandNotExecutable.as_str().as_bytes()
                );
            }
        });
        assert_eq!(fs::read_dir(session.path()).unwrap().count(), 1);
    }
}

fn fail(code: Code) -> c_int {
    if !matches!(
        code,
        Code::PnportResolutionFailed | Code::PnportCommandNotFound
    ) && let Some(session) = SESSION.get()
    {
        record_failure(session, code);
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
        Ok(PathBuf::from(OsStr::from_bytes(
            CStr::from_ptr(buffer.as_ptr().cast()).to_bytes(),
        )))
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
    let duplicate = matches!(command, libc::F_DUPFD | libc::F_DUPFD_CLOEXEC);
    // F_TRANSFEREXTENTS takes the destination descriptor as an integer
    // variadic argument, unlike the pointer-valued fcntl commands below.
    // Decode it once so the secondary backing is admitted before native fcntl.
    let transfer_descriptor = (command == libc::F_TRANSFEREXTENTS).then(|| args.arg::<c_int>());
    if let Some(transfer_fd) = transfer_descriptor.filter(|descriptor| *descriptor < 0) {
        // A negative destination cannot mutate either operand. Native fcntl
        // owns its validation order: EINVAL for a valid primary descriptor,
        // while an invalid primary still returns its own native failure.
        return original(fd, command, transfer_fd);
    }
    let mutation = matches!(
        command,
        libc::F_PREALLOCATE
            | libc::F_PUNCHHOLE
            | libc::F_TRIM_ACTIVE_FILE
            | libc::F_TRANSFEREXTENTS
    );
    // Rejected/reentrant hooks still use the exact native ABI below without
    // taking a runtime lock. Admitted duplication and metadata mutation share
    // the descriptor lock through the kernel call and provenance publication.
    let mut runtime = if guard.is_some() && (duplicate || mutation) {
        let Ok(runtime) = RUNTIME.get().map(|runtime| runtime.lock()).transpose() else {
            errno(fail(Code::PnportInjectionFailed));
            return -1;
        };
        runtime
    } else {
        None
    };
    if mutation
        && let Some(runtime) = runtime.as_mut()
        && let Err(code) = validate_descriptor_mutation(fd, transfer_descriptor, runtime, original)
    {
        errno(code);
        return -1;
    }

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
        libc::F_TRANSFEREXTENTS => original(fd, command, transfer_descriptor.unwrap_or_default()),
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
        | libc::F_SPECULATIVE_READ => {
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
        && duplicate
        && let Some(runtime) = runtime.as_mut()
    {
        let translation = runtime.descriptors.get(&fd).cloned();
        runtime.descriptors.remove(&result);
        if let Some(translation) = translation {
            runtime.descriptors.insert(result, translation);
        }
    }
    result
}

#[cfg(target_os = "macos")]
unsafe fn validate_descriptor_mutation(
    fd: c_int,
    transfer_descriptor: Option<c_int>,
    runtime: &mut Runtime,
    original: unsafe extern "C" fn(c_int, c_int, ...) -> c_int,
) -> std::result::Result<(), c_int> {
    // F_GETPATH is a backing-path probe, not a descriptor-validity check:
    // it fails for pipes and invalid descriptors alike. Validate every
    // operand first so native EBADF takes precedence over managed EROFS.
    descriptor_valid(fd, original)?;
    if let Some(transfer_fd) = transfer_descriptor {
        descriptor_valid(transfer_fd, original)?;
    }
    let primary_readonly = descriptor_readonly(fd, runtime)?;
    let transfer_readonly = match transfer_descriptor {
        Some(transfer_fd) => descriptor_readonly(transfer_fd, runtime)?,
        None => false,
    };
    if primary_readonly || transfer_readonly {
        return Err(EROFS);
    }
    Ok(())
}

unsafe fn descriptor_valid(
    fd: c_int,
    original: unsafe extern "C" fn(c_int, c_int, ...) -> c_int,
) -> std::result::Result<(), c_int> {
    let saved_errno = *__error();
    if original(fd, libc::F_GETFD) == -1 {
        let error = *__error();
        errno(saved_errno);
        return Err(error);
    }
    errno(saved_errno);
    Ok(())
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
    translate_following(path, dirfd, write, true, SymlinkPolicy::Allow)
}

fn native_translation(logical: &Path, physical: PathBuf) -> Translation {
    Translation {
        logical: logical.to_owned(),
        physical,
        readonly: false,
        virtual_link: false,
        kind: PathKind::Native,
    }
}

fn translate_lookup(
    runtime: &mut Runtime,
    path: &Path,
    follow_last: bool,
) -> pnport_core::diagnostic::Result<Translation> {
    runtime.view.graph.check_path_conflicts(path)?;
    let Some(lookup) =
        pnport_core::native_path::resolved_lookup(path, follow_last, &runtime.view.graph)
    else {
        return Ok(native_translation(path, path.to_owned()));
    };
    let checked = lookup.validate_parents(|parent| runtime.view.translate(parent))?;
    translate_checked_lookup(runtime, path, checked)
}

fn translate_checked_lookup(
    runtime: &mut Runtime,
    path: &Path,
    lookup: NativeLookup,
) -> pnport_core::diagnostic::Result<Translation> {
    let (resolved, requires_directory) = match lookup {
        pnport_core::native_path::Lookup::Resolved {
            path,
            requires_directory,
        } => (path, requires_directory),
        NativeLookup::NativeFailure { path: physical, .. } => {
            return Ok(native_translation(path, physical));
        }
    };
    let mut translation = runtime.view.translate(&resolved)?;
    if requires_directory {
        translation.virtual_link = false;
        if translation.readonly {
            translation.physical.push(".");
        }
    }
    if !translation.readonly {
        // Native lookup must keep the caller's symlink, '..', missing-parent
        // and trailing-separator semantics. Only managed backing is rewritten.
        path.clone_into(&mut translation.physical);
    }
    Ok(translation)
}

unsafe fn translate_following(
    path: *const c_char,
    dirfd: c_int,
    write: bool,
    follow_last: bool,
    policy: SymlinkPolicy,
) -> std::result::Result<(CString, Translation), c_int> {
    let Some(runtime) = RUNTIME.get() else {
        return Err(EIO);
    };
    let mut runtime = runtime.lock().map_err(|_| EIO)?;
    let path = path_from(path, dirfd, &runtime)?;
    runtime
        .view
        .graph
        .check_path_conflicts(&path)
        .map_err(|error| fail(error.code))?;
    let translation = if policy == SymlinkPolicy::Reject {
        match pnport_core::native_path::resolved_lookup_with_policy(
            &path,
            follow_last,
            &runtime.view.graph,
            policy,
        ) {
            Some(lookup) => {
                let mut virtual_alias = false;
                let checked = lookup
                    .validate_parents(|parent| {
                        virtual_alias |= runtime.view.contains_dependency_alias(parent)?;
                        runtime.view.translate(parent)
                    })
                    .map_err(|error| fail(error.code))?;
                if virtual_alias {
                    return Err(libc::ELOOP);
                }
                match &checked {
                    NativeLookup::NativeFailure { errno, .. } => return Err(*errno),
                    NativeLookup::Resolved { path, .. } => {
                        if runtime
                            .view
                            .contains_dependency_alias(path)
                            .map_err(|error| fail(error.code))?
                        {
                            return Err(libc::ELOOP);
                        }
                    }
                }
                translate_checked_lookup(&mut runtime, &path, checked)
                    .map_err(|error| fail(error.code))?
            }
            None => native_translation(&path, path.clone()),
        }
    } else {
        translate_lookup(&mut runtime, &path, follow_last).map_err(|error| fail(error.code))?
    };
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

unsafe fn descriptor_readonly(
    fd: c_int,
    runtime: &mut Runtime,
) -> std::result::Result<bool, c_int> {
    let saved_errno = *__error();
    // Check live kernel backing as well as remembered provenance. This also
    // covers an inherited read descriptor and the small open-to-track window;
    // physical managed backing cannot become writable by omitting a mapping.
    let mut path = [0u8; PATH_MAX as usize];
    if libc::fcntl(fd, F_GETPATH, path.as_mut_ptr()) != 0 {
        // Pipes, sockets and invalid descriptors retain the native operation's
        // result. A closed descriptor must not inherit a stale EROFS denial.
        errno(saved_errno);
        return Ok(false);
    }
    let path = Path::new(OsStr::from_bytes(
        CStr::from_ptr(path.as_ptr().cast()).to_bytes(),
    ));
    let readonly = if let Some(translation) = runtime.descriptors.get(&fd)
        && translation.physical == path
    {
        translation.readonly
    } else {
        runtime
            .view
            .translate(path)
            .map_err(|error| fail(error.code))?
            .readonly
    };
    errno(saved_errno);
    Ok(readonly)
}

unsafe fn mutate_descriptor(fd: c_int, native: impl FnOnce() -> c_int) -> c_int {
    let Some(_guard) = Guard::enter() else {
        return native();
    };
    let Some(runtime) = RUNTIME.get() else {
        return native();
    };
    let Ok(mut runtime) = runtime.lock() else {
        errno(fail(Code::PnportInjectionFailed));
        return -1;
    };
    match descriptor_readonly(fd, &mut runtime) {
        Ok(true) => {
            errno(EROFS);
            -1
        }
        Ok(false) => native(),
        Err(code) => {
            errno(code);
            -1
        }
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
        translated!($path, $dir, $write, true, $failure)
    };
    ($path:ident, $dir:expr, $write:expr, $follow:expr, $failure:expr) => {
        translated!($path, $dir, $write, $follow, SymlinkPolicy::Allow, $failure)
    };
    ($path:ident, $dir:expr, $write:expr, $follow:expr, $policy:expr, $failure:expr) => {
        match translate_following($path, $dir, $write, $follow, $policy) {
            Ok(value) => value,
            Err(error) => {
                errno(error);
                return $failure;
            }
        }
    };
}

fn initialize_owner() -> std::result::Result<(), InitializationStage> {
    use InitializationStage as Stage;
    let group = std::env::var("PNPORT_MACOS_GROUP")
        .ok()
        .and_then(|group| group.parse::<pid_t>().ok())
        .filter(|group| *group > 0)
        .ok_or(Stage::OwnedGroup)?;
    OWNED_GROUP.set(group).map_err(|_| Stage::OwnedGroup)?;
    let public = std::env::var("PNPORT_MACOS_OWNER_KEY").map_err(|_| Stage::OwnedGroup)?;
    let public =
        pnport_core::macos_process::decode_public_key(&public).map_err(|_| Stage::OwnedGroup)?;
    OWNER_KEY.set(public).map_err(|_| Stage::OwnedGroup)?;
    // SAFETY: getpid has no pointer or lifetime preconditions.
    register_owned_process(unsafe { getpid() }).map_err(|_| Stage::OwnedGroup)
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
        "PNPORT_MACOS_GROUP",
        "PNPORT_MACOS_OWNER_KEY",
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
    // Keep the lease outside the fallible setup closure. A failed constructor
    // must publish its stage/code before process exit releases the lease.
    let mut launch = None;
    let result: std::result::Result<(), InitializationStage> = (|| {
        use InitializationStage as Stage;
        // Acknowledge entry before graph/cache work that may legitimately wait
        // on another materializer. Only the later ready marker confirms setup.
        fs::create_dir_all(session.join("starting")).map_err(|_| Stage::AcknowledgeEntry)?;
        fs::write(session.join("starting").join(getpid().to_string()), b"1")
            .map_err(|_| Stage::AcknowledgeEntry)?;
        launch = if let Some(token) = std::env::var_os("PNPORT_LAUNCH_TOKEN") {
            let token = token.to_str().ok_or(Stage::LaunchToken)?;
            if !pnport_core::launch::valid_token(token) {
                return Err(Stage::LaunchToken);
            }
            // Link this exact pending inode before graph/cache coordination.
            // A stale acknowledgement with a reused filename cannot exempt a
            // different image from the missing-injection deadline.
            Some(
                pnport_core::launch::Entry::begin(&session, token)
                    .map_err(|_| Stage::AcknowledgeLaunch)?,
            )
        } else {
            None
        };
        let bytes = fs::read(session.join("graph.json")).map_err(|_| Stage::ReadGraph)?;
        let snapshot: Snapshot = serde_json::from_slice(&bytes).map_err(|_| Stage::DecodeGraph)?;
        let graph = Graph::from_snapshot(snapshot).map_err(|_| Stage::HydrateGraph)?;
        initialize_owner()?;
        let cache_path = std::env::var_os("PNPORT_CACHE").ok_or(Stage::CacheLocation)?;
        let cache = Cache::open(PathBuf::from(cache_path)).map_err(|_| Stage::OpenCache)?;
        let view = View::new(graph, cache, session.clone());
        RUNTIME
            .set(
                ForkMutex::new(Runtime {
                    view,
                    descriptors: HashMap::new(),
                    cwd: None,
                    directories: HashMap::new(),
                })
                .map_err(|_| Stage::RuntimeMutex)?,
            )
            .map_err(|_| Stage::InstallRuntime)?;
        if libc::pthread_atfork(Some(before_fork), Some(after_fork), Some(in_fork_child)) != 0 {
            return Err(Stage::RegisterForkHandlers);
        }
        fs::create_dir_all(session.join("ready")).map_err(|_| Stage::PublishReadiness)?;
        fs::write(session.join("ready").join(getpid().to_string()), b"1")
            .map_err(|_| Stage::PublishReadiness)?;
        if let Some(launch) = &launch {
            launch.acknowledge().map_err(|_| Stage::AcknowledgeLaunch)?;
        }
        Ok(())
    })();
    if let Err(stage) = result {
        #[cfg(test)]
        if launch.is_some() {
            // The isolated native failure control reaches this exact ordering
            // boundary, before publishing either diagnostic record.
            let token = std::env::var_os("PNPORT_LAUNCH_TOKEN").unwrap();
            let pending = fs::metadata(session.join("pending").join(&token)).unwrap();
            assert!(
                pnport_core::launch::entry_state(
                    &session.join("launch-starting").join(token),
                    &pending,
                )
                .unwrap()
                    == pnport_core::launch::EntryState::Initializing
            );
        }
        record_initialization_failure(&session, stage);
        record_failure(&session, Code::PnportInjectionFailed);
        _exit(125);
    }
}

#[cfg(test)]
mod initialization_tests {
    use super::*;

    #[test]
    fn initializer_process() {
        // Reached only if the native initializer did not reject the child.
    }

    #[test]
    fn failed_native_initializers_record_the_stage_without_input_contents() {
        for (bytes, token, expected) in [
            (None, None, InitializationStage::ReadGraph),
            (
                Some(b"private-input-canary".as_slice()),
                None,
                InitializationStage::DecodeGraph,
            ),
            (None, Some("pnport-test"), InitializationStage::ReadGraph),
            (
                None,
                Some("pnport-../outside"),
                InitializationStage::LaunchToken,
            ),
        ] {
            let session = tempfile::tempdir().unwrap();
            if let Some(bytes) = bytes {
                fs::write(session.path().join("graph.json"), bytes).unwrap();
            }
            let mut command = std::process::Command::new(std::env::current_exe().unwrap());
            command
                .args([
                    "pnport::initialization_tests::initializer_process",
                    "--exact",
                ])
                .env("PNPORT_SESSION", session.path())
                .env_remove("PNPORT_CACHE")
                .env_remove("PNPORT_LAUNCH_TOKEN");
            if let Some(token) = token {
                command.env("PNPORT_LAUNCH_TOKEN", token);
                if pnport_core::launch::valid_token(token) {
                    fs::create_dir(session.path().join("pending")).unwrap();
                    fs::write(session.path().join("pending").join(token), b"").unwrap();
                }
            }
            let status = command.status().unwrap();
            assert_eq!(status.code(), Some(125));
            let record = fs::read(session.path().join("initialization-failure")).unwrap();
            assert_eq!(
                serde_json::from_slice::<InitializationStage>(&record).unwrap(),
                expected
            );
            assert_eq!(
                fs::read(session.path().join("failure")).unwrap(),
                Code::PnportInjectionFailed.as_str().as_bytes()
            );
            assert!(record.len() < 64);
            assert!(session.path().join("starting").is_dir());
            assert!(!session.path().join("ready").exists());
            if token == Some("pnport-test") {
                use std::os::unix::fs::MetadataExt;
                let pending = fs::metadata(session.path().join("pending/pnport-test")).unwrap();
                let entered =
                    fs::metadata(session.path().join("launch-starting/pnport-test")).unwrap();
                assert_eq!(
                    (pending.dev(), pending.ino()),
                    (entered.dev(), entered.ino())
                );
            }
        }
    }
}
#[used]
#[cfg_attr(target_os = "macos", unsafe(link_section = "__DATA,__mod_init_func"))]
#[cfg_attr(target_os = "linux", unsafe(link_section = ".init_array"))]
static INITIALIZER: unsafe extern "C" fn() = initialize;

const fn open_follows_final_component(flags: c_int) -> bool {
    #[cfg(target_os = "macos")]
    let nofollow = libc::O_NOFOLLOW | libc::O_SYMLINK;
    #[cfg(target_os = "linux")]
    let nofollow = libc::O_NOFOLLOW;
    flags & nofollow == 0 && flags & (O_CREAT | libc::O_EXCL) != (O_CREAT | libc::O_EXCL)
}

const fn open_symlink_policy(flags: c_int) -> SymlinkPolicy {
    #[cfg(target_os = "macos")]
    if flags & libc::O_NOFOLLOW_ANY != 0 {
        return SymlinkPolicy::Reject;
    }
    let _ = flags;
    SymlinkPolicy::Allow
}

const fn conflicting_open_flags(flags: c_int) -> bool {
    // XNU rejects this pair before vnode lookup, so native forwarding cannot
    // mutate managed backing and preserves the kernel's argument-error order.
    #[cfg(target_os = "macos")]
    return flags & (libc::O_NOFOLLOW_ANY | libc::O_NOFOLLOW)
        == (libc::O_NOFOLLOW_ANY | libc::O_NOFOLLOW);
    #[cfg(target_os = "linux")]
    {
        let _ = flags;
        false
    }
}

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
    if conflicting_open_flags(flags) {
        return original(path, flags, mode);
    }
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
        open_follows_final_component(flags),
        open_symlink_policy(flags),
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
    if conflicting_open_flags(flags) {
        return original(dirfd, path, flags, mode);
    }
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
        open_follows_final_component(flags),
        open_symlink_policy(flags),
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
    ($name:ident, $wrapper:ident, ($path:ident: *const c_char $(,$arg:ident: $ty:ty)*) -> $ret:ty, $write:expr, $follow:expr, $failure:expr) => {
        hook!($name, $wrapper, ($path:*const c_char $(,$arg:$ty)*) -> $ret, {
            let original = original!($name, unsafe extern "C" fn(*const c_char $(,$ty)*) -> $ret);
            let Some(_guard) = Guard::enter() else { return original($path $(,$arg)*); };
            if RUNTIME.get().is_none() { return original($path $(,$arg)*); }
            let (path, _) = translated!($path, AT_FDCWD, $write, $follow, $failure);
            original(path.as_ptr() $(,$arg)*)
        });
    };
}
path_hook!(stat, pnport_stat, (path:*const c_char, output:*mut stat) -> c_int, false, true, -1);

path_hook!(access, pnport_access, (path:*const c_char, mode:c_int) -> c_int, mode & W_OK != 0, true, -1);

path_hook!(unlink, pnport_unlink, (path:*const c_char) -> c_int, true, false, -1);
path_hook!(rmdir, pnport_rmdir, (path:*const c_char) -> c_int, true, false, -1);
// Creating the native cache container is the sole writable operation on the
// merged root. Dependency entries and destructive root operations stay
// protected.
unsafe fn mkdir_path(path: *const c_char, fd: c_int) -> std::result::Result<CString, c_int> {
    let (_, translation) = translate_following(path, fd, false, false, SymlinkPolicy::Allow)?;
    if translation.kind == PathKind::CacheContainer {
        return CString::new(translation.logical.as_os_str().as_bytes()).map_err(|_| EINVAL);
    }
    if translation.readonly {
        return Err(EROFS);
    }
    CString::new(translation.physical.as_os_str().as_bytes()).map_err(|_| EINVAL)
}
hook!(mkdir, pnport_mkdir, (path:*const c_char, mode:mode_t) -> c_int, {
    let original=original!(mkdir,unsafe extern "C" fn(*const c_char,mode_t)->c_int);
    let Some(_guard)=Guard::enter() else {return original(path,mode);};
    if RUNTIME.get().is_none() {return original(path,mode);}
    let path=match mkdir_path(path,AT_FDCWD) {Ok(path)=>path,Err(code)=>{errno(code);return -1;}};
    original(path.as_ptr(),mode)
});
hook!(mkdirat, pnport_mkdirat, (fd:c_int,path:*const c_char,mode:mode_t) -> c_int, {
    let original=original!(mkdirat,unsafe extern "C" fn(c_int,*const c_char,mode_t)->c_int);
    let Some(_guard)=Guard::enter() else {return original(fd,path,mode);};
    if RUNTIME.get().is_none() {return original(fd,path,mode);}
    let path=match mkdir_path(path,fd) {Ok(path)=>path,Err(code)=>{errno(code);return -1;}};
    original(AT_FDCWD,path.as_ptr(),mode)
});
path_hook!(chmod, pnport_chmod, (path:*const c_char, mode:mode_t) -> c_int, true, true, -1);
path_hook!(truncate, pnport_truncate, (path:*const c_char, length:off_t) -> c_int, true, true, -1);
// pnport excludes fspy's generic mutation module. Every pathname mutation
// variant must therefore apply this view's policy before entering libc.
path_hook!(remove, pnport_remove, (path:*const c_char) -> c_int, true, false, -1);
path_hook!(creat, pnport_creat, (path:*const c_char, mode:mode_t) -> c_int, true, true, -1);
path_hook!(mkfifo, pnport_mkfifo, (path:*const c_char, mode:mode_t) -> c_int, true, false, -1);
path_hook!(mknod, pnport_mknod, (path:*const c_char, mode:mode_t, device:libc::dev_t) -> c_int, true, false, -1);
path_hook!(chown, pnport_chown, (path:*const c_char, owner:libc::uid_t, group:libc::gid_t) -> c_int, true, true, -1);
path_hook!(lchown, pnport_lchown, (path:*const c_char, owner:libc::uid_t, group:libc::gid_t) -> c_int, true, false, -1);
path_hook!(utime, pnport_utime, (path:*const c_char, times:*const libc::utimbuf) -> c_int, true, true, -1);
path_hook!(utimes, pnport_utimes, (path:*const c_char, times:*const libc::timeval) -> c_int, true, true, -1);
path_hook!(lutimes, pnport_lutimes, (path:*const c_char, times:*const libc::timeval) -> c_int, true, false, -1);
path_hook!(chflags, pnport_chflags, (path:*const c_char, flags:libc::c_uint) -> c_int, true, true, -1);
// Rust libc omits lchflags; retain Darwin's no-follow ABI explicitly.
unsafe extern "C" fn pnport_lchflags(path: *const c_char, flags: u32) -> c_int {
    let Some(_guard) = Guard::enter() else {
        return crate::libc::lchflags(path, flags);
    };
    if RUNTIME.get().is_none() {
        return crate::libc::lchflags(path, flags);
    }
    let (path, _) = translated!(path, AT_FDCWD, true, false, -1);
    crate::libc::lchflags(path.as_ptr(), flags)
}
const _: () = {
    #[used]
    #[unsafe(link_section = "__DATA,__interpose")]
    static mut ENTRY: crate::macros::InterposeEntry = crate::macros::InterposeEntry {
        _new: pnport_lchflags as *const c_void,
        _old: crate::libc::lchflags as *const c_void,
    };
};
hook!(setxattr, pnport_setxattr, (path:*const c_char,name:*const c_char,value:*const c_void,size:size_t,position:u32,options:c_int) -> c_int, {
    let original=original!(setxattr,unsafe extern "C" fn(*const c_char,*const c_char,*const c_void,size_t,u32,c_int)->c_int);
    let Some(_guard)=Guard::enter() else {return original(path,name,value,size,position,options);};
    if RUNTIME.get().is_none() {return original(path,name,value,size,position,options);}
    let (path,_)=translated!(path,AT_FDCWD,true,options & libc::XATTR_NOFOLLOW == 0,-1);
    original(path.as_ptr(),name,value,size,position,options)
});
path_hook!(removexattr, pnport_removexattr, (path:*const c_char,name:*const c_char,options:c_int) -> c_int, true, options & libc::XATTR_NOFOLLOW == 0, -1);

macro_rules! mutation_at_hook {
    ($name:ident, $wrapper:ident, ($fd:ident:c_int,$path:ident:*const c_char $(,$arg:ident:$ty:ty)*), $follow:expr) => {
        hook!($name, $wrapper, ($fd:c_int,$path:*const c_char $(,$arg:$ty)*) -> c_int, {
            let original=original!($name,unsafe extern "C" fn(c_int,*const c_char $(,$ty)*)->c_int);
            let Some(_guard)=Guard::enter() else {return original($fd,$path $(,$arg)*);};
            if RUNTIME.get().is_none() {return original($fd,$path $(,$arg)*);}
            let (path,_)=translated!($path,$fd,true,$follow,-1);
            original(AT_FDCWD,path.as_ptr() $(,$arg)*)
        });
    };
}
mutation_at_hook!(fchmodat, pnport_fchmodat, (fd:c_int,path:*const c_char,mode:mode_t,flags:c_int), flags & AT_SYMLINK_NOFOLLOW == 0);
mutation_at_hook!(fchownat, pnport_fchownat, (fd:c_int,path:*const c_char,owner:libc::uid_t,group:libc::gid_t,flags:c_int), flags & AT_SYMLINK_NOFOLLOW == 0);
mutation_at_hook!(utimensat, pnport_utimensat, (fd:c_int,path:*const c_char,times:*const libc::timespec,flags:c_int), flags & AT_SYMLINK_NOFOLLOW == 0);
mutation_at_hook!(mkfifoat, pnport_mkfifoat, (fd:c_int,path:*const c_char,mode:mode_t), false);
mutation_at_hook!(mknodat, pnport_mknodat, (fd:c_int,path:*const c_char,mode:mode_t,device:libc::dev_t), false);
hook!(fchmod, pnport_fchmod, (fd:c_int,mode:mode_t) -> c_int, {
    mutate_descriptor(fd, || libc::fchmod(fd, mode))
});
hook!(fchown, pnport_fchown, (fd:c_int,owner:libc::uid_t,group:libc::gid_t) -> c_int, {
    mutate_descriptor(fd, || libc::fchown(fd, owner, group))
});
hook!(ftruncate, pnport_ftruncate, (fd:c_int,length:off_t) -> c_int, {
    mutate_descriptor(fd, || libc::ftruncate(fd, length))
});
hook!(futimes, pnport_futimes, (fd:c_int,times:*const libc::timeval) -> c_int, {
    mutate_descriptor(fd, || libc::futimes(fd, times))
});
hook!(futimens, pnport_futimens, (fd:c_int,times:*const libc::timespec) -> c_int, {
    mutate_descriptor(fd, || libc::futimens(fd, times))
});
hook!(fchflags, pnport_fchflags, (fd:c_int,flags:libc::c_uint) -> c_int, {
    mutate_descriptor(fd, || libc::fchflags(fd, flags))
});
hook!(fsetxattr, pnport_fsetxattr, (fd:c_int,name:*const c_char,value:*const c_void,size:size_t,position:u32,options:c_int) -> c_int, {
    mutate_descriptor(fd, || libc::fsetxattr(fd, name, value, size, position, options))
});
hook!(fremovexattr, pnport_fremovexattr, (fd:c_int,name:*const c_char,options:c_int) -> c_int, {
    mutate_descriptor(fd, || libc::fremovexattr(fd, name, options))
});
hook!(dlopen, pnport_dlopen, (path:*const c_char,flags:c_int) -> *mut c_void, {
    let original = original!(dlopen, unsafe extern "C" fn(*const c_char,c_int)->*mut c_void);
    // NULL requests the process/global symbol namespace, not a filesystem path.
    if path.is_null() { return original(path,flags); }
    let Some(guard) = Guard::enter() else { return original(path,flags); };
    if RUNTIME.get().is_none() { return original(path,flags); }
    let (path,_) = translated!(path,AT_FDCWD,false,ptr::null_mut());
    // Translation has released the runtime lock. dyld now invokes arbitrary
    // library constructors, including nested loads and fork callbacks. Keep
    // their filesystem accesses virtualized instead of treating them as our
    // backing I/O. Inner hooks still guard their own native/runtime operations.
    drop(guard);
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
    let (path,translation) = translated!(path,dirfd,false,flags & AT_SYMLINK_NOFOLLOW == 0,-1);
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
    let (physical,translation) = translated!(path,AT_FDCWD,false,false,-1);
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
    let (physical,translation) = translated!(path,dirfd,false,false,-1);
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
unsafe fn close_descriptor(fd: c_int, native: unsafe extern "C" fn(c_int) -> c_int) -> c_int {
    let Some(guard) = Guard::enter() else {
        return native(fd);
    };
    let Some(runtime) = RUNTIME.get() else {
        return native(fd);
    };
    let Ok(mut runtime) = runtime.lock() else {
        errno(fail(Code::PnportInjectionFailed));
        return -1;
    };

    // Deferred pthread cancellation treats ordinary `close` as a cancellation
    // point on macOS. Keep a pending cancellation from terminating this thread
    // while it owns the runtime mutex; otherwise the process-wide lock would
    // remain permanently held. The cancellation state is restored only after
    // both the provenance update and the Rust guards have been released.
    let mut previous_cancel_state = 0;
    if set_cancel_state(libc::PTHREAD_CANCEL_DISABLE, &raw mut previous_cancel_state) != 0 {
        errno(EIO);
        return -1;
    }
    let result = native(fd);
    if result == 0 {
        runtime.descriptors.remove(&fd);
    }
    drop(runtime);
    drop(guard);
    // SAFETY: The state was disabled above on this thread. No runtime or
    // recursion guard is held when a pending cancellation may be delivered.
    if set_cancel_state(previous_cancel_state, ptr::null_mut()) != 0 {
        errno(EIO);
    }
    result
}
unsafe extern "C" {
    #[link_name = "pthread_setcancelstate"]
    fn set_cancel_state(state: c_int, old_state: *mut c_int) -> c_int;
    #[link_name = "close"]
    fn native_close(fd: c_int) -> c_int;
    #[link_name = "close$NOCANCEL"]
    fn native_close_nocancel(fd: c_int) -> c_int;
}
unsafe extern "C" fn pnport_close(fd: c_int) -> c_int {
    close_descriptor(fd, native_close)
}
unsafe extern "C" fn pnport_close_nocancel(fd: c_int) -> c_int {
    close_descriptor(fd, native_close_nocancel)
}
const _: () = {
    #[used]
    #[unsafe(link_section = "__DATA,__interpose")]
    static mut CLOSE: crate::macros::InterposeEntry = crate::macros::InterposeEntry {
        _new: pnport_close as _,
        _old: native_close as _,
    };
    #[used]
    #[unsafe(link_section = "__DATA,__interpose")]
    static mut NOCANCEL: crate::macros::InterposeEntry = crate::macros::InterposeEntry {
        _new: pnport_close_nocancel as _,
        _old: native_close_nocancel as _,
    };
};

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
    let entries=match directory_entries(&stream) {Ok(value)=>value,Err(code)=>{errno(code);return ptr::null_mut();}};
    if stream.virtual_end {errno(saved);return ptr::null_mut();}
    // Guard remains active while libc holds its stream lock. Any interposed
    // backing operations reenter only the original libc, never RUNTIME.
    errno(0);
    let entry=original(dir);
    if !entry.is_null() {

        errno(saved);return entry;
    }
    if *__error()!=0 {return entry;}
    errno(saved);
    match directory_entry(&mut stream,&entries) {Ok(value)=>value,Err(code)=>{errno(code);ptr::null_mut()}}
});

hook!(readdir_r, pnport_readdir_r, (dir:*mut DIR,entry:*mut dirent,result:*mut *mut dirent) -> c_int, {
    let original=original!(readdir_r,unsafe extern "C" fn(*mut DIR,*mut dirent,*mut *mut dirent)->c_int);
    let Some(_guard)=Guard::enter() else {return original(dir,entry,result);};
    let Some(stream)=directory_stream(dir) else {return original(dir,entry,result);};
    let saved=*__error();
    let Ok(mut stream)=stream.lock() else {return EIO;};
    let entries=match directory_entries(&stream) {Ok(value)=>value,Err(code)=>{errno(saved);return code;}};
    if stream.virtual_end {*result=ptr::null_mut();errno(saved);return 0;}
    let code=original(dir,entry,result);
    if code==0 && (*result).is_null() {
            match directory_entry(&mut stream,&entries) {
                Ok(value) if !value.is_null()=>{ptr::copy_nonoverlapping(value,entry,1);*result=entry;},
                Ok(_)=>{}, Err(code)=>{errno(saved);return code;}
            }
    }
    errno(saved);code
});

hook!(rewinddir, pnport_rewinddir, (dir:*mut DIR) -> (), {
    let original=original!(rewinddir,unsafe extern "C" fn(*mut DIR));
    let Some(_guard)=Guard::enter() else {return original(dir);};
    let stream=directory_stream(dir);
    let mut state=stream.as_ref().and_then(|stream|stream.lock().ok());
    if let Some(state)=state.as_mut() {state.position=0;state.virtual_end=false;}
    original(dir);
});
hook!(telldir, pnport_telldir, (dir:*mut DIR) -> libc::c_long, {
    let original=original!(telldir,unsafe extern "C" fn(*mut DIR)->libc::c_long);
    let Some(_guard)=Guard::enter() else {return original(dir);};
    if let Some(stream)=directory_stream(dir) && let Ok(stream)=stream.lock() && stream.position>0
        && let Ok(entries)=directory_entries(&stream) {return directory_cookie(stream.position,entries.len());}
    original(dir)
});
hook!(seekdir, pnport_seekdir, (dir:*mut DIR,position:libc::c_long) -> (), {
    let original=original!(seekdir,unsafe extern "C" fn(*mut DIR,libc::c_long));
    let Some(_guard)=Guard::enter() else {return original(dir,position);};
    let stream=directory_stream(dir);
    let mut state=stream.as_ref().and_then(|stream|stream.lock().ok());
    if let Some(state)=state.as_mut() {
        if let Ok(entries)=directory_entries(state) {
            let begin=directory_cookie(0,entries.len());
            if position>begin {
                state.position=usize::try_from(position-begin).expect("Reserved directory cookie is in range");
                state.virtual_end=state.position==entries.len();return;
            }
        }
        state.position=0;state.virtual_end=false;
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

mod directory;

#[derive(Clone, Copy)]
enum DirectoryCallbacks {
    Functions,
    Blocks,
}

// Only the common ABI prefix is read; callbacks do not escape scandir_b.
// https://clang.llvm.org/docs/Block-ABI-Apple.html#high-level
#[repr(C)]
struct DirectoryBlock<F> {
    _isa: *const c_void,
    _flags: c_int,
    _reserved: c_int,
    invoke: F,
}

unsafe extern "C" fn pnport_scandir(
    path: *const c_char,
    namelist: *mut c_void,
    select: *const c_void,
    compar: *const c_void,
) -> c_int {
    scan_directory(
        path,
        namelist,
        select,
        compar,
        DirectoryCallbacks::Functions,
    )
}

unsafe extern "C" fn pnport_scandir_b(
    path: *const c_char,
    namelist: *mut c_void,
    select: *const c_void,
    compar: *const c_void,
) -> c_int {
    scan_directory(path, namelist, select, compar, DirectoryCallbacks::Blocks)
}

unsafe fn scan_directory(
    path: *const c_char,
    namelist: *mut c_void,
    select: *const c_void,
    compar: *const c_void,
    callbacks: DirectoryCallbacks,
) -> c_int {
    let native = match callbacks {
        DirectoryCallbacks::Functions => crate::libc::scandir,
        DirectoryCallbacks::Blocks => crate::libc::scandir_b,
    };
    let Some(_guard) = Guard::enter() else {
        return native(path, namelist, select, compar);
    };
    if RUNTIME.get().is_none() {
        return native(path, namelist, select, compar);
    }
    let (path, translation) = translated!(path, AT_FDCWD, false, -1);
    let mut stream = DirectoryStream {
        logical: translation.logical,
        physical: translation.physical,
        position: 0,
        virtual_end: false,
        entry: Box::new(std::mem::zeroed()),
    };
    let overlay = match directory_entries(&stream) {
        Ok(value) => value,
        Err(code) => {
            errno(code);
            return -1;
        }
    };
    if overlay.is_empty() {
        let _callback_guard = CallbackGuard::enter();
        return native(path.as_ptr(), namelist, select, compar);
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
                let overlay = directory_entries(&stream)?;
                entry = directory_entry(&mut stream, &overlay)?;
                if entry.is_null() {
                    break;
                }
            }
            let keep = if select.is_null() {
                true
            } else {
                let _callback_guard = CallbackGuard::enter();
                match callbacks {
                    DirectoryCallbacks::Functions => {
                        let callback: unsafe extern "C" fn(*const dirent) -> c_int =
                            std::mem::transmute(select);
                        callback(entry) != 0
                    }
                    DirectoryCallbacks::Blocks => {
                        type Select = unsafe extern "C" fn(*const c_void, *const dirent) -> c_int;
                        let block = &*select.cast::<DirectoryBlock<Select>>();
                        (block.invoke)(select, entry) != 0
                    }
                }
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
    publish_directory_scan(entries, namelist, compar, callbacks)
}

unsafe fn publish_directory_scan(
    entries: Vec<*mut dirent>,
    namelist: *mut c_void,
    compar: *const c_void,
    callbacks: DirectoryCallbacks,
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
            let _callback_guard = CallbackGuard::enter();
            match callbacks {
                DirectoryCallbacks::Functions => {
                    let callback: unsafe extern "C" fn(*const c_void, *const c_void) -> c_int =
                        std::mem::transmute(compar);
                    libc::qsort(
                        list.cast(),
                        entries.len(),
                        std::mem::size_of::<*mut dirent>(),
                        Some(callback),
                    );
                }
                DirectoryCallbacks::Blocks => crate::libc::qsort_b(
                    list.cast(),
                    entries.len(),
                    std::mem::size_of::<*mut dirent>(),
                    compar,
                ),
            }
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
    #[used]
    #[unsafe(link_section = "__DATA,__interpose")]
    static mut BLOCKS: crate::macros::InterposeEntry = crate::macros::InterposeEntry {
        _new: pnport_scandir_b as *const c_void,
        _old: crate::libc::scandir_b as *const c_void,
    };
};
hook!(lstat, pnport_lstat, (path:*const c_char,output:*mut stat) -> c_int, {
    let original=original!(lstat,unsafe extern "C" fn(*const c_char,*mut stat)->c_int);
    let Some(_guard)=Guard::enter() else {return original(path,output);};
    if RUNTIME.get().is_none() {return original(path,output);}
    let (path,translation)=translated!(path,AT_FDCWD,false,false,-1);
    let result=original(path.as_ptr(),output);
    if result==0 {virtual_link_metadata(output,&translation);} result
});
hook!(rename, pnport_rename, (from:*const c_char,to:*const c_char) -> c_int, {
    let original=original!(rename,unsafe extern "C" fn(*const c_char,*const c_char)->c_int);
    let Some(_guard)=Guard::enter() else {return original(from,to);};
    if RUNTIME.get().is_none() {return original(from,to);}
    let (from,_)=translated!(from,AT_FDCWD,true,false,-1);let (to,_)=translated!(to,AT_FDCWD,true,false,-1);original(from.as_ptr(),to.as_ptr())
});
hook!(renameat, pnport_renameat, (from_fd:c_int,from:*const c_char,to_fd:c_int,to:*const c_char) -> c_int, {
    let original=original!(renameat,unsafe extern "C" fn(c_int,*const c_char,c_int,*const c_char)->c_int);
    let Some(_guard)=Guard::enter() else {return original(from_fd,from,to_fd,to);};
    if RUNTIME.get().is_none() {return original(from_fd,from,to_fd,to);}
    let (from,_)=translated!(from,from_fd,true,false,-1);
    let (to,_)=translated!(to,to_fd,true,false,-1);
    original(AT_FDCWD,from.as_ptr(),AT_FDCWD,to.as_ptr())
});
hook!(renamex_np, pnport_renamex, (from:*const c_char,to:*const c_char,flags:libc::c_uint) -> c_int, {
    let original=original!(renamex_np,unsafe extern "C" fn(*const c_char,*const c_char,libc::c_uint)->c_int);
    let Some(_guard)=Guard::enter() else {return original(from,to,flags);};
    if RUNTIME.get().is_none() {return original(from,to,flags);}
    let (from,_)=translated!(from,AT_FDCWD,true,false,-1);
    let (to,_)=translated!(to,AT_FDCWD,true,false,-1);
    original(from.as_ptr(),to.as_ptr(),flags)
});
hook!(renameatx_np, pnport_renameatx, (from_fd:c_int,from:*const c_char,to_fd:c_int,to:*const c_char,flags:libc::c_uint) -> c_int, {
    let original=original!(renameatx_np,unsafe extern "C" fn(c_int,*const c_char,c_int,*const c_char,libc::c_uint)->c_int);
    let Some(_guard)=Guard::enter() else {return original(from_fd,from,to_fd,to,flags);};
    if RUNTIME.get().is_none() {return original(from_fd,from,to_fd,to,flags);}
    let (from,_)=translated!(from,from_fd,true,false,-1);
    let (to,_)=translated!(to,to_fd,true,false,-1);
    original(AT_FDCWD,from.as_ptr(),AT_FDCWD,to.as_ptr(),flags)
});
hook!(link, pnport_link, (from:*const c_char,to:*const c_char) -> c_int, {
    let original=original!(link,unsafe extern "C" fn(*const c_char,*const c_char)->c_int);
    let Some(_guard)=Guard::enter() else {return original(from,to);};
    if RUNTIME.get().is_none() {return original(from,to);}
    // Creating another hard link changes the source inode's link count too.
    // Darwin link follows the source's final symlink, unlike unflagged linkat.
    let (from,_)=translated!(from,AT_FDCWD,true,true,-1);
    let (to,_)=translated!(to,AT_FDCWD,true,false,-1);
    original(from.as_ptr(),to.as_ptr())
});
hook!(linkat, pnport_linkat, (from_fd:c_int,from:*const c_char,to_fd:c_int,to:*const c_char,flags:c_int) -> c_int, {
    let original=original!(linkat,unsafe extern "C" fn(c_int,*const c_char,c_int,*const c_char,c_int)->c_int);
    let Some(_guard)=Guard::enter() else {return original(from_fd,from,to_fd,to,flags);};
    if RUNTIME.get().is_none() {return original(from_fd,from,to_fd,to,flags);}
    let (from,_)=translated!(from,from_fd,true,flags & libc::AT_SYMLINK_FOLLOW != 0,-1);
    let (to,_)=translated!(to,to_fd,true,false,-1);
    original(AT_FDCWD,from.as_ptr(),AT_FDCWD,to.as_ptr(),flags)
});
hook!(symlink, pnport_symlink, (target:*const c_char,path:*const c_char) -> c_int, {
    let original=original!(symlink,unsafe extern "C" fn(*const c_char,*const c_char)->c_int);
    let Some(_guard)=Guard::enter() else {return original(target,path);};
    if RUNTIME.get().is_none() {return original(target,path);}
    // The target is stored verbatim; only the new directory entry is mutated.
    let (path,_)=translated!(path,AT_FDCWD,true,false,-1);
    original(target,path.as_ptr())
});
hook!(symlinkat, pnport_symlinkat, (target:*const c_char,fd:c_int,path:*const c_char) -> c_int, {
    let original=original!(symlinkat,unsafe extern "C" fn(*const c_char,c_int,*const c_char)->c_int);
    let Some(_guard)=Guard::enter() else {return original(target,fd,path);};
    if RUNTIME.get().is_none() {return original(target,fd,path);}
    let (path,_)=translated!(path,fd,true,false,-1);
    original(target,AT_FDCWD,path.as_ptr())
});
hook!(unlinkat, pnport_unlinkat, (fd:c_int,path:*const c_char,flags:c_int) -> c_int, {
    let original=original!(unlinkat,unsafe extern "C" fn(c_int,*const c_char,c_int)->c_int);
    let Some(_guard)=Guard::enter() else {return original(fd,path,flags);};
    if RUNTIME.get().is_none() {return original(fd,path,flags);}
    let (path,_)=translated!(path,fd,true,false,-1);original(AT_FDCWD,path.as_ptr(),flags)
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
    let Some(runtime)=RUNTIME.get() else { return original(fd); };
    let Ok(mut runtime)=runtime.lock() else {errno(fail(Code::PnportInjectionFailed));return -1;};
    let result=original(fd);
    if result>=0 {let t=runtime.descriptors.get(&fd).cloned();runtime.descriptors.remove(&result);if let Some(t)=t {runtime.descriptors.insert(result,t);}} result
});
hook!(dup2, pnport_dup2, (fd:c_int,newfd:c_int) -> c_int, {
    let original=original!(dup2,unsafe extern "C" fn(c_int,c_int)->c_int);
    let Some(_guard)=Guard::enter() else {return original(fd,newfd);};
    let Some(runtime)=RUNTIME.get() else {return original(fd,newfd);};
    let Ok(mut runtime)=runtime.lock() else {errno(fail(Code::PnportInjectionFailed));return -1;};
    let result=original(fd,newfd);
    if result>=0 {let t=runtime.descriptors.get(&fd).cloned();runtime.descriptors.remove(&newfd);if let Some(t)=t {runtime.descriptors.insert(newfd,t);}} result
});

// Group/session changes keep native behavior only after the target birth is
// durably admitted. Cleanup addresses audit generations, never new group IDs.
hook!(setsid, pnport_setsid, () -> pid_t, {
    let original = original!(setsid, unsafe extern "C" fn()->pid_t);
    let Some(_guard) = Guard::enter() else { return original(); };
    if RUNTIME.get().is_none() || libc::getpid() == libc::getpgrp() { return original(); }
    if let Err(error) = admit_group_change(libc::getpid(), ProcessGroupOperation::Session) { errno(error); return -1; }
    original()
});
hook!(setpgid, pnport_setpgid, (pid:pid_t,group:pid_t) -> c_int, {
    let original = original!(setpgid, unsafe extern "C" fn(pid_t,pid_t)->c_int);
    let Some(_guard) = Guard::enter() else { return original(pid,group); };
    if RUNTIME.get().is_none() || pid < 0 || group < 0 { return original(pid,group); }
    let target = if pid == 0 { libc::getpid() } else { pid };
    // Preserve native errors for absent targets and non-child targets. Darwin
    // cannot move an unrelated process; its PID is never admitted by this call.
    let identity = pnport_core::macos_process::Identity::capture(target);
    match &identity {
        Err(error) if error.raw_os_error() == Some(libc::ESRCH) => return original(pid,group),
        Ok(identity) if target != libc::getpid() => {
            if pnport_core::macos_process::Identity::capture(libc::getpid()).is_ok_and(|current| current.birth != identity.parent_birth) {
                return original(pid,group);
            }
        }
        _ => (),
    }
    if let Err(error) = admit_group_change(libc::getpid(), ProcessGroupOperation::Group) { errno(error); return -1; }
    // Retain the first captured birth rather than capturing it again after
    // caller admission. Exit/exec in that interval receives a signed terminal
    // outcome, so libc can preserve ESRCH/EACCES without a whole-run failure.
    if let Err(error) = admit_group_identity(identity, ProcessGroupOperation::Group) { errno(error); return -1; }
    original(pid,group)
});
unsafe extern "C" {
    #[link_name = "setpgrp"]
    fn native_setpgrp() -> pid_t;
}
unsafe extern "C" fn pnport_setpgrp() -> pid_t {
    pnport_setpgid(0, 0)
}
const _: () = {
    #[used]
    #[unsafe(link_section = "__DATA,__interpose")]
    static mut ENTRY: crate::macros::InterposeEntry = crate::macros::InterposeEntry {
        _new: pnport_setpgrp as _,
        _old: native_setpgrp as _,
    };
};

static INJECTION_ENV: OnceLock<Vec<CString>> = OnceLock::new();
fn child_exec_error(error: &Error) -> c_int {
    // A failed native exec is recoverable by its caller (and libc's PATH
    // search). It did not launch an unmediated image. Only admission/runtime
    // failures without a native exec classification invalidate the session.
    match error.exec_failure {
        Some(ExecFailureKind::NotFound) => ENOENT,
        Some(ExecFailureKind::PermissionDenied) => libc::EACCES,
        Some(ExecFailureKind::InvalidFormat) => libc::ENOEXEC,
        Some(ExecFailureKind::InterpreterLoop) => libc::ELOOP,
        None => fail(error.code),
    }
}
fn admitted_program(path: &Path) -> std::result::Result<(CString, LaunchAdmission), c_int> {
    let admission = LaunchAdmission::new(path).map_err(|error| child_exec_error(&error))?;
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
            let mut runtime = runtime.lock().map_err(|_| {
                pnport_core::diagnostic::Error::new(
                    Code::PnportInjectionFailed,
                    "The native interception state is unavailable.",
                )
            })?;
            translate_lookup(&mut runtime, path, true)
        },
    )
    .map_err(|error| child_exec_error(&error))?;
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
            b"PNPORT_MACOS_GROUP=",
            b"PNPORT_MACOS_OWNER_KEY=",
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
    // Register the parent's current image before the kernel creates a child,
    // including POSIX_SPAWN_SETSID/SETPGROUP and environment replacement. Its
    // original-parent version proves ownership even before child initialization.
    if let Err(error) = admit_group_change(getpid(), ProcessGroupOperation::SpawnGroup) {
        return error;
    }
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
        let translation = {
            let mut runtime = runtime.lock().map_err(|_| EIO)?;
            translate_lookup(&mut runtime, &logical, true)
        };
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
