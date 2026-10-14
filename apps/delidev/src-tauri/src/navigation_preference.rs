// SPDX-License-Identifier: Apache-2.0
use std::{
    fs::{self, File, Metadata, OpenOptions},
    io::{Read, Write},
    path::{Path, PathBuf},
    sync::Mutex,
};

use serde::{Deserialize, Serialize};

const DOCUMENT_LIMIT: u64 = 4096;
const LEASE_NAME: &str = "navigation-preference.lock";

#[derive(Clone, Copy, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum NavigationProblem {
    Unavailable,
    ReadFailed,
    InvalidDocument,
    UnsupportedVersion,
    WriteFailed,
    OutcomeUnknown,
    Changed,
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct NavigationSnapshot {
    pub revision: u32,
    pub usage_pinned: bool,
    pub problem: Option<NavigationProblem>,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Document {
    version: u32,
    usage_pinned: bool,
}

// These observations stay private. IPC never exposes bytes or file identities.
#[derive(Clone, PartialEq, Eq)]
struct Observation {
    parent: FileIdentity,
    document: ObservedDocument,
}

#[derive(Clone, PartialEq, Eq)]
enum ObservedDocument {
    Missing,
    Present {
        bytes: Vec<u8>,
        identity: FileIdentity,
        version: u32,
        preference: bool,
    },
}

impl Observation {
    fn preference(&self) -> bool {
        match &self.document {
            ObservedDocument::Missing => false,
            ObservedDocument::Present { preference, .. } => *preference,
        }
    }
}

struct State {
    snapshot: NavigationSnapshot,
    observation: Option<Observation>,
}

/// One process-wide device preference, independent of every server connection.
/// The state mutex orders process revisions. A separate persistent sidecar
/// coordinates participating processes from inspection through publication.
pub struct NavigationStore {
    path: Option<PathBuf>,
    state: Mutex<State>,
    presentation: Mutex<()>,
}

impl NavigationStore {
    pub fn new(config_dir: Option<PathBuf>) -> Self {
        Self {
            path: config_dir.map(|directory| directory.join("navigation_preference.json")),
            presentation: Mutex::new(()),
            state: Mutex::new(State {
                snapshot: NavigationSnapshot {
                    revision: 0,
                    usage_pinned: false,
                    problem: Some(NavigationProblem::Unavailable),
                },
                observation: None,
            }),
        }
    }

    pub fn presentation(&self) -> std::sync::MutexGuard<'_, ()> {
        self.presentation
            .lock()
            .unwrap_or_else(|error| error.into_inner())
    }

    /// Retained presentation only; auxiliary views cannot inspect preference
    /// files. Product operations acquire presentation, state, then sidecar.
    pub fn current(&self) -> NavigationSnapshot {
        self.state
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .snapshot
            .clone()
    }

    pub fn read(&self) -> NavigationSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        let previous = state.snapshot.clone();
        let result = self
            .path
            .as_deref()
            .ok_or(NavigationProblem::Unavailable)
            .and_then(|path| {
                let lease = NavigationLease::acquire(path, NavigationProblem::ReadFailed)?;
                inspect(path, &lease)
            });
        match result {
            Ok(observation) => {
                state.snapshot.usage_pinned = observation.preference();
                state.snapshot.problem = None;
                // A Read adopts exact evidence even when the enum is unchanged.
                state.observation = Some(observation);
            }
            Err(problem) => {
                // Invalid external storage cannot replace the committed pin
                // state.
                state.snapshot.problem = Some(problem);
                tracing::warn!(operation = "navigation_preference_read", ?problem);
            }
        }
        if state.snapshot.revision == 0
            || state.snapshot.usage_pinned != previous.usage_pinned
            || state.snapshot.problem != previous.problem
        {
            advance(&mut state.snapshot);
        }
        state.snapshot.clone()
    }

    pub fn update(&self, usage_pinned: bool, expected_revision: u32) -> NavigationSnapshot {
        self.update_with(usage_pinned, expected_revision, persist)
    }

    fn update_with(
        &self,
        usage_pinned: bool,
        expected_revision: u32,
        write: impl FnOnce(
            &Path,
            bool,
            &Observation,
            &NavigationLease,
        ) -> Result<Observation, NavigationProblem>,
    ) -> NavigationSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        if state.snapshot.revision != expected_revision {
            let mut snapshot = state.snapshot.clone();
            snapshot.problem = Some(NavigationProblem::Changed);
            return snapshot;
        }
        if state.snapshot.problem.is_some() || state.snapshot.revision == u32::MAX {
            return state.snapshot.clone();
        }
        let result = self
            .path
            .as_deref()
            .ok_or(NavigationProblem::Unavailable)
            .and_then(|path| {
                let lease = NavigationLease::acquire(path, NavigationProblem::WriteFailed)?;
                let original = state
                    .observation
                    .as_ref()
                    .ok_or(NavigationProblem::Changed)?;
                // Invalid/newer/linked evidence retains its own classification.
                // Equal enums cannot authorize replacing different bytes/files.
                if inspect(path, &lease)? != *original {
                    return Err(NavigationProblem::Changed);
                }
                write(path, usage_pinned, original, &lease)
            });
        match result {
            Ok(observation) => {
                state.snapshot.usage_pinned = observation.preference();
                state.observation = Some(observation);
                tracing::info!(
                    operation = "navigation_preference_update",
                    state = "committed"
                );
            }
            Err(problem) => {
                state.snapshot.problem = Some(problem);
                tracing::warn!(operation = "navigation_preference_update", ?problem);
            }
        }
        advance(&mut state.snapshot);
        state.snapshot.clone()
    }
}

fn advance(state: &mut NavigationSnapshot) {
    // Never wrap into an older revision. Exhaustion stays read-only until
    // restart.
    if let Some(revision) = state.revision.checked_add(1) {
        state.revision = revision;
    } else {
        state.problem = Some(NavigationProblem::Unavailable);
    }
}

#[derive(Clone, Copy, PartialEq, Eq)]
struct FileIdentity {
    #[cfg(unix)]
    device: u64,
    #[cfg(unix)]
    inode: u64,
    #[cfg(windows)]
    volume: u64,
    #[cfg(windows)]
    file: [u8; 16],
}

fn identity(file: &File) -> Result<(FileIdentity, u64), NavigationProblem> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        let metadata = file.metadata().map_err(|_| NavigationProblem::ReadFailed)?;
        Ok((
            FileIdentity {
                device: metadata.dev(),
                inode: metadata.ino(),
            },
            metadata.nlink(),
        ))
    }
    #[cfg(windows)]
    {
        use std::{ffi::c_void, os::windows::io::AsRawHandle};
        #[repr(C)]
        #[derive(Default)]
        struct HandleInformation {
            attributes: u32,
            creation: [u32; 2],
            access: [u32; 2],
            write: [u32; 2],
            volume: u32,
            size_high: u32,
            size_low: u32,
            links: u32,
            file_index: [u32; 2],
        }
        #[repr(C)]
        #[derive(Default)]
        struct FileIdInformation {
            volume: u64,
            file: [u8; 16],
        }
        #[link(name = "kernel32")]
        unsafe extern "system" {
            fn GetFileInformationByHandle(handle: *mut c_void, info: *mut HandleInformation)
            -> i32;
            fn GetFileInformationByHandleEx(
                handle: *mut c_void,
                class: i32,
                info: *mut c_void,
                size: u32,
            ) -> i32;
        }
        let mut info = HandleInformation::default();
        let mut id = FileIdInformation::default();
        // FileIdInfo (18) supplies the full 128-bit identity required by ReFS.
        // Unsupported filesystems fail closed; the legacy file index is not
        // used.
        if unsafe { GetFileInformationByHandle(file.as_raw_handle(), &mut info) } == 0
            || unsafe {
                GetFileInformationByHandleEx(
                    file.as_raw_handle(),
                    18,
                    (&mut id as *mut FileIdInformation).cast(),
                    std::mem::size_of::<FileIdInformation>() as u32,
                )
            } == 0
        {
            return Err(NavigationProblem::ReadFailed);
        }
        Ok((
            FileIdentity {
                volume: id.volume,
                file: id.file,
            },
            info.links as u64,
        ))
    }
    #[cfg(not(any(unix, windows)))]
    {
        let _ = file;
        Err(NavigationProblem::ReadFailed)
    }
}

fn validate_metadata(metadata: &Metadata, directory: bool) -> Result<(), NavigationProblem> {
    if if directory {
        !metadata.is_dir()
    } else {
        !metadata.is_file()
    } {
        return Err(NavigationProblem::InvalidDocument);
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        if metadata.uid() != unsafe { libc::geteuid() } || (!directory && metadata.nlink() != 1) {
            return Err(NavigationProblem::InvalidDocument);
        }
    }
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        if metadata.file_attributes() & 0x400 != 0 {
            return Err(NavigationProblem::InvalidDocument);
        }
    }
    Ok(())
}

fn read_options(directory: bool) -> OpenOptions {
    let mut options = OpenOptions::new();
    options.read(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.custom_flags(libc::O_NOFOLLOW | if directory { libc::O_DIRECTORY } else { 0 });
    }
    #[cfg(windows)]
    {
        use std::os::windows::fs::OpenOptionsExt;
        // Inspect the reparse object; directory handles need backup semantics.
        options.custom_flags(0x00200000 | if directory { 0x02000000 } else { 0 });
    }
    options
}

fn open_evidence(path: &Path, directory: bool) -> Result<(File, FileIdentity), NavigationProblem> {
    let metadata = fs::symlink_metadata(path).map_err(|_| NavigationProblem::ReadFailed)?;
    validate_metadata(&metadata, directory)?;
    let file = read_options(directory)
        .open(path)
        .map_err(|_| NavigationProblem::ReadFailed)?;
    validate_metadata(
        &file.metadata().map_err(|_| NavigationProblem::ReadFailed)?,
        directory,
    )?;
    let (opened_identity, links) = identity(&file)?;
    if !directory && links != 1 {
        return Err(NavigationProblem::InvalidDocument);
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        if metadata.dev() != opened_identity.device || metadata.ino() != opened_identity.inode {
            return Err(NavigationProblem::Changed);
        }
    }
    Ok((file, opened_identity))
}

struct NavigationLease {
    // Never unlink or replace the sidecar: its persistent identity owns exclusion.
    file: File,
    lock_identity: FileIdentity,
    parent: File,
    parent_identity: FileIdentity,
    directory: PathBuf,
}

impl NavigationLease {
    fn acquire(path: &Path, problem: NavigationProblem) -> Result<Self, NavigationProblem> {
        let result = (|| {
            let directory = path.parent().ok_or(NavigationProblem::ReadFailed)?;
            fs::create_dir_all(directory).map_err(|_| NavigationProblem::ReadFailed)?;
            let (parent, parent_identity) = open_evidence(directory, true)?;
            let mut options = OpenOptions::new();
            options.read(true).write(true).create(true).truncate(false);
            #[cfg(unix)]
            {
                use std::os::unix::fs::OpenOptionsExt;
                options.mode(0o600).custom_flags(libc::O_NOFOLLOW);
            }
            #[cfg(windows)]
            {
                use std::os::windows::fs::OpenOptionsExt;
                options.custom_flags(0x00200000); // FILE_FLAG_OPEN_REPARSE_POINT
            }
            let file = options
                .open(directory.join(LEASE_NAME))
                .map_err(|_| NavigationProblem::ReadFailed)?;
            let metadata = file.metadata().map_err(|_| NavigationProblem::ReadFailed)?;
            validate_metadata(&metadata, false)?;
            #[cfg(unix)]
            {
                use std::os::unix::fs::MetadataExt;
                if metadata.mode() & 0o077 != 0 {
                    return Err(NavigationProblem::ReadFailed);
                }
            }
            let (lock_identity, links) = identity(&file)?;
            if links != 1 {
                return Err(NavigationProblem::ReadFailed);
            }
            file.try_lock().map_err(|error| {
                let code = match error {
                    fs::TryLockError::WouldBlock => "lease-busy",
                    fs::TryLockError::Error(_) => "lease-unavailable",
                };
                tracing::warn!(operation = "navigation_preference_lease", code);
                NavigationProblem::ReadFailed
            })?;
            let lease = Self {
                file,
                lock_identity,
                parent,
                parent_identity,
                directory: directory.into(),
            };
            lease.validate()?;
            Ok(lease)
        })();
        result.map_err(|_| problem)
    }

    fn validate(&self) -> Result<(), NavigationProblem> {
        let (_, parent_identity) = open_evidence(&self.directory, true)?;
        let (_, lock_identity) = open_evidence(&self.directory.join(LEASE_NAME), false)?;
        if parent_identity != self.parent_identity
            || identity(&self.parent)?.0 != self.parent_identity
            || lock_identity != self.lock_identity
            || identity(&self.file)?.1 != 1
        {
            return Err(NavigationProblem::Changed);
        }
        Ok(())
    }
}

impl Drop for NavigationLease {
    fn drop(&mut self) {
        // Explicit unlock also releases a lease inherited briefly across fork.
        if self.file.unlock().is_err() {
            tracing::warn!(
                operation = "navigation_preference_lease",
                code = "lease-release-failed"
            );
        }
    }
}

fn inspect(path: &Path, lease: &NavigationLease) -> Result<Observation, NavigationProblem> {
    lease.validate()?;
    let metadata = match fs::symlink_metadata(path) {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            lease.validate()?;
            return Ok(Observation {
                parent: lease.parent_identity,
                document: ObservedDocument::Missing,
            });
        }
        Err(_) => return Err(NavigationProblem::ReadFailed),
    };
    validate_metadata(&metadata, false)?;
    if metadata.len() > DOCUMENT_LIMIT {
        return Err(NavigationProblem::InvalidDocument);
    }
    let (file, file_identity) = open_evidence(path, false)?;
    let before = file.metadata().map_err(|_| NavigationProblem::ReadFailed)?;
    if before.len() > DOCUMENT_LIMIT {
        return Err(NavigationProblem::InvalidDocument);
    }
    let mut bytes = Vec::new();
    (&file)
        .take(DOCUMENT_LIMIT + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| NavigationProblem::ReadFailed)?;
    let after = file.metadata().map_err(|_| NavigationProblem::ReadFailed)?;
    validate_metadata(&after, false)?;
    if bytes.len() as u64 > DOCUMENT_LIMIT {
        return Err(NavigationProblem::InvalidDocument);
    }
    if before.len() != bytes.len() as u64 || after.len() != before.len() {
        return Err(NavigationProblem::Changed);
    }
    let (_, current_identity) = open_evidence(path, false)?;
    if current_identity != file_identity || identity(&file)? != (file_identity, 1) {
        return Err(NavigationProblem::Changed);
    }
    lease.validate()?;
    let document: Document =
        serde_json::from_slice(&bytes).map_err(|_| NavigationProblem::InvalidDocument)?;
    if document.version != 1 {
        return Err(NavigationProblem::UnsupportedVersion);
    }
    Ok(Observation {
        parent: lease.parent_identity,
        document: ObservedDocument::Present {
            bytes,
            identity: file_identity,
            version: document.version,
            preference: document.usage_pinned,
        },
    })
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum PersistStep {
    ScratchCreated,
    ScratchSynced,
    DestinationVerified,
    Published,
    /// Parent-directory sync that makes a verified Unix rollback durable.
    /// Failure is OutcomeUnknown.
    RollbackSynced,
    /// Persistence barrier for the published file object (Windows flushes the
    /// verified handle; Unix relies on the later parent sync). Failure is
    /// OutcomeUnknown.
    PublishBarrier,
    ScratchUnlinking,
    ScratchRemoved,
    /// Persistence barrier for the published directory entry (Unix parent
    /// sync, Windows first-save directory flush). Failure is OutcomeUnknown.
    DirectoryBarrier,
    DirectorySynced,
    ReceiptVerified,
}

/// Outcome of a refused or rolled back publication attempt.
struct PublishFailure {
    problem: NavigationProblem,
    /// True when the destination pathname may differ from its observed state.
    touched: bool,
    /// True when a verified rollback reversed a rename. The reversal is not
    /// durable until the parent directory is synced, so the attempt stays
    /// touched until then.
    reversed: bool,
}

impl PublishFailure {
    fn untouched(problem: NavigationProblem) -> Self {
        Self {
            problem,
            touched: false,
            reversed: false,
        }
    }

    #[cfg(unix)]
    fn reversed(problem: NavigationProblem) -> Self {
        Self {
            problem,
            touched: false,
            reversed: true,
        }
    }

    #[cfg(unix)]
    fn touched(problem: NavigationProblem) -> Self {
        Self {
            problem,
            touched: true,
            reversed: false,
        }
    }
}

#[cfg(unix)]
#[derive(Clone, Copy, PartialEq, Eq)]
enum RenameMode {
    /// Move the source only when the target does not exist.
    NoReplace,
    /// Atomically swap two existing entries.
    Exchange,
}

/// Atomic, single-syscall rename variants. Pathname-only rename cannot refuse a
/// clobber or report what it displaced, so unsupported platforms and
/// filesystems fail closed instead of falling back to it.
#[cfg(unix)]
fn rename_checked(source: &Path, target: &Path, mode: RenameMode) -> std::io::Result<()> {
    use std::{ffi::CString, os::unix::ffi::OsStrExt};
    let invalid = |_| std::io::Error::from(std::io::ErrorKind::InvalidInput);
    let source = CString::new(source.as_os_str().as_bytes()).map_err(invalid)?;
    let target = CString::new(target.as_os_str().as_bytes()).map_err(invalid)?;
    #[cfg(target_vendor = "apple")]
    let result = unsafe {
        libc::renamex_np(
            source.as_ptr(),
            target.as_ptr(),
            match mode {
                RenameMode::NoReplace => libc::RENAME_EXCL,
                RenameMode::Exchange => libc::RENAME_SWAP,
            },
        )
    };
    #[cfg(any(target_os = "linux", target_os = "android"))]
    let result = unsafe {
        libc::syscall(
            libc::SYS_renameat2,
            libc::AT_FDCWD,
            source.as_ptr(),
            libc::AT_FDCWD,
            target.as_ptr(),
            match mode {
                RenameMode::NoReplace => libc::RENAME_NOREPLACE,
                RenameMode::Exchange => libc::RENAME_EXCHANGE,
            },
        )
    };
    #[cfg(not(any(target_vendor = "apple", target_os = "linux", target_os = "android")))]
    let result = {
        let _ = (source, target, mode);
        return Err(std::io::Error::from(std::io::ErrorKind::Unsupported));
    };
    #[cfg(any(target_vendor = "apple", target_os = "linux", target_os = "android"))]
    if result == 0 {
        Ok(())
    } else {
        Err(std::io::Error::last_os_error())
    }
}

// Test seam that runs between the Unix rename syscall and the identity reads
// that follow it, the window in which a late writer can alter either entry.
#[cfg(all(test, unix))]
type RenameHook = Box<dyn FnMut(&Path, &Path)>;

#[cfg(all(test, unix))]
thread_local! {
    static AFTER_RENAME: std::cell::RefCell<Option<RenameHook>> =
        const { std::cell::RefCell::new(None) };
}

#[cfg(all(test, unix))]
fn run_after_rename_hook(scratch: &Path, target: &Path) {
    AFTER_RENAME.with(|hook| {
        if let Some(hook) = hook.borrow_mut().as_mut() {
            hook(scratch, target);
        }
    });
}

/// Device/inode of the entry itself (symlinks are not followed).
#[cfg(unix)]
fn entry_identity(path: &Path) -> std::io::Result<Option<FileIdentity>> {
    use std::os::unix::fs::MetadataExt;
    match fs::symlink_metadata(path) {
        Ok(metadata) => Ok(Some(FileIdentity {
            device: metadata.dev(),
            inode: metadata.ino(),
        })),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(error) => Err(error),
    }
}

/// Handle options that can rename or delete the verified file object itself.
#[cfg(windows)]
fn bind_options() -> OpenOptions {
    use std::os::windows::fs::OpenOptionsExt;
    let mut options = OpenOptions::new();
    // GENERIC_READ | GENERIC_WRITE | DELETE, inspecting the reparse object.
    options
        .access_mode(0x8000_0000 | 0x4000_0000 | 0x0001_0000)
        .custom_flags(0x00200000);
    options
}

/// Renames the file object behind `file` to `target` without consulting the
/// pathname it was opened through. `replace` is false for create-new.
#[cfg(windows)]
fn rename_handle(file: &File, target: &Path, replace: bool) -> std::io::Result<()> {
    use std::{
        ffi::c_void,
        os::windows::{ffi::OsStrExt, io::AsRawHandle},
    };
    // FILE_RENAME_INFO; the flags union starts with the ReplaceIfExists byte.
    #[repr(C)]
    struct RenameInfo {
        replace_if_exists: u32,
        root_directory: *mut c_void,
        name_bytes: u32,
        name: [u16; 1],
    }
    #[link(name = "kernel32")]
    unsafe extern "system" {
        fn SetFileInformationByHandle(
            handle: *mut c_void,
            class: i32,
            info: *mut c_void,
            size: u32,
        ) -> i32;
    }
    const FILE_RENAME_INFO_CLASS: i32 = 3;
    let name: Vec<u16> = target.as_os_str().encode_wide().collect();
    let offset = std::mem::offset_of!(RenameInfo, name);
    let size = offset + name.len() * 2 + 2;
    // u64 backing storage satisfies the struct alignment on every target.
    let mut buffer = vec![0u64; size.div_ceil(8)];
    let base = buffer.as_mut_ptr().cast::<u8>();
    unsafe {
        let header = base.cast::<RenameInfo>();
        (*header).replace_if_exists = u32::from(replace);
        (*header).root_directory = std::ptr::null_mut();
        (*header).name_bytes = (name.len() * 2) as u32;
        std::ptr::copy_nonoverlapping(name.as_ptr(), base.add(offset).cast::<u16>(), name.len());
    }
    if unsafe {
        SetFileInformationByHandle(
            file.as_raw_handle(),
            FILE_RENAME_INFO_CLASS,
            base.cast(),
            size as u32,
        )
    } == 0
    {
        Err(std::io::Error::last_os_error())
    } else {
        Ok(())
    }
}

/// Unlinks the file object behind `file`, whatever its pathname now resolves
/// to. POSIX semantics remove the name immediately instead of at last close.
#[cfg(windows)]
fn delete_handle(file: &File) -> std::io::Result<()> {
    use std::{ffi::c_void, os::windows::io::AsRawHandle};
    #[link(name = "kernel32")]
    unsafe extern "system" {
        fn SetFileInformationByHandle(
            handle: *mut c_void,
            class: i32,
            info: *mut c_void,
            size: u32,
        ) -> i32;
    }
    const FILE_DISPOSITION_INFO_EX_CLASS: i32 = 21;
    // FILE_DISPOSITION_FLAG_DELETE | FILE_DISPOSITION_FLAG_POSIX_SEMANTICS
    let mut flags: u32 = 0x1 | 0x2;
    if unsafe {
        SetFileInformationByHandle(
            file.as_raw_handle(),
            FILE_DISPOSITION_INFO_EX_CLASS,
            (&mut flags as *mut u32).cast(),
            std::mem::size_of::<u32>() as u32,
        )
    } == 0
    {
        Err(std::io::Error::last_os_error())
    } else {
        Ok(())
    }
}

/// Verifies that a scratch pathname is gone after its object was unlinked.
/// A POSIX-semantics delete removes the link from the visible namespace only
/// once the delete handle is closed, so callers release that handle first;
/// checking while it is open would report the owned scratch as a foreign entry.
#[cfg(windows)]
fn verify_unlinked(path: &Path) -> Result<(), NavigationProblem> {
    match fs::symlink_metadata(path) {
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Ok(_) => Err(NavigationProblem::Changed),
        Err(_) => Err(NavigationProblem::WriteFailed),
    }
}

/// Persistence barrier for a file object that was just published by a handle
/// rename. `SetFileInformationByHandle` has no write-through option, so the
/// verified handle is flushed (`FlushFileBuffers` on Windows) before the save
/// may be reported as committed. The handle is proven to still be the created
/// file object first; every failure is OutcomeUnknown so the new selection is
/// never adopted. This does not establish power-loss durability, which is
/// untested.
#[cfg(windows)]
fn flush_published(file: &File, expected: FileIdentity) -> Result<(), NavigationProblem> {
    match identity(file) {
        Ok((found, _)) if found == expected => {}
        _ => {
            tracing::warn!(
                operation = "navigation_preference_publish",
                code = "publish-barrier-identity-mismatch"
            );
            return Err(NavigationProblem::OutcomeUnknown);
        }
    }
    file.sync_all().map_err(|error| {
        tracing::warn!(
            operation = "navigation_preference_publish",
            code = "publish-barrier-failed",
            kind = ?error.kind()
        );
        NavigationProblem::OutcomeUnknown
    })
}

/// Win32 error codes that mean the volume or ACL does not support flushing a
/// directory handle (`ERROR_INVALID_FUNCTION`, `ERROR_ACCESS_DENIED`,
/// `ERROR_INVALID_HANDLE`, `ERROR_NOT_SUPPORTED`). They are an unavailable
/// best-effort barrier, not evidence that the published entry is lost.
/// `ERROR_INVALID_HANDLE` is only reached after the directory handle passed
/// the leased-parent identity check, so it means the filesystem or redirector
/// rejects `FlushFileBuffers` on directory handles.
#[cfg(windows)]
const DIRECTORY_FLUSH_UNSUPPORTED_CODES: [i32; 4] = [1, 5, 6, 50];

#[cfg(windows)]
fn directory_flush_is_unsupported(error: &std::io::Error) -> bool {
    error
        .raw_os_error()
        .is_some_and(|code| DIRECTORY_FLUSH_UNSUPPORTED_CODES.contains(&code))
}

/// Persistence barrier for a directory entry created by a Windows first save.
/// `FlushFileBuffers` on a directory handle needs write access, so it opens a
/// dedicated handle, proves it is still the leased parent and flushes it.
/// A volume that reports the directory flush as unsupported (see
/// `DIRECTORY_FLUSH_UNSUPPORTED_CODES`) keeps the already write-through
/// published file and the barrier is skipped with a warning. An identity
/// mismatch or any other open/flush failure is reported as OutcomeUnknown so
/// the new selection is never adopted. This does not establish power-loss
/// durability, which is untested.
#[cfg(windows)]
fn flush_directory_entry(
    directory: &Path,
    expected: FileIdentity,
) -> Result<(), NavigationProblem> {
    flush_directory_entry_with(directory, expected, open_directory_for_flush)
}

/// Opens the directory with the access `FlushFileBuffers` requires:
/// GENERIC_WRITE, backup semantics for directories, inspect reparse objects.
#[cfg(windows)]
fn open_directory_for_flush(directory: &Path) -> std::io::Result<File> {
    use std::os::windows::fs::OpenOptionsExt;
    let mut options = OpenOptions::new();
    options
        .access_mode(0x4000_0000)
        .custom_flags(0x02000000 | 0x00200000);
    options.open(directory)
}

/// `flush_directory_entry` with an injectable directory opener so the identity
/// and unsupported-open paths stay testable on hosts whose volumes or ACLs
/// decide whether `GENERIC_WRITE` directory opens succeed.
#[cfg(windows)]
fn flush_directory_entry_with(
    directory: &Path,
    expected: FileIdentity,
    open: impl FnOnce(&Path) -> std::io::Result<File>,
) -> Result<(), NavigationProblem> {
    use std::{ffi::c_void, os::windows::io::AsRawHandle};
    #[link(name = "kernel32")]
    unsafe extern "system" {
        fn FlushFileBuffers(handle: *mut c_void) -> i32;
    }
    let handle = match open(directory) {
        Ok(handle) => handle,
        Err(error) if directory_flush_is_unsupported(&error) => {
            tracing::warn!(
                operation = "navigation_preference_publish",
                code = "directory-barrier-unsupported",
                stage = "open",
                os_error = ?error.raw_os_error()
            );
            return Ok(());
        }
        Err(error) => {
            tracing::warn!(
                operation = "navigation_preference_publish",
                code = "directory-barrier-open-failed",
                kind = ?error.kind()
            );
            return Err(NavigationProblem::OutcomeUnknown);
        }
    };
    match identity(&handle) {
        Ok((found, _)) if found == expected => {}
        _ => {
            tracing::warn!(
                operation = "navigation_preference_publish",
                code = "directory-barrier-identity-mismatch"
            );
            return Err(NavigationProblem::OutcomeUnknown);
        }
    }
    if unsafe { FlushFileBuffers(handle.as_raw_handle()) } == 0 {
        let error = std::io::Error::last_os_error();
        if directory_flush_is_unsupported(&error) {
            tracing::warn!(
                operation = "navigation_preference_publish",
                code = "directory-barrier-unsupported",
                stage = "flush",
                os_error = ?error.raw_os_error()
            );
            return Ok(());
        }
        tracing::warn!(
            operation = "navigation_preference_publish",
            code = "directory-barrier-failed",
            kind = ?error.kind()
        );
        return Err(NavigationProblem::OutcomeUnknown);
    }
    Ok(())
}

/// The exclusively created scratch file. Publication and cleanup are bound to
/// the object this save created and verified, never to a bare pathname:
/// Windows renames or deletes through the verified handle; Unix has no
/// handle-bound primitive, so it uses atomic no-replace/exchange renames that
/// are re-checked by identity and rolled back when they moved foreign entries.
struct Scratch {
    path: PathBuf,
    identity: FileIdentity,
    parent_identity: FileIdentity,
    /// Creation handle, released once the bytes are synced.
    file: Option<File>,
    /// Handle opened after sync and proven to be the created file object.
    bound: Option<File>,
    /// Unix identity this save expects at `path` for cleanup: the scratch
    /// itself, or the replaced document after a successful exchange.
    #[cfg(unix)]
    residual: FileIdentity,
    consumed: bool,
}

impl Scratch {
    fn create(path: PathBuf) -> Result<Self, NavigationProblem> {
        let (_, parent_identity) =
            open_evidence(path.parent().ok_or(NavigationProblem::WriteFailed)?, true)?;
        let mut options = OpenOptions::new();
        options.write(true).create_new(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600).custom_flags(libc::O_NOFOLLOW);
        }
        #[cfg(windows)]
        {
            use std::os::windows::fs::OpenOptionsExt;
            options.custom_flags(0x00200000);
        }
        let file = options
            .open(&path)
            .map_err(|_| NavigationProblem::WriteFailed)?;
        // A create failure grants no ownership, even on a scratch-name
        // collision.
        let (identity, _) = identity(&file).map_err(|_| NavigationProblem::WriteFailed)?;
        Ok(Self {
            path,
            identity,
            parent_identity,
            file: Some(file),
            bound: None,
            #[cfg(unix)]
            residual: identity,
            consumed: false,
        })
    }

    fn open_bound(&self) -> std::io::Result<File> {
        #[cfg(windows)]
        {
            bind_options().open(&self.path)
        }
        #[cfg(not(windows))]
        {
            read_options(false).open(&self.path)
        }
    }

    /// Opens the scratch pathname once and proves the handle is this save's
    /// exclusively created, unlinked-elsewhere-free regular file. Windows then
    /// publishes and deletes through this very handle.
    fn bind(&mut self) -> Result<(), NavigationProblem> {
        let source = self
            .open_bound()
            .map_err(|_| NavigationProblem::WriteFailed)?;
        validate_metadata(
            &source
                .metadata()
                .map_err(|_| NavigationProblem::WriteFailed)?,
            false,
        )?;
        if identity(&source)? != (self.identity, 1) {
            return Err(NavigationProblem::Changed);
        }
        self.bound = Some(source);
        Ok(())
    }

    /// Barrier after a successful publication, before cleanup releases the
    /// bound handle. Windows flushes the renamed file object for both Missing
    /// and Present destinations; Unix syncs the parent directory afterwards.
    fn publish_barrier(&self) -> Result<(), NavigationProblem> {
        #[cfg(windows)]
        {
            let file = self
                .bound
                .as_ref()
                .ok_or(NavigationProblem::OutcomeUnknown)?;
            flush_published(file, self.identity)
        }
        #[cfg(not(windows))]
        {
            Ok(())
        }
    }

    #[cfg(unix)]
    fn publish(
        &mut self,
        target: &Path,
        document: &ObservedDocument,
    ) -> Result<(), PublishFailure> {
        match document {
            ObservedDocument::Missing => self.publish_unix(target, RenameMode::NoReplace, None),
            ObservedDocument::Present { identity, .. } => {
                self.publish_unix(target, RenameMode::Exchange, Some(*identity))
            }
        }
    }

    /// Moves or swaps the scratch entry, then proves by identity that the
    /// entry that moved is the one this save created. Anything else (a foreign
    /// entry swapped in after verification, or a document changed by a writer
    /// that ignored the lease) is moved back so the destination keeps its
    /// observed state and the foreign entry stays at the scratch pathname.
    #[cfg(unix)]
    fn publish_unix(
        &mut self,
        target: &Path,
        mode: RenameMode,
        replaced: Option<FileIdentity>,
    ) -> Result<(), PublishFailure> {
        if let Some(expected) = replaced {
            // Refuse before touching anything when the destination is already
            // known to differ from the observed document, so a change that
            // precedes the exchange never needs a reversal.
            match entry_identity(target) {
                Ok(Some(found)) if found == expected => {}
                Ok(_) => {
                    tracing::warn!(
                        operation = "navigation_preference_publish",
                        code = "destination-changed-before-exchange"
                    );
                    return Err(PublishFailure::untouched(NavigationProblem::Changed));
                }
                Err(_) => return Err(PublishFailure::untouched(NavigationProblem::WriteFailed)),
            }
        }
        if let Err(error) = rename_checked(&self.path, target, mode) {
            tracing::warn!(
                operation = "navigation_preference_publish",
                code = "rename-refused",
                kind = ?error.kind()
            );
            return Err(PublishFailure::untouched(match error.kind() {
                std::io::ErrorKind::AlreadyExists | std::io::ErrorKind::NotFound => {
                    NavigationProblem::Changed
                }
                _ => NavigationProblem::WriteFailed,
            }));
        }
        #[cfg(test)]
        run_after_rename_hook(&self.path, target);
        let published = entry_identity(target).ok().flatten();
        let displaced = entry_identity(&self.path).ok();
        let ours = published == Some(self.identity);
        let displaced_as_observed = match mode {
            RenameMode::NoReplace => true,
            RenameMode::Exchange => displaced == Some(replaced),
        };
        if ours && displaced_as_observed {
            match mode {
                RenameMode::NoReplace => self.consumed = true,
                RenameMode::Exchange => {
                    // The replaced document now sits at the scratch pathname.
                    self.residual = replaced
                        .ok_or(PublishFailure::touched(NavigationProblem::OutcomeUnknown))?;
                }
            }
            return Ok(());
        }
        // An exchange that placed this save's file but displaced something
        // other than the observed document cannot be told apart from a late
        // replacement of the displaced entry at the scratch pathname. Reversing
        // would move that possibly foreign entry into the destination, so keep
        // it at the scratch pathname as evidence and report the outcome as
        // unknown instead of claiming a verified rollback.
        let can_reverse = match mode {
            RenameMode::NoReplace => true,
            RenameMode::Exchange => displaced == Some(replaced),
        };
        if !can_reverse {
            tracing::error!(
                operation = "navigation_preference_publish",
                code = "displaced-entry-unverified",
                rolling_back = false
            );
            return Err(PublishFailure::touched(NavigationProblem::OutcomeUnknown));
        }
        tracing::warn!(
            operation = "navigation_preference_publish",
            code = "unexpected-entry-published",
            rolling_back = true
        );
        let rollback = match mode {
            RenameMode::NoReplace => rename_checked(target, &self.path, RenameMode::NoReplace),
            RenameMode::Exchange => rename_checked(&self.path, target, RenameMode::Exchange),
        };
        // Verified only when the destination holds its observed state again
        // (absent, or the observed document) and the foreign entry sits at the
        // scratch pathname.
        let restored = rollback.is_ok()
            && entry_identity(&self.path).ok() == Some(published)
            && match mode {
                RenameMode::NoReplace => entry_identity(target).ok() == Some(None),
                RenameMode::Exchange => entry_identity(target).ok() == Some(replaced),
            };
        if restored {
            // The foreign entry stays at the scratch pathname as evidence.
            Err(PublishFailure::reversed(NavigationProblem::Changed))
        } else {
            tracing::error!(
                operation = "navigation_preference_publish",
                code = "rollback-unverified"
            );
            Err(PublishFailure::touched(NavigationProblem::OutcomeUnknown))
        }
    }

    #[cfg(windows)]
    fn publish(
        &mut self,
        target: &Path,
        document: &ObservedDocument,
    ) -> Result<(), PublishFailure> {
        let file = self
            .bound
            .as_ref()
            .ok_or(PublishFailure::untouched(NavigationProblem::WriteFailed))?;
        let replace = matches!(document, ObservedDocument::Present { .. });
        match rename_handle(file, target, replace) {
            Ok(()) => {
                self.consumed = true;
                Ok(())
            }
            Err(error) => {
                tracing::warn!(
                    operation = "navigation_preference_publish",
                    code = "rename-refused",
                    kind = ?error.kind()
                );
                Err(PublishFailure::untouched(
                    if error.kind() == std::io::ErrorKind::AlreadyExists {
                        NavigationProblem::Changed
                    } else {
                        NavigationProblem::WriteFailed
                    },
                ))
            }
        }
    }

    #[cfg(not(any(unix, windows)))]
    fn publish(
        &mut self,
        _target: &Path,
        _document: &ObservedDocument,
    ) -> Result<(), PublishFailure> {
        Err(PublishFailure::untouched(NavigationProblem::WriteFailed))
    }

    /// Removes only the entry this save owns. Any other entry at the scratch
    /// pathname is retained as evidence and reported as `Changed`.
    #[cfg(unix)]
    fn remove(
        &mut self,
        step: &mut impl FnMut(PersistStep, &Path) -> Result<(), NavigationProblem>,
    ) -> Result<(), NavigationProblem> {
        self.file.take();
        self.bound.take();
        if self.consumed {
            return Ok(());
        }
        let directory = self.path.parent().ok_or(NavigationProblem::WriteFailed)?;
        let (_, parent_identity) = open_evidence(directory, true)?;
        if parent_identity != self.parent_identity {
            return Err(NavigationProblem::Changed);
        }
        match entry_identity(&self.path) {
            Ok(None) => {
                self.consumed = true;
                return Ok(());
            }
            Ok(Some(found)) if found == self.residual => {}
            Ok(Some(_)) => return Err(NavigationProblem::Changed),
            Err(_) => return Err(NavigationProblem::WriteFailed),
        }
        step(PersistStep::ScratchUnlinking, &self.path)?;
        // Unix cannot unlink by identity. Atomically claim whatever the
        // pathname names now under a private name, verify that claim, and only
        // then unlink it. A foreign entry is moved back untouched.
        let claim = directory.join(format!(
            ".navigation_preference-{}.claim",
            uuid::Uuid::now_v7()
        ));
        match rename_checked(&self.path, &claim, RenameMode::NoReplace) {
            Ok(()) => {}
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                self.consumed = true;
                return Ok(());
            }
            Err(_) => return Err(NavigationProblem::WriteFailed),
        }
        if entry_identity(&claim).ok() != Some(Some(self.residual)) {
            let restored = rename_checked(&claim, &self.path, RenameMode::NoReplace).is_ok();
            tracing::warn!(
                operation = "navigation_preference_update",
                code = "scratch-cleanup-foreign-entry",
                restored
            );
            return Err(NavigationProblem::Changed);
        }
        fs::remove_file(&claim).map_err(|_| NavigationProblem::WriteFailed)?;
        self.consumed = true;
        Ok(())
    }

    #[cfg(windows)]
    fn remove(
        &mut self,
        step: &mut impl FnMut(PersistStep, &Path) -> Result<(), NavigationProblem>,
    ) -> Result<(), NavigationProblem> {
        self.file.take();
        if self.consumed {
            self.bound.take();
            return Ok(());
        }
        let (_, parent_identity) = open_evidence(
            self.path.parent().ok_or(NavigationProblem::WriteFailed)?,
            true,
        )?;
        if parent_identity != self.parent_identity {
            return Err(NavigationProblem::Changed);
        }
        let file = match self.bound.take() {
            Some(file) => file,
            None => match self.open_bound() {
                Ok(file) => file,
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                    self.consumed = true;
                    return Ok(());
                }
                Err(_) => return Err(NavigationProblem::WriteFailed),
            },
        };
        if identity(&file)?.0 != self.identity {
            return Err(NavigationProblem::Changed);
        }
        step(PersistStep::ScratchUnlinking, &self.path)?;
        // Deletion is bound to the verified file object, so a replacement at
        // the pathname cannot be unlinked here.
        delete_handle(&file).map_err(|_| NavigationProblem::WriteFailed)?;
        self.consumed = true;
        // Release the delete handle before checking the pathname.
        drop(file);
        verify_unlinked(&self.path)
    }

    #[cfg(not(any(unix, windows)))]
    fn remove(
        &mut self,
        _step: &mut impl FnMut(PersistStep, &Path) -> Result<(), NavigationProblem>,
    ) -> Result<(), NavigationProblem> {
        self.file.take();
        self.bound.take();
        Err(NavigationProblem::WriteFailed)
    }
}

fn persist(
    path: &Path,
    preference: bool,
    original: &Observation,
    lease: &NavigationLease,
) -> Result<Observation, NavigationProblem> {
    persist_with(path, preference, original, lease, |_, _| Ok(()))
}

fn persist_with(
    path: &Path,
    preference: bool,
    original: &Observation,
    lease: &NavigationLease,
    mut step: impl FnMut(PersistStep, &Path) -> Result<(), NavigationProblem>,
) -> Result<Observation, NavigationProblem> {
    lease.validate()?;
    let temporary = lease.directory.join(format!(
        ".navigation_preference-{}.tmp",
        uuid::Uuid::now_v7()
    ));
    let mut scratch = Scratch::create(temporary)?;
    let mut published = false;
    let result = (|| {
        step(PersistStep::ScratchCreated, &scratch.path)?;
        let bytes = serde_json::to_vec(&Document {
            version: 1,
            usage_pinned: preference,
        })
        .map_err(|_| NavigationProblem::WriteFailed)?;
        let file = scratch
            .file
            .as_mut()
            .ok_or(NavigationProblem::WriteFailed)?;
        file.write_all(&bytes)
            .and_then(|_| file.sync_all())
            .map_err(|_| NavigationProblem::WriteFailed)?;
        scratch.file.take();
        step(PersistStep::ScratchSynced, &scratch.path)?;
        // Extra detection for observed nonparticipating changes. The sidecar
        // supplies exclusion only for writers that follow this same protocol.
        if inspect(path, lease)? != *original {
            return Err(NavigationProblem::Changed);
        }
        scratch.bind()?;
        step(PersistStep::DestinationVerified, &scratch.path)?;
        if let Err(failure) = scratch.publish(path, &original.document) {
            published = failure.touched;
            if failure.reversed {
                // The reversal changed the namespace twice. Stay touched until
                // the restored namespace is synced, so a failed barrier is
                // OutcomeUnknown rather than an untouched Changed.
                published = true;
                lease.validate()?;
                lease
                    .parent
                    .sync_all()
                    .map_err(|_| NavigationProblem::OutcomeUnknown)?;
                step(PersistStep::RollbackSynced, &scratch.path)?;
                published = false;
            }
            return Err(failure.problem);
        }
        published = true;
        step(PersistStep::Published, &scratch.path)?;
        step(PersistStep::PublishBarrier, &scratch.path)?;
        scratch.publish_barrier()?;
        // Discard the replaced document (Unix Present) before checking the
        // destination's link count. Moved scratch files need no removal.
        scratch.remove(&mut step)?;
        step(PersistStep::ScratchRemoved, &scratch.path)?;
        lease.validate()?;
        step(PersistStep::DirectoryBarrier, &scratch.path)?;
        #[cfg(unix)]
        lease
            .parent
            .sync_all()
            .map_err(|_| NavigationProblem::OutcomeUnknown)?;
        // A Missing destination is published without write-through, so the new
        // directory entry needs its own barrier before it may be adopted.
        // Present replacement keeps its existing publication semantics.
        #[cfg(windows)]
        if matches!(original.document, ObservedDocument::Missing) {
            flush_directory_entry(&lease.directory, lease.parent_identity)?;
        }
        step(PersistStep::DirectorySynced, &scratch.path)?;
        let receipt = inspect(path, lease)?;
        match &receipt.document {
            ObservedDocument::Present {
                bytes: received,
                identity,
                ..
            } if *received == bytes && *identity == scratch.identity => {}
            _ => return Err(NavigationProblem::OutcomeUnknown),
        }
        step(PersistStep::ReceiptVerified, &scratch.path)?;
        Ok(receipt)
    })();
    let cleanup = scratch.remove(&mut step);
    if cleanup.is_err() {
        tracing::warn!(
            operation = "navigation_preference_update",
            code = "scratch-cleanup-failed"
        );
    }
    match result {
        Ok(receipt) if cleanup.is_ok() => Ok(receipt),
        Err(problem) if !published && cleanup.is_ok() => Err(problem),
        // Publication/cleanup/durability/receipt uncertainty never adopts a new
        // selection or authorizes replay. Only an explicit Read recovers it.
        _ => Err(NavigationProblem::OutcomeUnknown),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn invalid_external_document_retains_last_committed_pin_state() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("navigation_preference.json");
        let store = NavigationStore::new(Some(directory.path().into()));
        let initial = store.read();
        store.update(true, initial.revision);
        fs::write(&path, b"invalid original").unwrap();
        let observed = store.read();
        assert!(observed.usage_pinned);
        assert_eq!(observed.problem, Some(NavigationProblem::InvalidDocument));
        store.update(false, observed.revision);
        assert_eq!(fs::read(&path).unwrap(), b"invalid original");
    }

    #[test]
    fn oversized_document_and_revision_exhaustion_preserve_original_bytes() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("navigation_preference.json");
        let original = vec![b' '; DOCUMENT_LIMIT as usize + 1];
        fs::write(&path, &original).unwrap();
        let store = NavigationStore::new(Some(directory.path().into()));
        let snapshot = store.read();
        assert_eq!(snapshot.problem, Some(NavigationProblem::InvalidDocument));
        store.update(true, snapshot.revision);
        assert_eq!(fs::read(&path).unwrap(), original);
        fs::remove_file(&path).unwrap();
        store.read();
        store.state.lock().unwrap().snapshot.revision = u32::MAX;
        store.update(true, u32::MAX);
        assert!(!path.exists());
        assert!(!store.current().usage_pinned);
    }

    #[test]
    fn hard_linked_document_is_not_replaced() {
        let directory = tempfile::tempdir().unwrap();
        let source = directory.path().join("original.json");
        let original = br#"{"version":1,"usage_pinned":true}"#;
        fs::write(&source, original).unwrap();
        let path = directory.path().join("navigation_preference.json");
        fs::hard_link(&source, &path).unwrap();
        let store = NavigationStore::new(Some(directory.path().into()));
        let snapshot = store.read();
        assert_eq!(snapshot.problem, Some(NavigationProblem::InvalidDocument));
        store.update(false, snapshot.revision);
        assert_eq!(fs::read(&path).unwrap(), original);
        assert_eq!(fs::read(&source).unwrap(), original);
    }

    #[test]
    fn failed_or_uncertain_publication_requires_a_fresh_read() {
        let directory = tempfile::tempdir().unwrap();
        let store = NavigationStore::new(Some(directory.path().into()));
        let initial = store.read();
        let dark = store.update(true, initial.revision);
        let failure = store.update_with(false, dark.revision, |_, _, _, _| {
            Err(NavigationProblem::WriteFailed)
        });
        assert!(failure.usage_pinned);
        assert_eq!(failure.problem, Some(NavigationProblem::WriteFailed));
        assert!(store.update(false, failure.revision).usage_pinned);
        let inspected = store.read();
        let uncertain = store.update_with(
            false,
            inspected.revision,
            |path, usage_pinned, original, lease| {
                persist(path, usage_pinned, original, lease)?;
                Err(NavigationProblem::OutcomeUnknown)
            },
        );
        assert!(uncertain.usage_pinned);
        assert_eq!(uncertain.problem, Some(NavigationProblem::OutcomeUnknown));
        assert!(store.update(true, uncertain.revision).usage_pinned);
        let inspected = store.read();
        assert!(!(inspected.usage_pinned));
        assert_eq!(inspected.problem, None);
        assert_eq!(store.update(false, inspected.revision).problem, None);
    }

    #[test]
    fn unchanged_reads_keep_all_windows_on_the_same_revision() {
        let directory = tempfile::tempdir().unwrap();
        let store = NavigationStore::new(Some(directory.path().into()));
        let initial = store.read();
        assert_eq!(store.read(), initial);
        assert_eq!(store.update(true, initial.revision).problem, None);
    }

    #[cfg(unix)]
    #[test]
    fn linked_storage_is_preserved_without_touching_its_target() {
        let directory = tempfile::tempdir().unwrap();
        let target = directory.path().join("external.json");
        fs::write(&target, br#"{"version":1,"usage_pinned":true}"#).unwrap();
        let original = fs::read(&target).unwrap();
        std::os::unix::fs::symlink(&target, directory.path().join("navigation_preference.json"))
            .unwrap();
        let store = NavigationStore::new(Some(directory.path().into()));
        let snapshot = store.read();
        assert_eq!(snapshot.problem, Some(NavigationProblem::InvalidDocument));
        store.update(false, snapshot.revision);
        assert_eq!(fs::read(target).unwrap(), original);
        assert!(
            fs::symlink_metadata(directory.path().join("navigation_preference.json"))
                .unwrap()
                .is_symlink()
        );
    }

    #[test]
    fn default_restart_and_stale_selection() {
        let directory = tempfile::tempdir().unwrap();
        let store = NavigationStore::new(Some(directory.path().into()));
        let initial = store.read();
        assert!(!(initial.usage_pinned));
        assert_eq!(initial.problem, None);
        let dark = store.update(true, initial.revision);
        assert!(dark.usage_pinned);
        assert_eq!(dark.problem, None);
        assert_eq!(
            store.update(false, initial.revision).problem,
            Some(NavigationProblem::Changed)
        );
        assert!(store.read().usage_pinned);
        let restarted = NavigationStore::new(Some(directory.path().into()));
        let observed = restarted.read();
        assert!(observed.usage_pinned);
        assert!(!(restarted.update(false, observed.revision).usage_pinned));
        assert!(!(restarted.read().usage_pinned));
    }

    #[test]
    fn invalid_and_newer_documents_remain_intact() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("navigation_preference.json");
        for bytes in [
            b"broken".as_slice(),
            br#"{"version":2,"usage_pinned":true}"#,
            br#"{"version":1,"usage_pinned":"yes"}"#,
            br#"{"version":1,"usage_pinned":true,"extra":true}"#,
            br#"{"version":1}"#,
        ] {
            fs::write(&path, bytes).unwrap();
            let store = NavigationStore::new(Some(directory.path().into()));
            let observed = store.read();
            assert!(!(observed.usage_pinned));
            assert!(observed.problem.is_some());
            assert!(!(store.update(false, observed.revision).usage_pinned));
            assert_eq!(fs::read(&path).unwrap(), bytes);
        }
    }

    #[test]
    fn external_change_requires_inspection_before_retry() {
        let directory = tempfile::tempdir().unwrap();
        let store = NavigationStore::new(Some(directory.path().into()));
        let initial = store.read();
        fs::write(
            directory.path().join("navigation_preference.json"),
            br#"{"version":1,"usage_pinned":true}"#,
        )
        .unwrap();
        let rejected = store.update(false, initial.revision);
        assert!(!(rejected.usage_pinned));
        assert_eq!(rejected.problem, Some(NavigationProblem::Changed));
        let observed = store.read();
        assert!(observed.usage_pinned);
        assert_eq!(store.update(false, observed.revision).problem, None);
    }

    #[test]
    fn write_failure_keeps_committed_selection_and_blocks_retry() {
        let directory = tempfile::tempdir().unwrap();
        let store = NavigationStore::new(Some(directory.path().into()));
        let initial = store.read();
        let committed = store.update(true, initial.revision);
        fs::remove_file(directory.path().join("navigation_preference.json")).unwrap();
        let moved = directory.path().with_extension("retained");
        fs::rename(directory.path(), &moved).unwrap();
        fs::write(directory.path(), b"not a directory").unwrap();
        let failed = store.update(false, committed.revision);
        assert!(failed.usage_pinned);
        assert!(failed.problem.is_some());
        assert!(store.update(false, failed.revision).usage_pinned);
        fs::remove_file(directory.path()).unwrap();
        fs::rename(moved, directory.path()).unwrap();
    }
}
