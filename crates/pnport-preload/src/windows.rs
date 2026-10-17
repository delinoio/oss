// SPDX-License-Identifier: Apache-2.0
#![allow(
    non_upper_case_globals,
    reason = "NT API constants retain their upstream names"
)]
//! pnport-only NT filesystem adapter. The Detours helper retains its upstream
//! MIT license in fspy_preload_windows; this adapter does not alter generic
//! fspy.

use std::{
    cell::Cell,
    collections::HashMap,
    ffi::{c_void, OsString},
    fs,
    os::windows::ffi::{OsStrExt, OsStringExt},
    path::{Path, PathBuf},
    ptr,
    sync::{Arc, Mutex, OnceLock, PoisonError, RwLock, RwLockReadGuard},
};

use ntapi::ntioapi::*;
use pnport_core::{
    cache::Cache,
    diagnostic::Code,
    graph::{Graph, Snapshot},
    launch::Entry,
    view::{PathKind, Translation, View},
    windows::{dos_path, file_path, LaunchContext},
};
use winapi::{
    shared::{
        minwindef::{BOOL, DWORD, HINSTANCE},
        ntdef::*,
    },
    um::{
        processthreadsapi::GetCurrentThread,
        synchapi::SetEvent,
        winnt::{ACCESS_MASK, DLL_PROCESS_ATTACH, DLL_PROCESS_DETACH},
    },
};

use crate::windows_detour::{AttachContext, Detour, DetourAny};

#[path = "windows_directory.rs"]
mod directory;
#[path = "windows_metadata.rs"]
mod metadata;
#[path = "windows_process.rs"]
mod process;

pub(crate) mod winapi_utils {
    pub fn ck_long(status: i32) -> winsafe::SysResult<()> {
        if status == 0 {
            Ok(())
        } else {
            Err(unsafe { winsafe::co::ERROR::from_raw(status as u32) })
        }
    }
}

const UNSUPPORTED: NTSTATUS = 0xc00000bbu32 as i32;
const DENIED: NTSTATUS = 0xc0000022u32 as i32;
const MISSING: NTSTATUS = 0xc0000034u32 as i32;
const INVALID: NTSTATUS = 0xc000000du32 as i32;
const IO_ERROR: NTSTATUS = 0xc0000185u32 as i32;

thread_local! { static ENTERED: Cell<bool> = const { Cell::new(false) }; }
struct Guard;
impl Guard {
    fn enter() -> Option<Self> {
        ENTERED
            .try_with(|entered| {
                if entered.replace(true) {
                    None
                } else {
                    Some(Self)
                }
            })
            .ok()
            .flatten()
    }
}
impl Drop for Guard {
    fn drop(&mut self) {
        let _ = ENTERED.try_with(|entered| entered.set(false));
    }
}

struct OpenFile {
    translation: Translation,
    listing: Mutex<Option<directory::Listing>>,
    /// Whether the native file object is associated with an I/O completion
    /// port, as set by `FileCompletionInformation` or changed/removed by
    /// `FileReplaceCompletionInformation`. Duplicated handles share this state
    /// because they share the `Arc<OpenFile>`, as they share the native file
    /// object. An association request holds the write lock across the native
    /// call and the state update, and a merged directory query holds the read
    /// lock for its whole emulation, so a query never observes a half-applied
    /// association.
    completion_port: RwLock<bool>,
}
impl OpenFile {
    fn completion_port(&self) -> RwLockReadGuard<'_, bool> {
        // The guarded value is a plain flag, so a poisoned lock is still valid.
        self.completion_port
            .read()
            .unwrap_or_else(PoisonError::into_inner)
    }
}
struct Runtime {
    view: View,
    handles: HashMap<usize, Arc<OpenFile>>,
    context: LaunchContext,
}
static RUNTIME: OnceLock<Mutex<Runtime>> = OnceLock::new();

fn fail(code: Code) -> NTSTATUS {
    if let Some(runtime) = RUNTIME.get() {
        if let Ok(runtime) = runtime.lock() {
            publish_failure(&runtime.context.session, code);
        }
    }
    IO_ERROR
}
fn publish_failure(session: &Path, code: Code) {
    // A fully written no-clobber publication preserves the first native error.
    if let Ok(temp) = tempfile::NamedTempFile::new_in(session) {
        if fs::write(temp.path(), code.as_str().as_bytes()).is_ok() {
            let _ = temp.persist_noclobber(session.join("failure"));
        }
    }
}
fn error_status(code: Code) -> NTSTATUS {
    if code == Code::PnportResolutionFailed {
        MISSING
    } else {
        fail(code)
    }
}

unsafe fn unicode(value: PUNICODE_STRING) -> std::result::Result<Vec<u16>, NTSTATUS> {
    if value.is_null() {
        return Err(INVALID);
    }
    let value = &*value;
    if !value.Length.is_multiple_of(2)
        || value.Length > value.MaximumLength
        || (value.Length != 0 && value.Buffer.is_null())
    {
        return Err(INVALID);
    }
    if value.Length == 0 {
        return Ok(Vec::new());
    }
    Ok(std::slice::from_raw_parts(value.Buffer, value.Length as usize / 2).to_vec())
}

fn handle(handle: HANDLE) -> Option<Arc<OpenFile>> {
    RUNTIME
        .get()?
        .lock()
        .ok()?
        .handles
        .get(&(handle as usize))
        .cloned()
}
fn track(handle: HANDLE, translation: Translation) {
    if let Some(runtime) = RUNTIME.get() {
        if let Ok(mut runtime) = runtime.lock() {
            runtime.handles.insert(
                handle as usize,
                Arc::new(OpenFile {
                    translation,
                    listing: Mutex::new(None),
                    completion_port: RwLock::new(false),
                }),
            );
        }
    }
}
unsafe fn object_path(
    attributes: POBJECT_ATTRIBUTES,
) -> std::result::Result<Option<PathBuf>, NTSTATUS> {
    if attributes.is_null() || (*attributes).ObjectName.is_null() {
        return Ok(None);
    }
    let name = unicode((*attributes).ObjectName)?;
    if name.is_empty() {
        return Ok(None);
    }
    // Pipes and console devices retain their native namespaces. Other device
    // namespaces cannot bypass graph classification by spelling a disk path
    // as a raw NT device; reject them before native execution.
    let spelling = String::from_utf16_lossy(&name);
    let starts = |prefix: &str| {
        spelling
            .get(..prefix.len())
            .is_some_and(|value| value.eq_ignore_ascii_case(prefix))
    };
    if starts(r"\Device\NamedPipe\")
        || spelling.eq_ignore_ascii_case(r"\??\NUL")
        || spelling.eq_ignore_ascii_case(r"\Device\ConDrv")
        || starts(r"\Device\ConDrv\")
        // The native DLL loader opens this control endpoint on Windows. Keep
        // its policy checks/errors native; it does not name a package file.
        // Replace this exact exception when generic device classification can
        // prove non-filesystem endpoints before opening them.
        || spelling.eq_ignore_ascii_case(r"\Device\SrpDevice")
        // Node's Windows crypto provider and Winsock use these non-filesystem
        // control endpoints. Keep their native access checks and completion
        // semantics. Remove these exact exceptions when endpoint classification
        // can distinguish control devices from raw filesystem paths.
        || spelling.eq_ignore_ascii_case(r"\Device\KsecDD")
        || spelling.eq_ignore_ascii_case(r"\Device\Afd\Endpoint")
    {
        return Ok(None);
    }
    if starts(r"\Device\") {
        tracing::debug!(
            action = "windows_namespace_rejected",
            "Rejected an unclassified NT device path"
        );
        return Err(UNSUPPORTED);
    }
    let path = dos_path(&name);
    if path.is_absolute() {
        return resolve_path(&path).map(Some);
    }
    let base = if !(*attributes).RootDirectory.is_null() {
        match handle((*attributes).RootDirectory) {
            Some(file) => file.translation.logical.clone(),
            None => file_path((*attributes).RootDirectory.cast()).map_err(|_| INVALID)?,
        }
    } else {
        std::env::current_dir().map_err(|_| INVALID)?
    };
    resolve_path(&base.join(path)).map(Some)
}

fn resolve_path(path: &Path) -> std::result::Result<PathBuf, NTSTATUS> {
    let result = {
        let runtime = RUNTIME
            .get()
            .ok_or(IO_ERROR)?
            .lock()
            .map_err(|_| IO_ERROR)?;
        pnport_core::windows::lookup_spelling(path, &runtime.view.graph)
    };
    result.map_err(|error| error_status(error.code))
}

fn translate(path: &Path, write: bool) -> std::result::Result<Translation, NTSTATUS> {
    let translated = {
        let mut runtime = RUNTIME
            .get()
            .ok_or(IO_ERROR)?
            .lock()
            .map_err(|_| IO_ERROR)?;
        runtime.view.translate(path)
    };
    let translated = translated.map_err(|error| error_status(error.code))?;
    if write && translated.readonly {
        return Err(DENIED);
    }
    if write && translated.kind == PathKind::ToolCache {
        for parent in path.ancestors() {
            if parent
                .file_name()
                .is_some_and(|name| name == "node_modules")
            {
                let issuer = parent.parent().ok_or(INVALID)?;
                let allowed = RUNTIME
                    .get()
                    .ok_or(IO_ERROR)?
                    .lock()
                    .map_err(|_| IO_ERROR)?
                    .view
                    .graph
                    .cache_container(issuer);
                if allowed {
                    match fs::create_dir(parent) {
                        Ok(()) => {}
                        Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => {}
                        Err(_) => return Err(IO_ERROR),
                    }
                    let checked = RUNTIME
                        .get()
                        .ok_or(IO_ERROR)?
                        .lock()
                        .map_err(|_| IO_ERROR)?
                        .view
                        .graph
                        .check_dependency_directory(issuer);
                    checked.map_err(|error| error_status(error.code))?;
                }
                break;
            }
        }
    }
    Ok(translated)
}

unsafe fn mapped(
    attributes: POBJECT_ATTRIBUTES,
    write: bool,
    call: impl FnOnce(POBJECT_ATTRIBUTES, Option<Translation>) -> NTSTATUS,
) -> NTSTATUS {
    let path = match object_path(attributes) {
        Ok(Some(path)) => path,
        Ok(None) => return call(attributes, None),
        Err(status) => return status,
    };
    let translation = match translate(&path, write) {
        Ok(value) => value,
        Err(status) => return status,
    };
    // Preserve native lookup semantics and object flags for unchanged paths.
    if translation.physical == path {
        return call(attributes, Some(translation));
    }
    let mut wide: Vec<_> = translation.physical.as_os_str().encode_wide().collect();
    // The NT object manager does not perform Win32 slash conversion. ZIP
    // suffixes originate in Yarn's portable paths and can still contain '/'.
    for unit in &mut wide {
        if *unit == 47 {
            *unit = 92;
        }
    }
    if !wide.starts_with(&[92, 63, 63, 92]) {
        let mut prefix: Vec<_> = r"\??\".encode_utf16().collect();
        if wide.starts_with(&[92, 92]) {
            prefix.extend("UNC\\".encode_utf16());
            wide.drain(..2);
        }
        prefix.extend(wide);
        wide = prefix;
    }
    let Ok(length) = u16::try_from(wide.len() * 2) else {
        return INVALID;
    };
    let mut name = UNICODE_STRING {
        Length: length,
        MaximumLength: length,
        Buffer: wide.as_mut_ptr(),
    };
    let mut attributes = ptr::read(attributes);
    attributes.RootDirectory = ptr::null_mut();
    attributes.ObjectName = &mut name;
    call(&mut attributes, Some(translation))
}

fn writable(file: HANDLE) -> bool {
    if let Some(file) = handle(file) {
        return !file.translation.readonly;
    }
    // Inherited/untracked disk handles still obey package protection. Pipes
    // and console handles retain their ordinary native stream semantics.
    if unsafe { winapi::um::fileapi::GetFileType(file) } != 1 {
        return true;
    }
    file_path(file.cast())
        .ok()
        .and_then(|path| translate(&path, false).ok())
        .is_some_and(|translation| !translation.readonly)
}
fn writes(access: ACCESS_MASK) -> bool {
    access & (0x40000000 | 0x10000000 | 0x02000000 | 0x000d0116) != 0
}

// Each detour has exactly the native signature; the imported helper resolves
// forwarded kernel32 exports to KernelBase before attaching the trampoline.
macro_rules! hook {
    ($static:ident, $native:path, $name:literal, ($($arg:ident: $ty:ty),*) -> $ret:ty, $body:block) => {
        static $static: Detour<unsafe extern "system" fn($($ty),*) -> $ret> = unsafe {
            Detour::new($name, $native, {
                unsafe extern "system" fn replacement($($arg:$ty),*) -> $ret $body
                replacement
            })
        };
    };
}
pub(super) use hook;

hook!(CREATE, ntapi::ntioapi::NtCreateFile, c"NtCreateFile", (out: PHANDLE, access: ACCESS_MASK, attrs: POBJECT_ATTRIBUTES, ios: PIO_STATUS_BLOCK, allocation: PLARGE_INTEGER, attributes: ULONG, share: ULONG, disposition: ULONG, options: ULONG, ea: PVOID, ea_len: ULONG) -> NTSTATUS, {
    let Some(_guard) = Guard::enter() else { return (CREATE.real())(out, access, attrs, ios, allocation, attributes, share, disposition, options, ea, ea_len); };
    if options & FILE_DIRECTORY_FILE != 0 && options & FILE_DELETE_ON_CLOSE == 0 && matches!(disposition, FILE_CREATE | FILE_OPEN_IF) {
        if let Ok(Some(path)) = object_path(attrs) {
            if path.file_name().is_some_and(|name| name == "node_modules") {
                if let Some(issuer) = path.parent() {
                    let checked = RUNTIME.get().and_then(|runtime| runtime.lock().ok()).and_then(|runtime| runtime.view.graph.cache_container(issuer).then(|| runtime.view.graph.check_dependency_directory(issuer)));
                    if let Some(checked) = checked {
                        if let Err(error) = checked { return error_status(error.code); }
                        let status = (CREATE.real())(out,access,attrs,ios,allocation,attributes,share,disposition,options,ea,ea_len);
                        if status >= 0 && !out.is_null() { track(*out,Translation { logical: path.clone(),physical: path,readonly:true,virtual_link:false,kind:PathKind::CacheContainer }); }
                        return status;
                    }
                }
            }
        }
    }
    mapped(attrs, writes(access) || disposition != FILE_OPEN || options & FILE_DELETE_ON_CLOSE != 0, |attrs, translation| {
        let status = (CREATE.real())(out, access, attrs, ios, allocation, attributes, share, disposition, options, ea, ea_len);
        if status >= 0 && !out.is_null() { if let Some(translation) = translation { track(*out, translation); } }
        status
    })
});
hook!(OPEN, ntapi::ntioapi::NtOpenFile, c"NtOpenFile", (out: PHANDLE, access: ACCESS_MASK, attrs: POBJECT_ATTRIBUTES, ios: PIO_STATUS_BLOCK, share: ULONG, options: ULONG) -> NTSTATUS, {
    let Some(_guard) = Guard::enter() else { return (OPEN.real())(out, access, attrs, ios, share, options); };
    mapped(attrs, writes(access) || options & FILE_DELETE_ON_CLOSE != 0, |attrs, translation| {
        let status = (OPEN.real())(out, access, attrs, ios, share, options);
        if status >= 0 && !out.is_null() { if let Some(translation) = translation { track(*out, translation); } }
        status
    })
});
hook!(ATTRS, ntapi::ntioapi::NtQueryAttributesFile, c"NtQueryAttributesFile", (attrs: POBJECT_ATTRIBUTES, info: PFILE_BASIC_INFORMATION) -> NTSTATUS, {
    let Some(_guard) = Guard::enter() else { return (ATTRS.real())(attrs, info); };
    mapped(attrs, false, |attrs, _| (ATTRS.real())(attrs, info))
});
hook!(FULL_ATTRS, ntapi::ntioapi::NtQueryFullAttributesFile, c"NtQueryFullAttributesFile", (attrs: POBJECT_ATTRIBUTES, info: PFILE_NETWORK_OPEN_INFORMATION) -> NTSTATUS, {
    let Some(_guard) = Guard::enter() else { return (FULL_ATTRS.real())(attrs, info); };
    mapped(attrs, false, |attrs, _| (FULL_ATTRS.real())(attrs, info))
});
hook!(DELETE, ntapi::ntioapi::NtDeleteFile, c"NtDeleteFile", (attrs: POBJECT_ATTRIBUTES) -> NTSTATUS, {
    let Some(_guard) = Guard::enter() else { return (DELETE.real())(attrs); };
    mapped(attrs,true,|attrs,_| (DELETE.real())(attrs))
});
hook!(WRITE, ntapi::ntioapi::NtWriteFile, c"NtWriteFile", (file: HANDLE, event: HANDLE, apc: PIO_APC_ROUTINE, context: PVOID, ios: PIO_STATUS_BLOCK, buffer: PVOID, length: ULONG, offset: PLARGE_INTEGER, key: PULONG) -> NTSTATUS, {
    let Some(_guard) = Guard::enter() else { return (WRITE.real())(file,event,apc,context,ios,buffer,length,offset,key); };
    if !writable(file) { return DENIED; }
    (WRITE.real())(file,event,apc,context,ios,buffer,length,offset,key)
});
/// Returns whether a successful completion-port request leaves the file object
/// associated. `FileCompletionInformation` always associates a port.
/// `FileReplaceCompletionInformation` (Windows 8.1+) changes the port, or
/// removes the association when `FILE_COMPLETION_INFORMATION::Port` is null.
/// Returns `None` when the replacement payload cannot be read.
unsafe fn completion_port_request(
    class: FILE_INFORMATION_CLASS,
    buffer: PVOID,
    length: ULONG,
) -> Option<bool> {
    if class == FileCompletionInformation {
        return Some(true);
    }
    if buffer.is_null() || (length as usize) < std::mem::size_of::<FILE_COMPLETION_INFORMATION>() {
        return None;
    }
    let request = ptr::read_unaligned(buffer.cast::<FILE_COMPLETION_INFORMATION>());
    Some(!request.Port.is_null())
}
hook!(SET_INFO, ntapi::ntioapi::NtSetInformationFile, c"NtSetInformationFile", (file: HANDLE, ios: PIO_STATUS_BLOCK, buffer: PVOID, length: ULONG, class: FILE_INFORMATION_CLASS) -> NTSTATUS, {
    let Some(_guard) = Guard::enter() else { return (SET_INFO.real())(file,ios,buffer,length,class); };
    if !writable(file) && !matches!(class, FilePositionInformation | FileCompletionInformation | FileReplaceCompletionInformation | FileIoPriorityHintInformation | FileIoCompletionNotificationInformation) { return DENIED; }
    // Rename and link destinations can target a virtual dependency even when
    // their source is native. Deny unsupported layouts before the syscall.
    if matches!(class, FileRenameInformation | FileRenameInformationEx | FileRenameInformationBypassAccessCheck | FileRenameInformationExBypassAccessCheck | FileLinkInformation | FileLinkInformationEx | FileLinkInformationBypassAccessCheck | FileLinkInformationExBypassAccessCheck) {
        let offset = std::mem::offset_of!(FILE_RENAME_INFORMATION, FileName);
        if buffer.is_null() || (length as usize) < offset { return INVALID; }
        let value = &*buffer.cast::<FILE_RENAME_INFORMATION>();
        if value.FileNameLength % 2 != 0 || offset + value.FileNameLength as usize > length as usize { return INVALID; }
        let mut name = UNICODE_STRING { Length: match u16::try_from(value.FileNameLength) { Ok(n) => n, Err(_) => return INVALID }, MaximumLength: value.FileNameLength as u16, Buffer: value.FileName.as_ptr().cast_mut() };
        let mut attrs: OBJECT_ATTRIBUTES = std::mem::zeroed();
        attrs.Length = std::mem::size_of::<OBJECT_ATTRIBUTES>() as u32;
        attrs.RootDirectory = value.RootDirectory;
        attrs.ObjectName = &mut name;
        let target_name = match unicode(&mut name) { Ok(value) => dos_path(&value), Err(status) => return status };
        if attrs.RootDirectory.is_null() && !target_name.is_absolute() {
            let Some(source) = handle(file) else { return UNSUPPORTED; };
            let Some(parent) = source.translation.logical.parent() else { return INVALID; };
            let target = parent.join(target_name);
            if !target.is_absolute() { return INVALID; }
            if let Err(status) = translate(&target, true) { return status; }
        } else { match object_path(&mut attrs) { Ok(Some(path)) => if let Err(status) = translate(&path, true) { return status; }, _ => return UNSUPPORTED } }
    }
    if matches!(class, FileCompletionInformation | FileReplaceCompletionInformation) {
        if let Some(owned) = handle(file) {
            // Serialize the native association request and the recorded state
            // against merged directory queries on this file object. The
            // requested state is read before the call; it is recorded only if
            // the native request succeeds so a rejected request never changes
            // directory query behavior.
            let requested = completion_port_request(class, buffer, length);
            let mut associated = owned.completion_port.write().unwrap_or_else(PoisonError::into_inner);
            let status = (SET_INFO.real())(file,ios,buffer,length,class);
            if status >= 0 {
                // An unreadable replacement request cannot succeed natively;
                // fail closed by treating the object as associated.
                *associated = requested.unwrap_or(true);
                tracing::debug!(
                    action = "windows_completion_port_recorded",
                    class,
                    associated = *associated,
                    "Recorded an I/O completion port association change on a managed handle"
                );
            }
            return status;
        }
    }
    (SET_INFO.real())(file,ios,buffer,length,class)
});
hook!(CLOSE, ntapi::ntobapi::NtClose, c"NtClose", (file: HANDLE) -> NTSTATUS, {
    let Some(_guard) = Guard::enter() else { return (CLOSE.real())(file); };
    let status = (CLOSE.real())(file);
    if status >= 0 { if let Some(runtime) = RUNTIME.get() { if let Ok(mut runtime) = runtime.lock() { runtime.handles.remove(&(file as usize)); } } }
    status
});
hook!(DUPLICATE, ntapi::ntobapi::NtDuplicateObject, c"NtDuplicateObject", (source_process: HANDLE, source: HANDLE, target_process: HANDLE, target: PHANDLE, access: ACCESS_MASK, attributes: ULONG, options: ULONG) -> NTSTATUS, {
    let Some(_guard) = Guard::enter() else { return (DUPLICATE.real())(source_process,source,target_process,target,access,attributes,options); };
    let current = winapi::um::processthreadsapi::GetCurrentProcess();
    let owned = handle(source);
    // Remote duplication cannot carry logical provenance safely. Do not
    // silently transfer a managed backing handle to an unmediated process.
    if owned.is_some() && (source_process != current || target_process != current) { return UNSUPPORTED; }
    let status = (DUPLICATE.real())(source_process,source,target_process,target,access,attributes,options);
    if let Some(runtime) = RUNTIME.get() { if let Ok(mut runtime) = runtime.lock() {
        if options & 1 != 0 { runtime.handles.remove(&(source as usize)); }
        if status >= 0 && !target.is_null() { if let Some(owned) = owned { runtime.handles.insert(*target as usize, owned); } }
    } }
    status
});
hook!(SECTION, ntapi::ntmmapi::NtCreateSection, c"NtCreateSection", (out: PHANDLE, access: ACCESS_MASK, attrs: POBJECT_ATTRIBUTES, size: PLARGE_INTEGER, protection: ULONG, attributes: ULONG, file: HANDLE) -> NTSTATUS, {
    let Some(_guard) = Guard::enter() else { return (SECTION.real())(out,access,attrs,size,protection,attributes,file); };
    // Copy-on-write mappings do not modify the package. Writable shared views
    // and image creation with write access must never mutate managed backing.
    if !file.is_null() && !writable(file) && protection & (0x04 | 0x40) != 0 { return DENIED; }
    (SECTION.real())(out,access,attrs,size,protection,attributes,file)
});

const FILE_DETOURS: &[DetourAny] = &[
    CREATE.as_any(),
    OPEN.as_any(),
    ATTRS.as_any(),
    FULL_ATTRS.as_any(),
    WRITE.as_any(),
    SET_INFO.as_any(),
    CLOSE.as_any(),
    DUPLICATE.as_any(),
    SECTION.as_any(),
    DELETE.as_any(),
];

unsafe fn initialize() -> std::io::Result<()> {
    let context = crate::windows_launch::context()?;
    let _guard = Guard::enter().ok_or(std::io::ErrorKind::Other)?;
    let _ = tracing_subscriber::fmt()
        .with_ansi(false)
        .with_target(false)
        .without_time()
        .with_writer(std::io::stderr)
        .with_max_level(if context.debug {
            tracing::Level::DEBUG
        } else {
            tracing::Level::ERROR
        })
        .try_init();
    let result = (|| {
        pnport_core::cache::private_dir(&context.session)
            .map_err(|_| std::io::ErrorKind::PermissionDenied)?;
        let entry = Entry::begin(&context.session, &context.launch)?;
        let bytes = fs::read(context.session.join("graph.json"))?;
        if bytes.len() > 64 * 1024 * 1024 {
            return Err(std::io::ErrorKind::InvalidData.into());
        }
        let snapshot: Snapshot =
            serde_json::from_slice(&bytes).map_err(|_| std::io::ErrorKind::InvalidData)?;
        let graph = Graph::from_snapshot(snapshot).map_err(|_| std::io::ErrorKind::InvalidData)?;
        let cache =
            Cache::open(context.cache.clone()).map_err(|_| std::io::ErrorKind::PermissionDenied)?;
        let view = View::new(graph, cache, context.session.clone());
        RUNTIME
            .set(Mutex::new(Runtime {
                view,
                handles: HashMap::new(),
                context: context.clone(),
            }))
            .map_err(|_| std::io::ErrorKind::AlreadyExists)?;
        let ctx = AttachContext::new().map_err(|_| std::io::ErrorKind::Unsupported)?;
        winapi_utils::ck_long(fspy_detours_sys::DetourTransactionBegin())
            .map_err(|_| std::io::ErrorKind::Other)?;
        let attached = (|| {
            winapi_utils::ck_long(fspy_detours_sys::DetourUpdateThread(
                GetCurrentThread().cast(),
            ))?;
            for detour in FILE_DETOURS
                .iter()
                .chain(directory::DETOURS)
                .chain(process::DETOURS)
                .chain(metadata::DETOURS)
            {
                detour.attach(&ctx)?;
            }
            winapi_utils::ck_long(fspy_detours_sys::DetourTransactionCommit())
        })();
        if attached.is_err() {
            fspy_detours_sys::DetourTransactionAbort();
            return Err(std::io::ErrorKind::Other.into());
        }
        fs::write(
            context.session.join("ready").join(&context.launch),
            b"ready\n",
        )?;
        entry.acknowledge()?;
        Ok(())
    })();
    if result.is_err() {
        publish_failure(&context.session, Code::PnportInjectionFailed);
    }
    result
}

#[no_mangle]
unsafe extern "system" fn DllMain(_: HINSTANCE, reason: DWORD, _: *mut c_void) -> BOOL {
    if fspy_detours_sys::DetourIsHelperProcess() != 0 {
        return 0;
    }
    if reason == DLL_PROCESS_ATTACH {
        if fspy_detours_sys::DetourRestoreAfterWith() == 0 || initialize().is_err() {
            return 0;
        }
    } else if reason == DLL_PROCESS_DETACH {
        // During process teardown all owned handles and leases are released by
        // Windows. Do not reopen runtime state under the loader's detach lock.
        let _ = ENTERED.try_with(|entered| entered.set(true));
    }
    1
}
