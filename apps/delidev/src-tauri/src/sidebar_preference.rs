// SPDX-License-Identifier: Apache-2.0
use std::{
    fs::{self, File, Metadata, OpenOptions},
    io::{Read, Write},
    path::{Path, PathBuf},
    sync::Mutex,
};

use serde::{Deserialize, Serialize};

const DOCUMENT_LIMIT: u64 = 4096;
const LEASE_NAME: &str = "sidebar-preference.lock";

#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum SidebarPreference {
    #[default]
    Expanded,
    Collapsed,
}

#[derive(Clone, Copy, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum SidebarProblem {
    Unavailable,
    ReadFailed,
    InvalidDocument,
    UnsupportedVersion,
    WriteFailed,
    OutcomeUnknown,
    Changed,
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct SidebarSnapshot {
    pub revision: u32,
    pub sidebar_preference: SidebarPreference,
    pub problem: Option<SidebarProblem>,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Document {
    version: u32,
    sidebar_preference: SidebarPreference,
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
        preference: SidebarPreference,
    },
}

impl Observation {
    fn preference(&self) -> SidebarPreference {
        match &self.document {
            ObservedDocument::Missing => SidebarPreference::Expanded,
            ObservedDocument::Present { preference, .. } => *preference,
        }
    }
}

struct State {
    snapshot: SidebarSnapshot,
    observation: Option<Observation>,
}

/// One process-wide device preference, independent of every server connection.
/// The state mutex orders process revisions. A separate persistent sidecar
/// coordinates participating processes from inspection through publication.
pub struct SidebarStore {
    path: Option<PathBuf>,
    state: Mutex<State>,
    presentation: Mutex<()>,
}

impl SidebarStore {
    pub fn new(config_dir: Option<PathBuf>) -> Self {
        Self {
            path: config_dir.map(|directory| directory.join("sidebar_preference.json")),
            presentation: Mutex::new(()),
            state: Mutex::new(State {
                snapshot: SidebarSnapshot {
                    revision: 0,
                    sidebar_preference: SidebarPreference::Expanded,
                    problem: Some(SidebarProblem::Unavailable),
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
    pub fn current(&self) -> SidebarSnapshot {
        self.state
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .snapshot
            .clone()
    }

    pub fn read(&self) -> SidebarSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        let previous = state.snapshot.clone();
        let result = self
            .path
            .as_deref()
            .ok_or(SidebarProblem::Unavailable)
            .and_then(|path| {
                let lease = SidebarLease::acquire(path, SidebarProblem::ReadFailed)?;
                inspect(path, &lease)
            });
        match result {
            Ok(observation) => {
                state.snapshot.sidebar_preference = observation.preference();
                state.snapshot.problem = None;
                // A Read adopts exact evidence even when the enum is unchanged.
                state.observation = Some(observation);
            }
            Err(problem) => {
                // Invalid external storage cannot replace the committed layout.
                state.snapshot.problem = Some(problem);
                tracing::warn!(operation = "sidebar_preference_read", ?problem);
            }
        }
        if state.snapshot.revision == 0
            || state.snapshot.sidebar_preference != previous.sidebar_preference
            || state.snapshot.problem != previous.problem
        {
            advance(&mut state.snapshot);
        }
        state.snapshot.clone()
    }

    pub fn update(
        &self,
        sidebar_preference: SidebarPreference,
        expected_revision: u32,
    ) -> SidebarSnapshot {
        self.update_with(sidebar_preference, expected_revision, persist)
    }

    fn update_with(
        &self,
        sidebar_preference: SidebarPreference,
        expected_revision: u32,
        write: impl FnOnce(
            &Path,
            SidebarPreference,
            &Observation,
            &SidebarLease,
        ) -> Result<Observation, SidebarProblem>,
    ) -> SidebarSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        if state.snapshot.revision != expected_revision {
            let mut snapshot = state.snapshot.clone();
            snapshot.problem = Some(SidebarProblem::Changed);
            return snapshot;
        }
        if state.snapshot.problem.is_some() || state.snapshot.revision == u32::MAX {
            return state.snapshot.clone();
        }
        let result = self
            .path
            .as_deref()
            .ok_or(SidebarProblem::Unavailable)
            .and_then(|path| {
                let lease = SidebarLease::acquire(path, SidebarProblem::WriteFailed)?;
                let original = state.observation.as_ref().ok_or(SidebarProblem::Changed)?;
                // Invalid/newer/linked evidence retains its own classification.
                // Equal enums cannot authorize replacing different bytes/files.
                if inspect(path, &lease)? != *original {
                    return Err(SidebarProblem::Changed);
                }
                write(path, sidebar_preference, original, &lease)
            });
        match result {
            Ok(observation) => {
                state.snapshot.sidebar_preference = observation.preference();
                state.observation = Some(observation);
                tracing::info!(operation = "sidebar_preference_update", state = "committed");
            }
            Err(problem) => {
                state.snapshot.problem = Some(problem);
                tracing::warn!(operation = "sidebar_preference_update", ?problem);
            }
        }
        advance(&mut state.snapshot);
        state.snapshot.clone()
    }
}

fn advance(state: &mut SidebarSnapshot) {
    // Never wrap into an older revision. Exhaustion stays read-only until
    // restart.
    if let Some(revision) = state.revision.checked_add(1) {
        state.revision = revision;
    } else {
        state.problem = Some(SidebarProblem::Unavailable);
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

fn identity(file: &File) -> Result<(FileIdentity, u64), SidebarProblem> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        let metadata = file.metadata().map_err(|_| SidebarProblem::ReadFailed)?;
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
            return Err(SidebarProblem::ReadFailed);
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
        Err(SidebarProblem::ReadFailed)
    }
}

fn validate_metadata(metadata: &Metadata, directory: bool) -> Result<(), SidebarProblem> {
    if if directory {
        !metadata.is_dir()
    } else {
        !metadata.is_file()
    } {
        return Err(SidebarProblem::InvalidDocument);
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        if metadata.uid() != unsafe { libc::geteuid() } || (!directory && metadata.nlink() != 1) {
            return Err(SidebarProblem::InvalidDocument);
        }
    }
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        if metadata.file_attributes() & 0x400 != 0 {
            return Err(SidebarProblem::InvalidDocument);
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

fn open_evidence(path: &Path, directory: bool) -> Result<(File, FileIdentity), SidebarProblem> {
    let metadata = fs::symlink_metadata(path).map_err(|_| SidebarProblem::ReadFailed)?;
    validate_metadata(&metadata, directory)?;
    let file = read_options(directory)
        .open(path)
        .map_err(|_| SidebarProblem::ReadFailed)?;
    validate_metadata(
        &file.metadata().map_err(|_| SidebarProblem::ReadFailed)?,
        directory,
    )?;
    let (opened_identity, links) = identity(&file)?;
    if !directory && links != 1 {
        return Err(SidebarProblem::InvalidDocument);
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        if metadata.dev() != opened_identity.device || metadata.ino() != opened_identity.inode {
            return Err(SidebarProblem::Changed);
        }
    }
    Ok((file, opened_identity))
}

struct SidebarLease {
    // Never unlink or replace the sidecar: its persistent identity owns exclusion.
    file: File,
    lock_identity: FileIdentity,
    parent: File,
    parent_identity: FileIdentity,
    directory: PathBuf,
}

impl SidebarLease {
    fn acquire(path: &Path, problem: SidebarProblem) -> Result<Self, SidebarProblem> {
        let result = (|| {
            let directory = path.parent().ok_or(SidebarProblem::ReadFailed)?;
            fs::create_dir_all(directory).map_err(|_| SidebarProblem::ReadFailed)?;
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
                .map_err(|_| SidebarProblem::ReadFailed)?;
            let metadata = file.metadata().map_err(|_| SidebarProblem::ReadFailed)?;
            validate_metadata(&metadata, false)?;
            #[cfg(unix)]
            {
                use std::os::unix::fs::MetadataExt;
                if metadata.mode() & 0o077 != 0 {
                    return Err(SidebarProblem::ReadFailed);
                }
            }
            let (lock_identity, links) = identity(&file)?;
            if links != 1 {
                return Err(SidebarProblem::ReadFailed);
            }
            file.try_lock().map_err(|error| {
                let code = match error {
                    fs::TryLockError::WouldBlock => "lease-busy",
                    fs::TryLockError::Error(_) => "lease-unavailable",
                };
                tracing::warn!(operation = "sidebar_preference_lease", code);
                SidebarProblem::ReadFailed
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

    fn validate(&self) -> Result<(), SidebarProblem> {
        let (_, parent_identity) = open_evidence(&self.directory, true)?;
        let (_, lock_identity) = open_evidence(&self.directory.join(LEASE_NAME), false)?;
        if parent_identity != self.parent_identity
            || identity(&self.parent)?.0 != self.parent_identity
            || lock_identity != self.lock_identity
            || identity(&self.file)?.1 != 1
        {
            return Err(SidebarProblem::Changed);
        }
        Ok(())
    }
}

impl Drop for SidebarLease {
    fn drop(&mut self) {
        // Explicit unlock also releases a lease inherited briefly across fork.
        if self.file.unlock().is_err() {
            tracing::warn!(
                operation = "sidebar_preference_lease",
                code = "lease-release-failed"
            );
        }
    }
}

fn inspect(path: &Path, lease: &SidebarLease) -> Result<Observation, SidebarProblem> {
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
        Err(_) => return Err(SidebarProblem::ReadFailed),
    };
    validate_metadata(&metadata, false)?;
    if metadata.len() > DOCUMENT_LIMIT {
        return Err(SidebarProblem::InvalidDocument);
    }
    let (file, file_identity) = open_evidence(path, false)?;
    let before = file.metadata().map_err(|_| SidebarProblem::ReadFailed)?;
    if before.len() > DOCUMENT_LIMIT {
        return Err(SidebarProblem::InvalidDocument);
    }
    let mut bytes = Vec::new();
    (&file)
        .take(DOCUMENT_LIMIT + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| SidebarProblem::ReadFailed)?;
    let after = file.metadata().map_err(|_| SidebarProblem::ReadFailed)?;
    validate_metadata(&after, false)?;
    if bytes.len() as u64 > DOCUMENT_LIMIT {
        return Err(SidebarProblem::InvalidDocument);
    }
    if before.len() != bytes.len() as u64 || after.len() != before.len() {
        return Err(SidebarProblem::Changed);
    }
    let (_, current_identity) = open_evidence(path, false)?;
    if current_identity != file_identity || identity(&file)? != (file_identity, 1) {
        return Err(SidebarProblem::Changed);
    }
    lease.validate()?;
    let document: Document =
        serde_json::from_slice(&bytes).map_err(|_| SidebarProblem::InvalidDocument)?;
    if document.version != 1 {
        return Err(SidebarProblem::UnsupportedVersion);
    }
    Ok(Observation {
        parent: lease.parent_identity,
        document: ObservedDocument::Present {
            bytes,
            identity: file_identity,
            version: document.version,
            preference: document.sidebar_preference,
        },
    })
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum PersistStep {
    ScratchCreated,
    ScratchSynced,
    DestinationVerified,
    Published,
    /// Persistence barrier for the published file object (Windows flushes the
    /// verified handle; Unix relies on the later parent sync). Failure is
    /// OutcomeUnknown.
    PublishBarrier,
    ScratchUnlinking,
    ScratchRemoved,
    DirectorySynced,
    ReceiptVerified,
}

/// Outcome of a refused or rolled back publication attempt.
struct PublishFailure {
    problem: SidebarProblem,
    /// True when the destination pathname may differ from its observed state.
    touched: bool,
}

impl PublishFailure {
    fn untouched(problem: SidebarProblem) -> Self {
        Self {
            problem,
            touched: false,
        }
    }

    #[cfg(unix)]
    fn touched(problem: SidebarProblem) -> Self {
        Self {
            problem,
            touched: true,
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

/// Persistence barrier for a file object that was just published by a handle
/// rename. `SetFileInformationByHandle` has no write-through option, so the
/// verified handle is flushed (`FlushFileBuffers` on Windows) before the save
/// may be reported as committed. The handle is proven to still be the created
/// file object first; every failure is OutcomeUnknown so the new selection is
/// never adopted. This does not establish power-loss durability, which is
/// untested.
#[cfg(any(windows, test))]
fn flush_published(file: &File, expected: FileIdentity) -> Result<(), SidebarProblem> {
    match identity(file) {
        Ok((found, _)) if found == expected => {}
        _ => {
            tracing::warn!(
                operation = "sidebar_preference_publish",
                code = "publish-barrier-identity-mismatch"
            );
            return Err(SidebarProblem::OutcomeUnknown);
        }
    }
    file.sync_all().map_err(|error| {
        tracing::warn!(
            operation = "sidebar_preference_publish",
            code = "publish-barrier-failed",
            kind = ?error.kind()
        );
        SidebarProblem::OutcomeUnknown
    })
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
    fn create(path: PathBuf) -> Result<Self, SidebarProblem> {
        let (_, parent_identity) =
            open_evidence(path.parent().ok_or(SidebarProblem::WriteFailed)?, true)?;
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
            .map_err(|_| SidebarProblem::WriteFailed)?;
        // A create failure grants no ownership, even on a scratch-name
        // collision.
        let (identity, _) = identity(&file).map_err(|_| SidebarProblem::WriteFailed)?;
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
    fn bind(&mut self) -> Result<(), SidebarProblem> {
        let source = self.open_bound().map_err(|_| SidebarProblem::WriteFailed)?;
        validate_metadata(
            &source.metadata().map_err(|_| SidebarProblem::WriteFailed)?,
            false,
        )?;
        if identity(&source)? != (self.identity, 1) {
            return Err(SidebarProblem::Changed);
        }
        self.bound = Some(source);
        Ok(())
    }

    /// Barrier after a successful publication, before cleanup releases the
    /// bound handle. Windows flushes the renamed file object for both Missing
    /// and Present destinations; Unix syncs the parent directory afterwards.
    fn publish_barrier(&self) -> Result<(), SidebarProblem> {
        #[cfg(windows)]
        {
            let file = self.bound.as_ref().ok_or(SidebarProblem::OutcomeUnknown)?;
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
        if let Err(error) = rename_checked(&self.path, target, mode) {
            tracing::warn!(
                operation = "sidebar_preference_publish",
                code = "rename-refused",
                kind = ?error.kind()
            );
            return Err(PublishFailure::untouched(match error.kind() {
                std::io::ErrorKind::AlreadyExists | std::io::ErrorKind::NotFound => {
                    SidebarProblem::Changed
                }
                _ => SidebarProblem::WriteFailed,
            }));
        }
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
                    self.residual =
                        replaced.ok_or(PublishFailure::touched(SidebarProblem::OutcomeUnknown))?;
                }
            }
            return Ok(());
        }
        tracing::warn!(
            operation = "sidebar_preference_publish",
            code = "unexpected-entry-published",
            rolling_back = true
        );
        let rollback = match mode {
            RenameMode::NoReplace => rename_checked(target, &self.path, RenameMode::NoReplace),
            RenameMode::Exchange => rename_checked(&self.path, target, RenameMode::Exchange),
        };
        let restored = rollback.is_ok()
            && entry_identity(&self.path).ok() == Some(published)
            && match mode {
                RenameMode::NoReplace => entry_identity(target).ok() == Some(None),
                RenameMode::Exchange => entry_identity(target).ok() == displaced,
            };
        if restored {
            // The foreign entry stays at the scratch pathname as evidence.
            Err(PublishFailure::untouched(SidebarProblem::Changed))
        } else {
            tracing::error!(
                operation = "sidebar_preference_publish",
                code = "rollback-unverified"
            );
            Err(PublishFailure::touched(SidebarProblem::OutcomeUnknown))
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
            .ok_or(PublishFailure::untouched(SidebarProblem::WriteFailed))?;
        let replace = matches!(document, ObservedDocument::Present { .. });
        match rename_handle(file, target, replace) {
            Ok(()) => {
                self.consumed = true;
                Ok(())
            }
            Err(error) => {
                tracing::warn!(
                    operation = "sidebar_preference_publish",
                    code = "rename-refused",
                    kind = ?error.kind()
                );
                Err(PublishFailure::untouched(
                    if error.kind() == std::io::ErrorKind::AlreadyExists {
                        SidebarProblem::Changed
                    } else {
                        SidebarProblem::WriteFailed
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
        Err(PublishFailure::untouched(SidebarProblem::WriteFailed))
    }

    /// Removes only the entry this save owns. Any other entry at the scratch
    /// pathname is retained as evidence and reported as `Changed`.
    #[cfg(unix)]
    fn remove(
        &mut self,
        step: &mut impl FnMut(PersistStep, &Path) -> Result<(), SidebarProblem>,
    ) -> Result<(), SidebarProblem> {
        self.file.take();
        self.bound.take();
        if self.consumed {
            return Ok(());
        }
        let directory = self.path.parent().ok_or(SidebarProblem::WriteFailed)?;
        let (_, parent_identity) = open_evidence(directory, true)?;
        if parent_identity != self.parent_identity {
            return Err(SidebarProblem::Changed);
        }
        match entry_identity(&self.path) {
            Ok(None) => {
                self.consumed = true;
                return Ok(());
            }
            Ok(Some(found)) if found == self.residual => {}
            Ok(Some(_)) => return Err(SidebarProblem::Changed),
            Err(_) => return Err(SidebarProblem::WriteFailed),
        }
        step(PersistStep::ScratchUnlinking, &self.path)?;
        // Unix cannot unlink by identity. Atomically claim whatever the
        // pathname names now under a private name, verify that claim, and only
        // then unlink it. A foreign entry is moved back untouched.
        let claim = directory.join(format!(
            ".sidebar_preference-{}.claim",
            uuid::Uuid::now_v7()
        ));
        match rename_checked(&self.path, &claim, RenameMode::NoReplace) {
            Ok(()) => {}
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                self.consumed = true;
                return Ok(());
            }
            Err(_) => return Err(SidebarProblem::WriteFailed),
        }
        if entry_identity(&claim).ok() != Some(Some(self.residual)) {
            let restored = rename_checked(&claim, &self.path, RenameMode::NoReplace).is_ok();
            tracing::warn!(
                operation = "sidebar_preference_update",
                code = "scratch-cleanup-foreign-entry",
                restored
            );
            return Err(SidebarProblem::Changed);
        }
        fs::remove_file(&claim).map_err(|_| SidebarProblem::WriteFailed)?;
        self.consumed = true;
        Ok(())
    }

    #[cfg(windows)]
    fn remove(
        &mut self,
        step: &mut impl FnMut(PersistStep, &Path) -> Result<(), SidebarProblem>,
    ) -> Result<(), SidebarProblem> {
        self.file.take();
        if self.consumed {
            self.bound.take();
            return Ok(());
        }
        let (_, parent_identity) =
            open_evidence(self.path.parent().ok_or(SidebarProblem::WriteFailed)?, true)?;
        if parent_identity != self.parent_identity {
            return Err(SidebarProblem::Changed);
        }
        let file = match self.bound.take() {
            Some(file) => file,
            None => match self.open_bound() {
                Ok(file) => file,
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                    self.consumed = true;
                    return Ok(());
                }
                Err(_) => return Err(SidebarProblem::WriteFailed),
            },
        };
        if identity(&file)?.0 != self.identity {
            return Err(SidebarProblem::Changed);
        }
        step(PersistStep::ScratchUnlinking, &self.path)?;
        // Deletion is bound to the verified file object, so a replacement at
        // the pathname cannot be unlinked here.
        delete_handle(&file).map_err(|_| SidebarProblem::WriteFailed)?;
        self.consumed = true;
        match fs::symlink_metadata(&self.path) {
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
            Ok(_) => Err(SidebarProblem::Changed),
            Err(_) => Err(SidebarProblem::WriteFailed),
        }
    }

    #[cfg(not(any(unix, windows)))]
    fn remove(
        &mut self,
        _step: &mut impl FnMut(PersistStep, &Path) -> Result<(), SidebarProblem>,
    ) -> Result<(), SidebarProblem> {
        self.file.take();
        self.bound.take();
        Err(SidebarProblem::WriteFailed)
    }
}

fn persist(
    path: &Path,
    preference: SidebarPreference,
    original: &Observation,
    lease: &SidebarLease,
) -> Result<Observation, SidebarProblem> {
    persist_with(path, preference, original, lease, |_, _| Ok(()))
}

fn persist_with(
    path: &Path,
    preference: SidebarPreference,
    original: &Observation,
    lease: &SidebarLease,
    mut step: impl FnMut(PersistStep, &Path) -> Result<(), SidebarProblem>,
) -> Result<Observation, SidebarProblem> {
    lease.validate()?;
    let temporary = lease
        .directory
        .join(format!(".sidebar_preference-{}.tmp", uuid::Uuid::now_v7()));
    let mut scratch = Scratch::create(temporary)?;
    let mut published = false;
    let result = (|| {
        step(PersistStep::ScratchCreated, &scratch.path)?;
        let bytes = serde_json::to_vec(&Document {
            version: 1,
            sidebar_preference: preference,
        })
        .map_err(|_| SidebarProblem::WriteFailed)?;
        let file = scratch.file.as_mut().ok_or(SidebarProblem::WriteFailed)?;
        file.write_all(&bytes)
            .and_then(|_| file.sync_all())
            .map_err(|_| SidebarProblem::WriteFailed)?;
        scratch.file.take();
        step(PersistStep::ScratchSynced, &scratch.path)?;
        // Extra detection for observed nonparticipating changes. The sidecar
        // supplies exclusion only for writers that follow this same protocol.
        if inspect(path, lease)? != *original {
            return Err(SidebarProblem::Changed);
        }
        scratch.bind()?;
        step(PersistStep::DestinationVerified, &scratch.path)?;
        if let Err(failure) = scratch.publish(path, &original.document) {
            published = failure.touched;
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
        #[cfg(unix)]
        lease
            .parent
            .sync_all()
            .map_err(|_| SidebarProblem::OutcomeUnknown)?;
        step(PersistStep::DirectorySynced, &scratch.path)?;
        let receipt = inspect(path, lease)?;
        match &receipt.document {
            ObservedDocument::Present {
                bytes: received,
                identity,
                ..
            } if *received == bytes && *identity == scratch.identity => {}
            _ => return Err(SidebarProblem::OutcomeUnknown),
        }
        step(PersistStep::ReceiptVerified, &scratch.path)?;
        Ok(receipt)
    })();
    let cleanup = scratch.remove(&mut step);
    if cleanup.is_err() {
        tracing::warn!(
            operation = "sidebar_preference_update",
            code = "scratch-cleanup-failed"
        );
    }
    match result {
        Ok(receipt) if cleanup.is_ok() => Ok(receipt),
        Err(problem) if !published && cleanup.is_ok() => Err(problem),
        // Publication/cleanup/durability/receipt uncertainty never adopts a new
        // selection or authorizes replay. Only an explicit Read recovers it.
        _ => Err(SidebarProblem::OutcomeUnknown),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn invalid_external_document_retains_last_committed_layout() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("sidebar_preference.json");
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        store.update(SidebarPreference::Collapsed, initial.revision);
        fs::write(&path, b"invalid original").unwrap();
        let observed = store.read();
        assert_eq!(observed.sidebar_preference, SidebarPreference::Collapsed);
        assert_eq!(observed.problem, Some(SidebarProblem::InvalidDocument));
        store.update(SidebarPreference::Expanded, observed.revision);
        assert_eq!(fs::read(&path).unwrap(), b"invalid original");
    }

    #[test]
    fn oversized_document_and_revision_exhaustion_preserve_original_bytes() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("sidebar_preference.json");
        let original = vec![b' '; DOCUMENT_LIMIT as usize + 1];
        fs::write(&path, &original).unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let snapshot = store.read();
        assert_eq!(snapshot.problem, Some(SidebarProblem::InvalidDocument));
        store.update(SidebarPreference::Collapsed, snapshot.revision);
        assert_eq!(fs::read(&path).unwrap(), original);
        fs::remove_file(&path).unwrap();
        store.read();
        store.state.lock().unwrap().snapshot.revision = u32::MAX;
        store.update(SidebarPreference::Collapsed, u32::MAX);
        assert!(!path.exists());
        assert_eq!(
            store.current().sidebar_preference,
            SidebarPreference::Expanded
        );
    }

    #[test]
    fn hard_linked_document_is_not_replaced() {
        let directory = tempfile::tempdir().unwrap();
        let source = directory.path().join("original.json");
        let original = br#"{"version":1,"sidebar_preference":"collapsed"}"#;
        fs::write(&source, original).unwrap();
        let path = directory.path().join("sidebar_preference.json");
        fs::hard_link(&source, &path).unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let snapshot = store.read();
        assert_eq!(snapshot.problem, Some(SidebarProblem::InvalidDocument));
        store.update(SidebarPreference::Expanded, snapshot.revision);
        assert_eq!(fs::read(&path).unwrap(), original);
        assert_eq!(fs::read(&source).unwrap(), original);
    }

    #[test]
    fn failed_or_uncertain_publication_requires_a_fresh_read() {
        let directory = tempfile::tempdir().unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        let dark = store.update(SidebarPreference::Collapsed, initial.revision);
        let failure =
            store.update_with(SidebarPreference::Expanded, dark.revision, |_, _, _, _| {
                Err(SidebarProblem::WriteFailed)
            });
        assert_eq!(failure.sidebar_preference, SidebarPreference::Collapsed);
        assert_eq!(failure.problem, Some(SidebarProblem::WriteFailed));
        assert_eq!(
            store
                .update(SidebarPreference::Expanded, failure.revision)
                .sidebar_preference,
            SidebarPreference::Collapsed
        );
        let inspected = store.read();
        let uncertain = store.update_with(
            SidebarPreference::Expanded,
            inspected.revision,
            |path, sidebar_preference, original, lease| {
                persist(path, sidebar_preference, original, lease)?;
                Err(SidebarProblem::OutcomeUnknown)
            },
        );
        assert_eq!(uncertain.sidebar_preference, SidebarPreference::Collapsed);
        assert_eq!(uncertain.problem, Some(SidebarProblem::OutcomeUnknown));
        assert_eq!(
            store
                .update(SidebarPreference::Collapsed, uncertain.revision)
                .sidebar_preference,
            SidebarPreference::Collapsed
        );
        let inspected = store.read();
        assert_eq!(inspected.sidebar_preference, SidebarPreference::Expanded);
        assert_eq!(inspected.problem, None);
        assert_eq!(
            store
                .update(SidebarPreference::Expanded, inspected.revision)
                .problem,
            None
        );
    }

    #[test]
    fn unchanged_reads_keep_all_windows_on_the_same_revision() {
        let directory = tempfile::tempdir().unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        assert_eq!(store.read(), initial);
        assert_eq!(
            store
                .update(SidebarPreference::Collapsed, initial.revision)
                .problem,
            None
        );
    }

    #[cfg(unix)]
    #[test]
    fn linked_storage_is_preserved_without_touching_its_target() {
        let directory = tempfile::tempdir().unwrap();
        let target = directory.path().join("external.json");
        fs::write(
            &target,
            br#"{"version":1,"sidebar_preference":"collapsed"}"#,
        )
        .unwrap();
        let original = fs::read(&target).unwrap();
        std::os::unix::fs::symlink(&target, directory.path().join("sidebar_preference.json"))
            .unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let snapshot = store.read();
        assert_eq!(snapshot.problem, Some(SidebarProblem::InvalidDocument));
        store.update(SidebarPreference::Expanded, snapshot.revision);
        assert_eq!(fs::read(target).unwrap(), original);
        assert!(
            fs::symlink_metadata(directory.path().join("sidebar_preference.json"))
                .unwrap()
                .is_symlink()
        );
    }

    #[test]
    fn default_restart_and_stale_selection() {
        let directory = tempfile::tempdir().unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        assert_eq!(initial.sidebar_preference, SidebarPreference::Expanded);
        assert_eq!(initial.problem, None);
        let dark = store.update(SidebarPreference::Collapsed, initial.revision);
        assert_eq!(dark.sidebar_preference, SidebarPreference::Collapsed);
        assert_eq!(dark.problem, None);
        assert_eq!(
            store
                .update(SidebarPreference::Expanded, initial.revision)
                .problem,
            Some(SidebarProblem::Changed)
        );
        assert_eq!(
            store.read().sidebar_preference,
            SidebarPreference::Collapsed
        );
        let restarted = SidebarStore::new(Some(directory.path().into()));
        let observed = restarted.read();
        assert_eq!(observed.sidebar_preference, SidebarPreference::Collapsed);
        assert_eq!(
            restarted
                .update(SidebarPreference::Expanded, observed.revision)
                .sidebar_preference,
            SidebarPreference::Expanded
        );
        assert_eq!(
            restarted.read().sidebar_preference,
            SidebarPreference::Expanded
        );
    }

    #[test]
    fn invalid_and_newer_documents_remain_intact() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("sidebar_preference.json");
        for bytes in [
            b"broken".as_slice(),
            br#"{"version":2,"sidebar_preference":"collapsed"}"#,
            br#"{"version":1,"sidebar_preference":"other"}"#,
            br#"{"version":1,"sidebar_preference":"collapsed","extra":true}"#,
        ] {
            fs::write(&path, bytes).unwrap();
            let store = SidebarStore::new(Some(directory.path().into()));
            let observed = store.read();
            assert_eq!(observed.sidebar_preference, SidebarPreference::Expanded);
            assert!(observed.problem.is_some());
            assert_eq!(
                store
                    .update(SidebarPreference::Expanded, observed.revision)
                    .sidebar_preference,
                SidebarPreference::Expanded
            );
            assert_eq!(fs::read(&path).unwrap(), bytes);
        }
    }

    #[test]
    fn external_change_requires_inspection_before_retry() {
        let directory = tempfile::tempdir().unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        fs::write(
            directory.path().join("sidebar_preference.json"),
            br#"{"version":1,"sidebar_preference":"collapsed"}"#,
        )
        .unwrap();
        let rejected = store.update(SidebarPreference::Expanded, initial.revision);
        assert_eq!(rejected.sidebar_preference, SidebarPreference::Expanded);
        assert_eq!(rejected.problem, Some(SidebarProblem::Changed));
        let observed = store.read();
        assert_eq!(observed.sidebar_preference, SidebarPreference::Collapsed);
        assert_eq!(
            store
                .update(SidebarPreference::Expanded, observed.revision)
                .problem,
            None
        );
    }

    #[test]
    fn write_failure_keeps_committed_selection_and_blocks_retry() {
        let directory = tempfile::tempdir().unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        let committed = store.update(SidebarPreference::Collapsed, initial.revision);
        fs::remove_file(directory.path().join("sidebar_preference.json")).unwrap();
        let moved = directory.path().with_extension("retained");
        fs::rename(directory.path(), &moved).unwrap();
        fs::write(directory.path(), b"not a directory").unwrap();
        let failed = store.update(SidebarPreference::Expanded, committed.revision);
        assert_eq!(failed.sidebar_preference, SidebarPreference::Collapsed);
        assert!(failed.problem.is_some());
        assert_eq!(
            store
                .update(SidebarPreference::Expanded, failed.revision)
                .sidebar_preference,
            SidebarPreference::Collapsed
        );
        fs::remove_file(directory.path()).unwrap();
        fs::rename(moved, directory.path()).unwrap();
    }

    fn wait_for_marker(path: &Path) {
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(15);
        while !path.exists() {
            assert!(
                std::time::Instant::now() < deadline,
                "sidebar fixture barrier timed out"
            );
            std::thread::sleep(std::time::Duration::from_millis(10));
        }
    }

    struct FixtureProcess(std::process::Child);

    impl Drop for FixtureProcess {
        fn drop(&mut self) {
            let _ = self.0.kill();
            let _ = self.0.wait();
        }
    }

    // Run only by the parent fixture below. This uses a separate OS process and
    // independently opened handles, rather than testing two local mutexes.
    #[test]
    fn cooperating_process_child() {
        let Some(directory) = std::env::var_os("DELIDEV_SIDEBAR_LEASE_FIXTURE_DIR") else {
            return;
        };
        let directory = PathBuf::from(directory);
        let contender = SidebarStore::new(Some(directory.clone()));
        let stale = SidebarStore::new(Some(directory.clone()));
        let first = contender.read();
        let original = stale.read();
        assert_eq!(first.problem, None);
        assert_eq!(original.problem, None);
        fs::write(directory.join("child-ready"), b"ready").unwrap();
        wait_for_marker(&directory.join("attempt-while-locked"));
        let refused = contender.update(SidebarPreference::Collapsed, first.revision);
        assert_eq!(refused.sidebar_preference, first.sidebar_preference);
        assert_eq!(refused.problem, Some(SidebarProblem::WriteFailed));
        assert_eq!(contender.read().problem, Some(SidebarProblem::ReadFailed));
        fs::write(directory.join("child-refused"), b"refused").unwrap();
        wait_for_marker(&directory.join("attempt-after-unlock"));
        let changed = stale.update(SidebarPreference::Collapsed, original.revision);
        assert_eq!(changed.problem, Some(SidebarProblem::Changed));
        assert_eq!(changed.sidebar_preference, original.sidebar_preference);
        let refreshed = stale.read();
        assert_eq!(refreshed.problem, None);
        assert_eq!(
            stale
                .update(SidebarPreference::Collapsed, refreshed.revision)
                .problem,
            None
        );
    }

    #[test]
    fn cooperating_processes_hold_one_persistent_lease_through_publication() {
        for initial_bytes in [
            None,
            Some(br#"{ "version": 1, "sidebar_preference": "expanded" }"#.as_slice()),
        ] {
            let directory = tempfile::tempdir().unwrap();
            let path = directory.path().join("sidebar_preference.json");
            if let Some(bytes) = initial_bytes {
                fs::write(&path, bytes).unwrap();
            }
            let store = SidebarStore::new(Some(directory.path().into()));
            let initial = store.read();
            let mut child = FixtureProcess(
                std::process::Command::new(std::env::current_exe().unwrap())
                    .args([
                        "--exact",
                        "sidebar_preference::tests::cooperating_process_child",
                        "--nocapture",
                    ])
                    .env("DELIDEV_SIDEBAR_LEASE_FIXTURE_DIR", directory.path())
                    .stdout(std::process::Stdio::null())
                    .spawn()
                    .unwrap(),
            );
            wait_for_marker(&directory.path().join("child-ready"));
            let committed = store.update_with(
                SidebarPreference::Expanded,
                initial.revision,
                |path, preference, original, lease| {
                    persist_with(path, preference, original, lease, |step, _| {
                        if step == PersistStep::ScratchSynced {
                            fs::write(directory.path().join("attempt-while-locked"), b"go")
                                .unwrap();
                            wait_for_marker(&directory.path().join("child-refused"));
                            // The child could not inspect or publish while this
                            // parent retained its lease after original
                            // inspection.
                            match initial_bytes {
                                Some(bytes) => assert_eq!(fs::read(path).unwrap(), bytes),
                                None => assert!(!path.exists()),
                            }
                        }
                        Ok(())
                    })
                },
            );
            assert_eq!(committed.problem, None);
            assert!(directory.path().join(LEASE_NAME).is_file());
            fs::write(directory.path().join("attempt-after-unlock"), b"go").unwrap();
            assert!(child.0.wait().unwrap().success());
            assert_eq!(
                store.read().sidebar_preference,
                SidebarPreference::Collapsed
            );
            assert_eq!(store.read().problem, None);
            assert!(directory.path().join(LEASE_NAME).is_file());
        }
    }

    #[test]
    fn same_enum_different_bytes_or_file_requires_explicit_read() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("sidebar_preference.json");
        let canonical = br#"{"version":1,"sidebar_preference":"expanded"}"#;
        let different = br#"{ "version": 1, "sidebar_preference": "expanded" }"#;
        fs::write(&path, canonical).unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        fs::write(&path, different).unwrap();
        let changed = store.update(SidebarPreference::Collapsed, initial.revision);
        assert_eq!(changed.problem, Some(SidebarProblem::Changed));
        assert_eq!(fs::read(&path).unwrap(), different);
        let refreshed = store.read();
        assert_eq!(refreshed.sidebar_preference, SidebarPreference::Expanded);
        assert_eq!(
            store
                .update(SidebarPreference::Collapsed, refreshed.revision)
                .problem,
            None
        );
        let baseline = store.read();
        let bytes = fs::read(&path).unwrap();
        let replacement = directory.path().join("replacement.json");
        fs::write(&replacement, &bytes).unwrap();
        crate::appearance::replace(&replacement, &path).unwrap();
        assert_eq!(
            store
                .update(SidebarPreference::Expanded, baseline.revision)
                .problem,
            Some(SidebarProblem::Changed)
        );
        assert_eq!(fs::read(path).unwrap(), bytes);
    }

    #[test]
    fn read_adopts_new_same_enum_evidence_without_changing_presentation_revision() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("sidebar_preference.json");
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        fs::write(
            &path,
            br#"{ "version": 1, "sidebar_preference": "expanded" }"#,
        )
        .unwrap();
        let read = store.read();
        assert_eq!(read, initial);
        assert_eq!(
            store
                .update(SidebarPreference::Collapsed, read.revision)
                .problem,
            None
        );
    }

    #[test]
    fn observed_replacement_during_scratch_preparation_preserves_external_bytes() {
        for (external, expected) in [
            (
                b"invalid original".as_slice(),
                SidebarProblem::InvalidDocument,
            ),
            (
                br#"{"version":2,"sidebar_preference":"expanded"}"#.as_slice(),
                SidebarProblem::UnsupportedVersion,
            ),
            (
                br#"{"version":1,"sidebar_preference":"collapsed"}"#.as_slice(),
                SidebarProblem::Changed,
            ),
            (
                br#"{ "version": 1, "sidebar_preference": "expanded" }"#.as_slice(),
                SidebarProblem::Changed,
            ),
        ] {
            let directory = tempfile::tempdir().unwrap();
            let path = directory.path().join("sidebar_preference.json");
            fs::write(&path, br#"{"version":1,"sidebar_preference":"expanded"}"#).unwrap();
            let store = SidebarStore::new(Some(directory.path().into()));
            let initial = store.read();
            let rejected = store.update_with(
                SidebarPreference::Collapsed,
                initial.revision,
                |path, preference, original, lease| {
                    persist_with(path, preference, original, lease, |step, _| {
                        if step == PersistStep::ScratchSynced {
                            let replacement = directory.path().join("external.json");
                            fs::write(&replacement, external).unwrap();
                            crate::appearance::replace(&replacement, path).unwrap();
                        }
                        Ok(())
                    })
                },
            );
            assert_eq!(rejected.problem, Some(expected));
            assert_eq!(rejected.sidebar_preference, SidebarPreference::Expanded);
            assert_eq!(fs::read(&path).unwrap(), external);
            assert_eq!(
                store.update(SidebarPreference::Collapsed, rejected.revision),
                rejected
            );
            assert_eq!(fs::read_dir(directory.path()).unwrap().count(), 2);
        }
    }

    #[test]
    fn missing_no_clobber_preserves_creation_after_final_absence_check() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("sidebar_preference.json");
        let external = br#"{"version":2,"sidebar_preference":"expanded"}"#;
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        let rejected = store.update_with(
            SidebarPreference::Collapsed,
            initial.revision,
            |path, preference, original, lease| {
                persist_with(path, preference, original, lease, |step, _| {
                    if step == PersistStep::DestinationVerified {
                        fs::write(path, external).unwrap();
                    }
                    Ok(())
                })
            },
        );
        assert_eq!(rejected.problem, Some(SidebarProblem::Changed));
        assert_eq!(rejected.sidebar_preference, SidebarPreference::Expanded);
        assert_eq!(fs::read(&path).unwrap(), external);
        assert_eq!(fs::read_dir(directory.path()).unwrap().count(), 2);
        assert_eq!(
            store.read().problem,
            Some(SidebarProblem::UnsupportedVersion)
        );
    }

    #[cfg(unix)]
    #[test]
    fn late_linked_and_oversized_documents_keep_original_evidence() {
        for case in 0..3 {
            let directory = tempfile::tempdir().unwrap();
            let path = directory.path().join("sidebar_preference.json");
            let source = directory.path().join("original.json");
            let bytes = br#"{"version":1,"sidebar_preference":"expanded"}"#;
            fs::write(&path, bytes).unwrap();
            fs::write(&source, bytes).unwrap();
            let store = SidebarStore::new(Some(directory.path().into()));
            let initial = store.read();
            let rejected = store.update_with(
                SidebarPreference::Collapsed,
                initial.revision,
                |path, preference, original, lease| {
                    persist_with(path, preference, original, lease, |step, _| {
                        if step == PersistStep::ScratchSynced {
                            fs::remove_file(path).unwrap();
                            match case {
                                0 => fs::hard_link(&source, path).unwrap(),
                                1 => std::os::unix::fs::symlink(&source, path).unwrap(),
                                _ => fs::write(path, vec![b' '; DOCUMENT_LIMIT as usize + 1])
                                    .unwrap(),
                            }
                        }
                        Ok(())
                    })
                },
            );
            assert_eq!(rejected.problem, Some(SidebarProblem::InvalidDocument));
            assert_eq!(rejected.sidebar_preference, SidebarPreference::Expanded);
            assert_eq!(fs::read(source).unwrap(), bytes);
            if case == 1 {
                assert!(fs::symlink_metadata(path).unwrap().is_symlink());
            } else if case == 2 {
                assert_eq!(
                    fs::read(path).unwrap(),
                    vec![b' '; DOCUMENT_LIMIT as usize + 1]
                );
            } else {
                assert_eq!(fs::read(path).unwrap(), bytes);
            }
        }
    }

    #[test]
    fn busy_lease_retains_selection_until_explicit_read() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("sidebar_preference.json");
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        let committed = store.update(SidebarPreference::Collapsed, initial.revision);
        let lease = SidebarLease::acquire(&path, SidebarProblem::WriteFailed).unwrap();
        let refused = store.update(SidebarPreference::Expanded, committed.revision);
        assert_eq!(refused.problem, Some(SidebarProblem::WriteFailed));
        assert_eq!(refused.sidebar_preference, SidebarPreference::Collapsed);
        let blocked_read = store.read();
        assert_eq!(blocked_read.problem, Some(SidebarProblem::ReadFailed));
        assert_eq!(
            blocked_read.sidebar_preference,
            SidebarPreference::Collapsed
        );
        drop(lease);
        assert_eq!(
            store.update(SidebarPreference::Expanded, blocked_read.revision),
            blocked_read
        );
        let read = store.read();
        assert_eq!(read.problem, None);
        assert_eq!(
            store
                .update(SidebarPreference::Expanded, read.revision)
                .problem,
            None
        );
    }

    #[cfg(unix)]
    #[test]
    fn invalid_sidecar_never_acquires_or_changes_document() {
        for case in 0..4 {
            use std::os::unix::fs::PermissionsExt;
            let directory = tempfile::tempdir().unwrap();
            let lock = directory.path().join(LEASE_NAME);
            let other = directory.path().join("external.lock");
            fs::write(&other, b"external lock").unwrap();
            match case {
                0 => fs::hard_link(&other, &lock).unwrap(),
                1 => std::os::unix::fs::symlink(&other, &lock).unwrap(),
                2 => fs::create_dir(&lock).unwrap(),
                _ => {
                    fs::write(&lock, b"public lock").unwrap();
                    fs::set_permissions(&lock, fs::Permissions::from_mode(0o644)).unwrap();
                }
            }
            let store = SidebarStore::new(Some(directory.path().into()));
            let refused = store.read();
            assert_eq!(refused.problem, Some(SidebarProblem::ReadFailed));
            assert_eq!(
                store.update(SidebarPreference::Collapsed, refused.revision),
                refused
            );
            assert_eq!(fs::read(other).unwrap(), b"external lock");
            assert!(!directory.path().join("sidebar_preference.json").exists());
        }
    }

    #[test]
    fn replaced_parent_or_sidecar_refuses_publication() {
        for replace_parent in [false, true] {
            let directory = tempfile::tempdir().unwrap();
            let path = directory.path().join("sidebar_preference.json");
            let store = SidebarStore::new(Some(directory.path().into()));
            let initial = store.read();
            let held = directory.path().with_extension("retained");
            let rejected = store.update_with(
                SidebarPreference::Collapsed,
                initial.revision,
                |path, preference, original, lease| {
                    persist_with(path, preference, original, lease, |step, _| {
                        if step == PersistStep::ScratchSynced {
                            if replace_parent {
                                fs::rename(directory.path(), &held).unwrap();
                                fs::create_dir(directory.path()).unwrap();
                            } else {
                                let lock = directory.path().join(LEASE_NAME);
                                fs::remove_file(&lock).unwrap();
                                let replacement =
                                    SidebarLease::acquire(path, SidebarProblem::WriteFailed)
                                        .unwrap();
                                drop(replacement);
                            }
                        }
                        Ok(())
                    })
                },
            );
            assert!(rejected.problem.is_some());
            assert_eq!(rejected.sidebar_preference, SidebarPreference::Expanded);
            assert!(!path.exists());
            if replace_parent {
                fs::remove_dir(directory.path()).unwrap();
                fs::rename(&held, directory.path()).unwrap();
            }
        }
    }

    #[test]
    fn scratch_collision_and_foreign_replacement_are_never_cleaned() {
        let directory = tempfile::tempdir().unwrap();
        let collision = directory.path().join("collision.tmp");
        fs::write(&collision, b"existing scratch").unwrap();
        assert!(Scratch::create(collision.clone()).is_err());
        assert_eq!(fs::read(collision).unwrap(), b"existing scratch");
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        let mut foreign = None;
        let refused = store.update_with(
            SidebarPreference::Collapsed,
            initial.revision,
            |path, preference, original, lease| {
                persist_with(path, preference, original, lease, |step, scratch| {
                    if step == PersistStep::ScratchSynced {
                        let replaced = scratch.with_extension("foreign");
                        fs::write(&replaced, b"foreign scratch").unwrap();
                        crate::appearance::replace(&replaced, scratch).unwrap();
                        foreign = Some(scratch.to_path_buf());
                    }
                    Ok(())
                })
            },
        );
        assert_eq!(refused.problem, Some(SidebarProblem::OutcomeUnknown));
        assert_eq!(refused.sidebar_preference, SidebarPreference::Expanded);
        assert_eq!(fs::read(foreign.unwrap()).unwrap(), b"foreign scratch");
        assert!(!directory.path().join("sidebar_preference.json").exists());
    }

    #[test]
    fn failures_before_publication_clean_only_owned_scratch() {
        for fail_at in [
            PersistStep::ScratchCreated,
            PersistStep::ScratchSynced,
            PersistStep::DestinationVerified,
        ] {
            let directory = tempfile::tempdir().unwrap();
            let unrelated = directory.path().join("unrelated.tmp");
            fs::write(&unrelated, b"unrelated").unwrap();
            let store = SidebarStore::new(Some(directory.path().into()));
            let initial = store.read();
            let refused = store.update_with(
                SidebarPreference::Collapsed,
                initial.revision,
                |path, preference, original, lease| {
                    persist_with(path, preference, original, lease, |step, _| {
                        if step == fail_at {
                            Err(SidebarProblem::WriteFailed)
                        } else {
                            Ok(())
                        }
                    })
                },
            );
            assert_eq!(refused.problem, Some(SidebarProblem::WriteFailed));
            assert_eq!(refused.sidebar_preference, SidebarPreference::Expanded);
            assert_eq!(fs::read(unrelated).unwrap(), b"unrelated");
            assert_eq!(fs::read_dir(directory.path()).unwrap().count(), 2);
        }
    }

    #[test]
    fn every_post_publication_failure_keeps_old_selection_and_requires_read() {
        for fail_at in [
            PersistStep::Published,
            PersistStep::PublishBarrier,
            PersistStep::ScratchRemoved,
            PersistStep::DirectorySynced,
            PersistStep::ReceiptVerified,
        ] {
            for existing in [false, true] {
                let directory = tempfile::tempdir().unwrap();
                let path = directory.path().join("sidebar_preference.json");
                if existing {
                    fs::write(&path, br#"{"version":1,"sidebar_preference":"expanded"}"#).unwrap();
                }
                let store = SidebarStore::new(Some(directory.path().into()));
                let initial = store.read();
                let uncertain = store.update_with(
                    SidebarPreference::Collapsed,
                    initial.revision,
                    |path, preference, original, lease| {
                        persist_with(path, preference, original, lease, |step, _| {
                            if step == fail_at {
                                Err(SidebarProblem::WriteFailed)
                            } else {
                                Ok(())
                            }
                        })
                    },
                );
                assert_eq!(uncertain.problem, Some(SidebarProblem::OutcomeUnknown));
                assert_eq!(uncertain.sidebar_preference, SidebarPreference::Expanded);
                assert_eq!(
                    store.update(SidebarPreference::Expanded, uncertain.revision),
                    uncertain
                );
                assert_eq!(fs::read_dir(directory.path()).unwrap().count(), 2);
                let read = store.read();
                assert_eq!(read.problem, None);
                assert_eq!(read.sidebar_preference, SidebarPreference::Collapsed);
            }
        }
    }

    #[test]
    fn changed_receipt_is_uncertain() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("sidebar_preference.json");
        let external = b"external receipt";
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        let uncertain = store.update_with(
            SidebarPreference::Collapsed,
            initial.revision,
            |path, preference, original, lease| {
                persist_with(path, preference, original, lease, |step, _| {
                    if step == PersistStep::ScratchRemoved {
                        fs::write(path, external).unwrap();
                    }
                    Ok(())
                })
            },
        );
        assert_eq!(uncertain.problem, Some(SidebarProblem::OutcomeUnknown));
        assert_eq!(uncertain.sidebar_preference, SidebarPreference::Expanded);
        assert_eq!(fs::read(path).unwrap(), external);
        assert_eq!(store.read().problem, Some(SidebarProblem::InvalidDocument));
    }

    #[cfg(unix)]
    fn inode(path: &Path) -> u64 {
        use std::os::unix::fs::MetadataExt;
        fs::symlink_metadata(path).unwrap().ino()
    }

    /// Swaps a distinct regular file onto `target` the way a nonparticipating
    /// writer would, and returns the inode of the newly installed entry.
    #[cfg(unix)]
    fn install_foreign(target: &Path, bytes: &[u8]) -> u64 {
        let staged = target.with_extension("foreign");
        fs::write(&staged, bytes).unwrap();
        let foreign = inode(&staged);
        crate::appearance::replace(&staged, target).unwrap();
        foreign
    }

    #[cfg(unix)]
    const EXISTING: &[u8] = br#"{"version":1,"sidebar_preference":"expanded"}"#;

    #[cfg(unix)]
    #[test]
    fn scratch_replaced_after_identity_check_is_never_published() {
        for existing in [false, true] {
            let directory = tempfile::tempdir().unwrap();
            let path = directory.path().join("sidebar_preference.json");
            if existing {
                fs::write(&path, EXISTING).unwrap();
            }
            let original_inode = existing.then(|| inode(&path));
            let store = SidebarStore::new(Some(directory.path().into()));
            let initial = store.read();
            let mut foreign = None;
            let refused = store.update_with(
                SidebarPreference::Collapsed,
                initial.revision,
                |path, preference, original, lease| {
                    persist_with(path, preference, original, lease, |step, scratch| {
                        if step == PersistStep::DestinationVerified {
                            foreign = Some((
                                scratch.to_path_buf(),
                                install_foreign(scratch, b"foreign scratch"),
                            ));
                        }
                        Ok(())
                    })
                },
            );
            // Foreign evidence is retained, so the save is uncertain, never a
            // committed success.
            assert_eq!(refused.problem, Some(SidebarProblem::OutcomeUnknown));
            assert_eq!(refused.sidebar_preference, SidebarPreference::Expanded);
            let (scratch, foreign_inode) = foreign.unwrap();
            assert_eq!(fs::read(&scratch).unwrap(), b"foreign scratch");
            assert_eq!(inode(&scratch), foreign_inode);
            if existing {
                assert_eq!(fs::read(&path).unwrap(), EXISTING);
                assert_eq!(Some(inode(&path)), original_inode);
                assert_eq!(fs::read_dir(directory.path()).unwrap().count(), 3);
            } else {
                assert!(!path.exists());
                assert_eq!(fs::read_dir(directory.path()).unwrap().count(), 2);
            }
            assert_eq!(
                store.update(SidebarPreference::Collapsed, refused.revision),
                refused
            );
        }
    }

    #[cfg(unix)]
    #[test]
    fn destination_changed_after_identity_check_is_restored_not_replaced() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("sidebar_preference.json");
        fs::write(&path, EXISTING).unwrap();
        let external = br#"{ "version": 1, "sidebar_preference": "expanded" }"#;
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        let mut external_inode = None;
        let rejected = store.update_with(
            SidebarPreference::Collapsed,
            initial.revision,
            |path, preference, original, lease| {
                persist_with(path, preference, original, lease, |step, _| {
                    if step == PersistStep::DestinationVerified {
                        external_inode = Some(install_foreign(path, external));
                    }
                    Ok(())
                })
            },
        );
        assert_eq!(rejected.problem, Some(SidebarProblem::Changed));
        assert_eq!(rejected.sidebar_preference, SidebarPreference::Expanded);
        assert_eq!(fs::read(&path).unwrap(), external);
        assert_eq!(Some(inode(&path)), external_inode);
        // Our owned scratch returned to its pathname and was cleaned.
        assert_eq!(fs::read_dir(directory.path()).unwrap().count(), 2);
    }

    #[cfg(unix)]
    #[test]
    fn cleanup_never_unlinks_a_replacement_inserted_before_unlink() {
        // Before publication, with the destination Missing.
        {
            let directory = tempfile::tempdir().unwrap();
            let store = SidebarStore::new(Some(directory.path().into()));
            let initial = store.read();
            let mut foreign = None;
            let refused = store.update_with(
                SidebarPreference::Collapsed,
                initial.revision,
                |path, preference, original, lease| {
                    persist_with(path, preference, original, lease, |step, scratch| {
                        match step {
                            PersistStep::DestinationVerified => {
                                return Err(SidebarProblem::WriteFailed);
                            }
                            PersistStep::ScratchUnlinking => {
                                foreign = Some((
                                    scratch.to_path_buf(),
                                    install_foreign(scratch, b"foreign cleanup"),
                                ));
                            }
                            _ => {}
                        }
                        Ok(())
                    })
                },
            );
            assert_eq!(refused.problem, Some(SidebarProblem::OutcomeUnknown));
            let (scratch, foreign_inode) = foreign.unwrap();
            assert_eq!(fs::read(&scratch).unwrap(), b"foreign cleanup");
            assert_eq!(inode(&scratch), foreign_inode);
            // Sidecar plus the retained foreign entry; no claim leftovers.
            assert_eq!(fs::read_dir(directory.path()).unwrap().count(), 2);
        }
        // After Present publication, where the replaced document is cleaned.
        {
            let directory = tempfile::tempdir().unwrap();
            let path = directory.path().join("sidebar_preference.json");
            fs::write(&path, EXISTING).unwrap();
            let store = SidebarStore::new(Some(directory.path().into()));
            let initial = store.read();
            let mut foreign = None;
            let uncertain = store.update_with(
                SidebarPreference::Collapsed,
                initial.revision,
                |path, preference, original, lease| {
                    persist_with(path, preference, original, lease, |step, scratch| {
                        if step == PersistStep::ScratchUnlinking {
                            foreign = Some((
                                scratch.to_path_buf(),
                                install_foreign(scratch, b"foreign cleanup"),
                            ));
                        }
                        Ok(())
                    })
                },
            );
            assert_eq!(uncertain.problem, Some(SidebarProblem::OutcomeUnknown));
            assert_eq!(uncertain.sidebar_preference, SidebarPreference::Expanded);
            let (scratch, foreign_inode) = foreign.unwrap();
            assert_eq!(fs::read(&scratch).unwrap(), b"foreign cleanup");
            assert_eq!(inode(&scratch), foreign_inode);
            assert_eq!(fs::read_dir(directory.path()).unwrap().count(), 3);
            // The published preference is recoverable with an explicit Read.
            let recovered = store.read();
            assert_eq!(recovered.problem, None);
            assert_eq!(recovered.sidebar_preference, SidebarPreference::Collapsed);
        }
        // After Missing publication the scratch pathname is no longer owned.
        {
            let directory = tempfile::tempdir().unwrap();
            let store = SidebarStore::new(Some(directory.path().into()));
            let initial = store.read();
            let mut foreign = None;
            let committed = store.update_with(
                SidebarPreference::Collapsed,
                initial.revision,
                |path, preference, original, lease| {
                    persist_with(path, preference, original, lease, |step, scratch| {
                        if step == PersistStep::Published {
                            fs::write(scratch, b"foreign later").unwrap();
                            foreign = Some((scratch.to_path_buf(), inode(scratch)));
                        }
                        Ok(())
                    })
                },
            );
            assert_eq!(committed.problem, None);
            assert_eq!(committed.sidebar_preference, SidebarPreference::Collapsed);
            let (scratch, foreign_inode) = foreign.unwrap();
            assert_eq!(fs::read(&scratch).unwrap(), b"foreign later");
            assert_eq!(inode(&scratch), foreign_inode);
        }
    }

    #[cfg(unix)]
    #[test]
    fn symlink_at_scratch_pathname_is_retained_and_target_untouched() {
        let directory = tempfile::tempdir().unwrap();
        let target = directory.path().join("external.json");
        fs::write(&target, b"external target").unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        let mut link = None;
        let refused = store.update_with(
            SidebarPreference::Collapsed,
            initial.revision,
            |path, preference, original, lease| {
                persist_with(path, preference, original, lease, |step, scratch| {
                    if step == PersistStep::DestinationVerified {
                        fs::remove_file(scratch).unwrap();
                        std::os::unix::fs::symlink(&target, scratch).unwrap();
                        link = Some(scratch.to_path_buf());
                        return Err(SidebarProblem::WriteFailed);
                    }
                    Ok(())
                })
            },
        );
        assert_eq!(refused.problem, Some(SidebarProblem::OutcomeUnknown));
        assert!(
            fs::symlink_metadata(link.unwrap())
                .unwrap()
                .file_type()
                .is_symlink()
        );
        assert_eq!(fs::read(&target).unwrap(), b"external target");
        assert!(!directory.path().join("sidebar_preference.json").exists());
    }

    #[cfg(unix)]
    #[test]
    fn cleanup_failure_after_present_publication_is_uncertain() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("sidebar_preference.json");
        fs::write(&path, EXISTING).unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        let uncertain = store.update_with(
            SidebarPreference::Collapsed,
            initial.revision,
            |path, preference, original, lease| {
                persist_with(path, preference, original, lease, |step, scratch| {
                    if step == PersistStep::Published {
                        // Replace the displaced document with a directory.
                        fs::remove_file(scratch).unwrap();
                        fs::create_dir(scratch).unwrap();
                    }
                    Ok(())
                })
            },
        );
        assert_eq!(uncertain.problem, Some(SidebarProblem::OutcomeUnknown));
        assert_eq!(uncertain.sidebar_preference, SidebarPreference::Expanded);
        assert_eq!(fs::read_dir(directory.path()).unwrap().count(), 3);
        let recovered = store.read();
        assert_eq!(recovered.problem, None);
        assert_eq!(recovered.sidebar_preference, SidebarPreference::Collapsed);
    }

    #[cfg(unix)]
    #[test]
    fn atomic_rename_primitives_refuse_clobber_and_swap_entries() {
        let directory = tempfile::tempdir().unwrap();
        let first = directory.path().join("first");
        let second = directory.path().join("second");
        fs::write(&first, b"first").unwrap();
        fs::write(&second, b"second").unwrap();
        let refused = rename_checked(&first, &second, RenameMode::NoReplace).unwrap_err();
        assert_eq!(refused.kind(), std::io::ErrorKind::AlreadyExists);
        assert_eq!(fs::read(&second).unwrap(), b"second");
        rename_checked(&first, &second, RenameMode::Exchange).unwrap();
        assert_eq!(fs::read(&first).unwrap(), b"second");
        assert_eq!(fs::read(&second).unwrap(), b"first");
        let vacant = directory.path().join("vacant");
        rename_checked(&first, &vacant, RenameMode::NoReplace).unwrap();
        assert!(!first.exists());
        assert_eq!(fs::read(&vacant).unwrap(), b"second");
    }

    #[test]
    fn hard_linked_sidecar_and_invalid_replacement_deny_every_writer() {
        let directory = tempfile::tempdir().unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        let lock = directory.path().join(LEASE_NAME);
        let alias = directory.path().join("lock-alias");
        fs::hard_link(&lock, &alias).unwrap();
        let refused = store.update(SidebarPreference::Collapsed, initial.revision);
        assert_eq!(refused.problem, Some(SidebarProblem::WriteFailed));
        assert!(!directory.path().join("sidebar_preference.json").exists());
        assert_eq!(store.read().problem, Some(SidebarProblem::ReadFailed));
        fs::remove_file(alias).unwrap();
        let recovered = store.read();
        assert_eq!(recovered.problem, None);
        assert_eq!(
            store
                .update(SidebarPreference::Collapsed, recovered.revision)
                .problem,
            None
        );
    }

    #[cfg(windows)]
    #[test]
    fn windows_handle_identity_and_link_count_cover_original_file() {
        let directory = tempfile::tempdir().unwrap();
        let first = directory.path().join("first.json");
        let second = directory.path().join("second.json");
        fs::write(&first, b"first").unwrap();
        let a = File::open(&first).unwrap();
        let b = File::open(&first).unwrap();
        let (original, links) = identity(&a).unwrap();
        assert_eq!(links, 1);
        assert!(identity(&b).unwrap().0 == original);
        fs::hard_link(&first, &second).unwrap();
        assert_eq!(identity(&a).unwrap().1, 2);
        assert!(open_evidence(&first, false).is_err());
        fs::remove_file(&second).unwrap();
        fs::write(&second, b"second").unwrap();
        assert!(identity(&File::open(second).unwrap()).unwrap().0 != original);
    }

    #[test]
    fn publish_barrier_runs_after_publication_and_before_cleanup() {
        for existing in [false, true] {
            let directory = tempfile::tempdir().unwrap();
            let path = directory.path().join("sidebar_preference.json");
            if existing {
                fs::write(&path, br#"{"version":1,"sidebar_preference":"expanded"}"#).unwrap();
            }
            let store = SidebarStore::new(Some(directory.path().into()));
            let initial = store.read();
            let mut steps = Vec::new();
            let committed = store.update_with(
                SidebarPreference::Collapsed,
                initial.revision,
                |path, preference, original, lease| {
                    persist_with(path, preference, original, lease, |step, _| {
                        steps.push(step);
                        Ok(())
                    })
                },
            );
            assert_eq!(committed.problem, None);
            assert_eq!(committed.sidebar_preference, SidebarPreference::Collapsed);
            let position = |wanted| steps.iter().position(|step| *step == wanted).unwrap();
            assert!(position(PersistStep::Published) < position(PersistStep::PublishBarrier));
            assert!(position(PersistStep::PublishBarrier) < position(PersistStep::ScratchRemoved));
        }
    }

    #[test]
    fn publish_barrier_flushes_only_the_created_file_object() {
        let directory = tempfile::tempdir().unwrap();
        let created = directory.path().join("created");
        let other = directory.path().join("other");
        fs::write(&created, b"created").unwrap();
        fs::write(&other, b"other").unwrap();
        let mut options = OpenOptions::new();
        options.read(true).write(true);
        let file = options.open(&created).unwrap();
        let (expected, _) = identity(&file).unwrap();
        assert_eq!(flush_published(&file, expected), Ok(()));
        let (foreign, _) = identity(&File::open(&other).unwrap()).unwrap();
        assert_eq!(
            flush_published(&file, foreign),
            Err(SidebarProblem::OutcomeUnknown)
        );
    }

    #[cfg(windows)]
    #[test]
    fn windows_replacement_commits_only_after_the_published_file_flush() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("sidebar_preference.json");
        fs::write(&path, br#"{"version":1,"sidebar_preference":"expanded"}"#).unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        let committed = store.update(SidebarPreference::Collapsed, initial.revision);
        assert_eq!(committed.problem, None);
        assert_eq!(committed.sidebar_preference, SidebarPreference::Collapsed);
        assert_eq!(
            store.read().sidebar_preference,
            SidebarPreference::Collapsed
        );
    }
}
