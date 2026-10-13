// SPDX-License-Identifier: Apache-2.0
use std::{
    collections::BTreeSet,
    fs::{self, File, OpenOptions},
    io::{Read, Write},
    path::{Path, PathBuf},
    sync::Mutex,
    time::Duration,
};

use serde::{Deserialize, Serialize};

const DOCUMENT_NAME: &str = "session-creation-preferences.json";
// Persistent private sidecar; it is never unlinked or replaced because its
// stable identity owns interprocess exclusion.
const LEASE_NAME: &str = "session-creation-preferences.lock";
// FILE_SHARE_READ | FILE_SHARE_WRITE, deliberately without FILE_SHARE_DELETE.
#[cfg(windows)]
const WINDOWS_LEASE_SHARE_MODE: u32 = 0x1 | 0x2;
const LEASE_ATTEMPTS: u32 = 10;
const LEASE_RETRY_DELAY: Duration = Duration::from_millis(10);

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq, PartialOrd, Ord)]
#[serde(rename_all = "kebab-case")]
pub enum CreationKind {
    Session,
    GeneralChat,
    Schedule,
    Checkout,
    RemoteRepository,
    NativeObservation,
    ClaudeLogin,
    ServerRemediation,
    RepositoryRemediation,
    ImportTarget,
}
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq, PartialOrd, Ord)]
#[serde(deny_unknown_fields)]
pub struct Scope {
    pub server_id: String,
    pub device_id: String,
}
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Pair {
    pub agent_id: String,
    pub machine_id: String,
}
#[derive(Clone, Copy, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum Problem {
    Unavailable,
    ReadFailed,
    InvalidDocument,
    UnsupportedVersion,
    WriteFailed,
    OutcomeUnknown,
    Changed,
    Capacity,
}
#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct Snapshot {
    pub revision: u32,
    pub scope: Scope,
    pub pair: Option<Pair>,
    pub problem: Option<Problem>,
}
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
struct Record {
    scope: Scope,
    kind: CreationKind,
    pair: Pair,
}
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
struct DeviceRecord {
    scope: Scope,
    kind: CreationKind,
    machine_id: String,
}
#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct DeviceSnapshot {
    pub revision: u32,
    pub scope: Scope,
    pub machine_id: Option<String>,
    pub problem: Option<Problem>,
}
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Document {
    version: u32,
    records: Vec<Record>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    devices: Vec<DeviceRecord>,
}
struct State {
    revision: u32,
    selected: Option<(Scope, CreationKind)>,
    records: Vec<Record>,
    devices: Vec<DeviceRecord>,
    problem: Option<Problem>,
}
pub struct Store {
    path: Option<PathBuf>,
    state: Mutex<State>,
}
fn valid_id(id: &str) -> bool {
    uuid::Uuid::parse_str(id)
        .is_ok_and(|value| value.hyphenated().to_string() == id && !value.is_nil())
}
impl Scope {
    pub fn valid(&self) -> bool {
        valid_id(&self.server_id) && valid_id(&self.device_id)
    }
}
impl Pair {
    fn valid(&self) -> bool {
        valid_id(&self.agent_id) && valid_id(&self.machine_id)
    }
}
impl State {
    fn advance(&mut self) {
        if let Some(next) = self.revision.checked_add(1) {
            self.revision = next;
        } else {
            self.problem = Some(Problem::Unavailable);
        }
    }

    fn snapshot(&self, scope: &Scope, kind: CreationKind) -> Snapshot {
        Snapshot {
            revision: self.revision,
            scope: scope.clone(),
            pair: self
                .records
                .iter()
                .find(|r| r.scope == *scope && r.kind == kind)
                .map(|r| r.pair.clone()),
            problem: self.problem,
        }
    }
}
impl Store {
    pub fn new(directory: Option<PathBuf>) -> Self {
        Self {
            path: directory.map(|p| p.join(DOCUMENT_NAME)),
            state: Mutex::new(State {
                revision: 0,
                selected: None,
                records: vec![],
                devices: vec![],
                problem: Some(Problem::Unavailable),
            }),
        }
    }

    pub fn read(&self, scope: Scope, kind: CreationKind) -> Snapshot {
        let mut state = self.state.lock().unwrap_or_else(|e| e.into_inner());
        let observed = self
            .path
            .as_deref()
            .ok_or(Problem::Unavailable)
            .and_then(inspect);
        let previous = (
            state.records.clone(),
            state.devices.clone(),
            state.problem,
            state.selected.clone(),
        );
        match observed {
            Ok((records, devices)) if scope.valid() => {
                state.records = records;
                state.devices = devices;
                state.problem = None;
            }
            Ok(_) => state.problem = Some(Problem::InvalidDocument),
            Err(problem) => {
                state.problem = Some(problem);
                tracing::warn!(operation = "creation_preferences_read", ?problem);
            }
        }
        state.selected = Some((scope.clone(), kind));
        if state.revision == 0
            || previous
                != (
                    state.records.clone(),
                    state.devices.clone(),
                    state.problem,
                    state.selected.clone(),
                )
        {
            state.advance();
        }
        state.snapshot(&scope, kind)
    }

    pub fn update(
        &self,
        scope: Scope,
        kind: CreationKind,
        pair: Pair,
        expected_revision: u32,
    ) -> Snapshot {
        self.update_with(scope, kind, pair, expected_revision, persist_guarded)
    }

    fn update_with(
        &self,
        scope: Scope,
        kind: CreationKind,
        pair: Pair,
        expected_revision: u32,
        write: impl FnOnce(
            &Path,
            &[Record],
            &[DeviceRecord],
            &dyn Fn() -> Result<(), Problem>,
        ) -> Result<(), Problem>,
    ) -> Snapshot {
        let mut state = self.state.lock().unwrap_or_else(|e| e.into_inner());
        if state.revision != expected_revision
            || state.selected.as_ref() != Some(&(scope.clone(), kind))
        {
            let mut result = state.snapshot(&scope, kind);
            result.problem = Some(Problem::Changed);
            return result;
        }
        if state.problem.is_some() || state.revision == u32::MAX {
            return state.snapshot(&scope, kind);
        }
        let result = (|| {
            if !scope.valid()
                || !pair.valid()
                || !matches!(kind, CreationKind::Session | CreationKind::GeneralChat)
            {
                return Err(Problem::InvalidDocument);
            }
            let path = self.path.as_deref().ok_or(Problem::Unavailable)?;
            // Hold the interprocess lease from reinspection through the
            // synchronized publication so no other process can interleave.
            let lease = Lease::acquire(path)?;
            let observed = inspect(path)?;
            // A rename-and-replace during inspection would leave this lease
            // excluding nobody, so prove it still owns the path first.
            lease.revalidate()?;
            if observed != (state.records.clone(), state.devices.clone()) {
                return Err(Problem::Changed);
            }
            let mut records = state.records.clone();
            if let Some(record) = records
                .iter_mut()
                .find(|r| r.scope == scope && r.kind == kind)
            {
                record.pair = pair;
            } else {
                if records.len() + state.devices.len() >= 128 {
                    return Err(Problem::Capacity);
                }
                records.push(Record {
                    scope: scope.clone(),
                    kind,
                    pair,
                });
            }
            write(path, &records, &state.devices, &|| lease.revalidate())?;
            Ok(records)
        })();
        match result {
            Ok(records) => {
                state.records = records;
                tracing::info!(
                    operation = "creation_preferences_update",
                    outcome = "committed"
                );
            }
            Err(problem) => {
                state.problem = Some(problem);
                tracing::warn!(operation = "creation_preferences_update", ?problem);
            }
        }
        state.advance();
        state.snapshot(&scope, kind)
    }

    pub fn read_device(&self, scope: Scope, kind: CreationKind) -> DeviceSnapshot {
        self.read(scope.clone(), kind);
        let state = self.state.lock().unwrap_or_else(|e| e.into_inner());
        DeviceSnapshot {
            revision: state.revision,
            scope: scope.clone(),
            machine_id: state
                .devices
                .iter()
                .find(|r| r.scope == scope && r.kind == kind)
                .map(|r| r.machine_id.clone()),
            problem: state.problem,
        }
    }

    pub fn update_device(
        &self,
        scope: Scope,
        kind: CreationKind,
        machine_id: String,
        expected_revision: u32,
    ) -> DeviceSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|e| e.into_inner());
        let snapshot = |state: &State, problem| DeviceSnapshot {
            revision: state.revision,
            scope: scope.clone(),
            machine_id: state
                .devices
                .iter()
                .find(|r| r.scope == scope && r.kind == kind)
                .map(|r| r.machine_id.clone()),
            problem,
        };
        if state.revision != expected_revision
            || state.selected.as_ref() != Some(&(scope.clone(), kind))
        {
            return snapshot(&state, Some(Problem::Changed));
        }
        if state.problem.is_some() || state.revision == u32::MAX {
            return snapshot(&state, state.problem);
        }
        let result = (|| {
            if !scope.valid()
                || !valid_id(&machine_id)
                || matches!(kind, CreationKind::Session | CreationKind::GeneralChat)
            {
                return Err(Problem::InvalidDocument);
            }
            let path = self.path.as_deref().ok_or(Problem::Unavailable)?;
            let lease = Lease::acquire(path)?;
            let observed = inspect(path)?;
            lease.revalidate()?;
            if observed != (state.records.clone(), state.devices.clone()) {
                return Err(Problem::Changed);
            }
            let mut devices = state.devices.clone();
            if let Some(record) = devices
                .iter_mut()
                .find(|r| r.scope == scope && r.kind == kind)
            {
                record.machine_id = machine_id;
            } else {
                if state.records.len() + devices.len() >= 128 {
                    return Err(Problem::Capacity);
                }
                devices.push(DeviceRecord {
                    scope: scope.clone(),
                    kind,
                    machine_id,
                });
            }
            persist_guarded(path, &state.records, &devices, &|| lease.revalidate())?;
            Ok(devices)
        })();
        match result {
            Ok(devices) => {
                state.devices = devices;
                tracing::info!(
                    operation = "device_preferences_update",
                    outcome = "committed"
                );
            }
            Err(problem) => {
                state.problem = Some(problem);
                tracing::warn!(operation = "device_preferences_update", ?problem);
            }
        }
        state.advance();
        snapshot(&state, state.problem)
    }
}
/// Exclusive interprocess lease over the preferences document.
struct Lease {
    file: File,
    lock_path: PathBuf,
}

impl Lease {
    /// Acquires the lease without blocking indefinitely. Contention beyond a
    /// short bounded retry window, an unsafe sidecar or any lock failure is
    /// reported as `WriteFailed` so the caller never hangs or overwrites a
    /// document it could not exclusively inspect.
    fn acquire(path: &Path) -> Result<Self, Problem> {
        let directory = path.parent().ok_or(Problem::WriteFailed)?;
        fs::create_dir_all(directory).map_err(|_| Problem::WriteFailed)?;
        let lock_path = directory.join(LEASE_NAME);
        match fs::symlink_metadata(&lock_path) {
            Ok(metadata) => validate_sidecar(&metadata)?,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
            Err(_) => return Err(Problem::WriteFailed),
        }
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
            // Deny FILE_SHARE_DELETE so the operating system refuses to
            // rename, delete or replace the sidecar (or its containing
            // directory) while this lease is held. That closes the window
            // between the identity guard and the document replacement, which
            // path-based rechecks alone cannot do.
            options
                .share_mode(WINDOWS_LEASE_SHARE_MODE)
                .custom_flags(0x00200000); // FILE_FLAG_OPEN_REPARSE_POINT
        }
        let file = options.open(&lock_path).map_err(|_| {
            tracing::warn!(
                operation = "creation_preferences_lease",
                code = "lease-open-failed"
            );
            Problem::WriteFailed
        })?;
        validate_sidecar(&file.metadata().map_err(|_| Problem::WriteFailed)?)?;
        #[cfg(windows)]
        validate_windows_single_link(&file)?;
        let mut attempt = 1;
        loop {
            match file.try_lock() {
                Ok(()) => break,
                Err(fs::TryLockError::WouldBlock) if attempt < LEASE_ATTEMPTS => {
                    attempt += 1;
                    std::thread::sleep(LEASE_RETRY_DELAY);
                }
                Err(error) => {
                    let code = match error {
                        fs::TryLockError::WouldBlock => "lease-busy",
                        fs::TryLockError::Error(_) => "lease-unavailable",
                    };
                    tracing::warn!(operation = "creation_preferences_lease", code);
                    return Err(Problem::WriteFailed);
                }
            }
        }
        let lease = Self { file, lock_path };
        // The sidecar must still be the locked file; a replaced path would
        // leave this lease excluding nobody.
        lease.revalidate()?;
        Ok(lease)
    }

    /// Proves the locked handle is still the file at the sidecar path. The
    /// path is resolved afresh, so a renamed or replaced sidecar or containing
    /// directory fails the comparison. Call it again before every publication.
    fn revalidate(&self) -> Result<(), Problem> {
        self.validate_identity(&self.lock_path)
    }

    fn validate_identity(&self, lock_path: &Path) -> Result<(), Problem> {
        let linked = fs::symlink_metadata(lock_path).map_err(|_| Problem::WriteFailed)?;
        validate_sidecar(&linked)?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::MetadataExt;
            let opened = self.file.metadata().map_err(|_| Problem::WriteFailed)?;
            if linked.dev() != opened.dev() || linked.ino() != opened.ino() {
                tracing::warn!(
                    operation = "creation_preferences_lease",
                    code = "lease-replaced"
                );
                return Err(Problem::WriteFailed);
            }
        }
        #[cfg(windows)]
        {
            // Re-open the current sidecar path without following a reparse
            // point and compare the stable volume and 128-bit file identities
            // (legacy file index only where unsupported) with the locked
            // handle.
            use std::os::windows::fs::OpenOptionsExt;
            let current = OpenOptions::new()
                .read(true)
                .custom_flags(0x00200000) // FILE_FLAG_OPEN_REPARSE_POINT
                .open(lock_path)
                .map_err(|_| Problem::WriteFailed)?;
            // The path-based metadata cannot report the NTFS link count, so
            // read it from both handles and reject a hard-linked sidecar.
            validate_windows_single_link(&self.file)?;
            validate_windows_single_link(&current)?;
            let locked = windows_file_identity(&self.file)?;
            if locked != windows_file_identity(&current)? {
                tracing::warn!(
                    operation = "creation_preferences_lease",
                    code = "lease-replaced"
                );
                return Err(Problem::WriteFailed);
            }
        }
        Ok(())
    }
}

/// Stable identity of an open Windows file handle.
///
/// The legacy 64-bit file index is not guaranteed to be unique on ReFS, so the
/// 128-bit `FileIdInfo` identifier is preferred. The legacy pair is only a
/// fallback for file systems that cannot report the extended identifier.
#[cfg(windows)]
#[derive(Debug, PartialEq, Eq)]
enum WindowsFileIdentity {
    Extended { volume: u64, file_id: [u8; 16] },
    Legacy { volume: u32, file_index: u64 },
}

/// Confirms that `GetFileInformationByHandleEx(FileIdInfo)` failed because the
/// file system or Windows version cannot report the extended identifier.
#[cfg(windows)]
fn extended_identity_unsupported(code: i32) -> bool {
    use windows_sys::Win32::Foundation::{
        ERROR_INVALID_FUNCTION, ERROR_INVALID_PARAMETER, ERROR_NOT_SUPPORTED,
    };
    matches!(
        code as u32,
        ERROR_INVALID_FUNCTION | ERROR_INVALID_PARAMETER | ERROR_NOT_SUPPORTED
    )
}

#[cfg(windows)]
fn windows_file_identity(file: &File) -> Result<WindowsFileIdentity, Problem> {
    use std::os::windows::io::AsRawHandle;

    use windows_sys::Win32::Storage::FileSystem::{
        BY_HANDLE_FILE_INFORMATION, FILE_ID_INFO, FileIdInfo, GetFileInformationByHandle,
        GetFileInformationByHandleEx,
    };
    let handle = file.as_raw_handle();
    let mut extended: FILE_ID_INFO = unsafe { std::mem::zeroed() };
    // SAFETY: the handle is owned by `file` for the duration of the call and
    // `extended` is a valid, writable buffer of exactly the size passed.
    let extended_ok = unsafe {
        GetFileInformationByHandleEx(
            handle,
            FileIdInfo,
            std::ptr::addr_of_mut!(extended).cast(),
            std::mem::size_of::<FILE_ID_INFO>() as u32,
        )
    } != 0;
    if extended_ok {
        return Ok(WindowsFileIdentity::Extended {
            volume: extended.VolumeSerialNumber,
            file_id: extended.FileId.Identifier,
        });
    }
    // Capture the error before any other call can overwrite it.
    let code = std::io::Error::last_os_error().raw_os_error();
    if !code.is_some_and(extended_identity_unsupported) {
        // The extended identifier is supported on ReFS, where the legacy index
        // is not guaranteed unique, so any other failure must not weaken the
        // comparison.
        tracing::warn!(
            operation = "creation_preferences_lease",
            code = "lease-extended-identity-failed",
            os_error = ?code
        );
        return Err(Problem::WriteFailed);
    }
    tracing::debug!(
        operation = "creation_preferences_lease",
        code = "lease-extended-identity-unsupported"
    );
    let mut information: BY_HANDLE_FILE_INFORMATION = unsafe { std::mem::zeroed() };
    // SAFETY: the handle is owned by `file` for the duration of the call and
    // `information` is a valid, writable out-pointer.
    if unsafe { GetFileInformationByHandle(handle, &mut information) } == 0 {
        tracing::warn!(
            operation = "creation_preferences_lease",
            code = "lease-identity-unavailable"
        );
        return Err(Problem::WriteFailed);
    }
    Ok(WindowsFileIdentity::Legacy {
        volume: information.dwVolumeSerialNumber,
        file_index: (u64::from(information.nFileIndexHigh) << 32)
            | u64::from(information.nFileIndexLow),
    })
}

/// Rejects a Windows sidecar that has more than one hard link.
///
/// `fs::Metadata` does not expose the NTFS link count on stable Rust, so it is
/// read from the open handle. This mirrors the Unix `nlink() != 1` check and
/// fails closed when the count cannot be read.
#[cfg(windows)]
fn validate_windows_single_link(file: &File) -> Result<(), Problem> {
    use std::os::windows::io::AsRawHandle;

    use windows_sys::Win32::Storage::FileSystem::{
        BY_HANDLE_FILE_INFORMATION, GetFileInformationByHandle,
    };
    let mut information: BY_HANDLE_FILE_INFORMATION = unsafe { std::mem::zeroed() };
    // SAFETY: the handle is owned by `file` for the duration of the call and
    // `information` is a valid, writable out-pointer.
    if unsafe { GetFileInformationByHandle(file.as_raw_handle(), &mut information) } == 0 {
        tracing::warn!(
            operation = "creation_preferences_lease",
            code = "lease-link-count-unavailable"
        );
        return Err(Problem::WriteFailed);
    }
    if information.nNumberOfLinks != 1 {
        tracing::warn!(
            operation = "creation_preferences_lease",
            code = "lease-hard-linked",
            links = information.nNumberOfLinks
        );
        return Err(Problem::WriteFailed);
    }
    Ok(())
}

impl Drop for Lease {
    fn drop(&mut self) {
        // Explicit unlock also releases a lease inherited briefly across fork.
        if self.file.unlock().is_err() {
            tracing::warn!(
                operation = "creation_preferences_lease",
                code = "lease-release-failed"
            );
        }
    }
}

fn validate_sidecar(metadata: &fs::Metadata) -> Result<(), Problem> {
    if !metadata.is_file() {
        return Err(Problem::WriteFailed);
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        if metadata.mode() & 0o077 != 0 || metadata.nlink() != 1 {
            return Err(Problem::WriteFailed);
        }
    }
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        if metadata.file_attributes() & 0x400 != 0 {
            return Err(Problem::WriteFailed);
        }
    }
    Ok(())
}
fn inspect(path: &Path) -> Result<(Vec<Record>, Vec<DeviceRecord>), Problem> {
    let metadata = match fs::symlink_metadata(path) {
        Ok(m) => m,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok((vec![], vec![])),
        Err(_) => return Err(Problem::ReadFailed),
    };
    if !metadata.is_file() || metadata.len() > 64 << 10 {
        return Err(Problem::InvalidDocument);
    }
    let file = File::open(path).map_err(|_| Problem::ReadFailed)?;
    let mut bytes = vec![];
    file.take((64 << 10) + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| Problem::ReadFailed)?;
    if bytes.len() > 64 << 10 {
        return Err(Problem::InvalidDocument);
    }
    let value: Document = serde_json::from_slice(&bytes).map_err(|_| Problem::InvalidDocument)?;
    if !matches!(value.version, 1 | 2) {
        return Err(Problem::UnsupportedVersion);
    }
    let mut keys = BTreeSet::new();
    if value.records.len() + value.devices.len() > 128
        || (value.version == 1 && !value.devices.is_empty())
        || value.records.iter().any(|r| {
            !r.scope.valid()
                || !r.pair.valid()
                || !matches!(r.kind, CreationKind::Session | CreationKind::GeneralChat)
                || !keys.insert((r.scope.clone(), r.kind))
        })
        || value.devices.iter().any(|r| {
            !r.scope.valid()
                || !valid_id(&r.machine_id)
                || matches!(r.kind, CreationKind::Session | CreationKind::GeneralChat)
                || !keys.insert((r.scope.clone(), r.kind))
        })
    {
        return Err(Problem::InvalidDocument);
    }
    Ok((value.records, value.devices))
}
#[cfg(test)]
fn persist(path: &Path, records: &[Record]) -> Result<(), Problem> {
    persist_guarded(path, records, &[], &|| Ok(()))
}
/// Publishes the document after `guard` confirms, immediately before the
/// replacement, that the caller's interprocess lease still owns the path, and
/// confirms it again after the replacement before reporting success.
fn persist_guarded(
    path: &Path,
    records: &[Record],
    devices: &[DeviceRecord],
    guard: &dyn Fn() -> Result<(), Problem>,
) -> Result<(), Problem> {
    let parent = path.parent().ok_or(Problem::WriteFailed)?;
    fs::create_dir_all(parent).map_err(|_| Problem::WriteFailed)?;
    let scratch = parent.join(format!(".session-creation-{}.tmp", uuid::Uuid::now_v7()));
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut file = options.open(&scratch).map_err(|_| Problem::WriteFailed)?;
    let result = (|| {
        let bytes = serde_json::to_vec(&Document {
            version: 2,
            records: records.to_vec(),
            devices: devices.to_vec(),
        })
        .map_err(|_| Problem::WriteFailed)?;
        if bytes.len() > 64 << 10 {
            return Err(Problem::Capacity);
        }
        file.write_all(&bytes)
            .and_then(|_| file.sync_all())
            .map_err(|_| Problem::WriteFailed)?;
        drop(file);
        guard()?;
        crate::appearance::replace(&scratch, path).map_err(|_| Problem::WriteFailed)?;
        #[cfg(unix)]
        File::open(parent)
            .and_then(|d| d.sync_all())
            .map_err(|_| Problem::OutcomeUnknown)?;
        // The sidecar can be swapped between the pre-publication guard and the
        // replacement. A writer whose lease no longer excludes others cannot
        // claim the document as committed: another process may already have
        // inspected the previous contents and will overwrite this publication.
        guard().map_err(|_| {
            tracing::warn!(
                operation = "creation_preferences_lease",
                code = "lease-lost-after-publication"
            );
            Problem::OutcomeUnknown
        })?;
        Ok(())
    })();
    let _ = fs::remove_file(scratch);
    result
}

#[cfg(test)]
mod tests {
    use super::*;
    fn scope() -> Scope {
        Scope {
            server_id: uuid::Uuid::now_v7().to_string(),
            device_id: uuid::Uuid::now_v7().to_string(),
        }
    }
    fn pair() -> Pair {
        Pair {
            agent_id: uuid::Uuid::now_v7().to_string(),
            machine_id: uuid::Uuid::now_v7().to_string(),
        }
    }
    #[test]
    fn version_one_pairs_survive_device_extension_and_restart() {
        let temp = tempfile::tempdir().unwrap();
        let selected = scope();
        let original = pair();
        let path = temp.path().join("session-creation-preferences.json");
        fs::write(
            &path,
            serde_json::to_vec(&Document {
                version: 1,
                records: vec![Record {
                    scope: selected.clone(),
                    kind: CreationKind::Session,
                    pair: original.clone(),
                }],
                devices: vec![],
            })
            .unwrap(),
        )
        .unwrap();
        let store = Store::new(Some(temp.path().into()));
        let device = uuid::Uuid::now_v7().to_string();
        let read = store.read_device(selected.clone(), CreationKind::Schedule);
        let saved = store.update_device(
            selected.clone(),
            CreationKind::Schedule,
            device.clone(),
            read.revision,
        );
        assert!(saved.problem.is_none());
        assert_eq!(saved.machine_id, Some(device.clone()));
        let restarted = Store::new(Some(temp.path().into()));
        assert_eq!(
            restarted.read(selected.clone(), CreationKind::Session).pair,
            Some(original)
        );
        assert_eq!(
            restarted
                .read_device(selected.clone(), CreationKind::Schedule)
                .machine_id,
            Some(device)
        );
        assert_eq!(
            restarted
                .read_device(selected.clone(), CreationKind::Checkout)
                .machine_id,
            None
        );
        assert_eq!(
            restarted
                .read_device(scope(), CreationKind::Schedule)
                .machine_id,
            None
        );
        let value: Document = serde_json::from_slice(&fs::read(path).unwrap()).unwrap();
        assert_eq!(value.version, 2);
        assert_eq!(value.devices.len(), 1);
        assert_eq!(value.records.len(), 1);
    }
    #[test]
    fn device_records_share_capacity_and_revision_guards() {
        let temp = tempfile::tempdir().unwrap();
        let store = Store::new(Some(temp.path().into()));
        let selected = scope();
        let read = store.read_device(selected.clone(), CreationKind::Schedule);
        store.read_device(selected.clone(), CreationKind::Checkout);
        assert_eq!(
            store
                .update_device(
                    selected.clone(),
                    CreationKind::Schedule,
                    uuid::Uuid::now_v7().to_string(),
                    read.revision
                )
                .problem,
            Some(Problem::Changed)
        );
        let records: Vec<Record> = (0..128)
            .map(|_| Record {
                scope: scope(),
                kind: CreationKind::Session,
                pair: pair(),
            })
            .collect();
        persist(
            &temp.path().join("session-creation-preferences.json"),
            &records,
        )
        .unwrap();
        let read = store.read_device(selected.clone(), CreationKind::Schedule);
        assert_eq!(
            store
                .update_device(
                    selected,
                    CreationKind::Schedule,
                    uuid::Uuid::now_v7().to_string(),
                    read.revision
                )
                .problem,
            Some(Problem::Capacity)
        );
    }
    #[test]
    fn restart_kind_scope_and_revision_isolation() {
        let temp = tempfile::tempdir().unwrap();
        let store = Store::new(Some(temp.path().into()));
        let scope = scope();
        let session = pair();
        let chat = pair();
        let read = store.read(scope.clone(), CreationKind::Session);
        assert_eq!(read.pair, None);
        assert_eq!(
            store
                .update(
                    scope.clone(),
                    CreationKind::Session,
                    session.clone(),
                    read.revision
                )
                .pair,
            Some(session.clone())
        );
        let read = store.read(scope.clone(), CreationKind::GeneralChat);
        assert_eq!(read.pair, None);
        let saved = store.update(
            scope.clone(),
            CreationKind::GeneralChat,
            chat.clone(),
            read.revision,
        );
        assert!(saved.problem.is_none());
        assert_eq!(
            store
                .update(scope.clone(), CreationKind::Session, pair(), read.revision)
                .problem,
            Some(Problem::Changed)
        );
        let restarted = Store::new(Some(temp.path().into()));
        assert_eq!(
            restarted.read(scope.clone(), CreationKind::Session).pair,
            Some(session)
        );
        assert_eq!(
            restarted
                .read(scope.clone(), CreationKind::GeneralChat)
                .pair,
            Some(chat)
        );
        let foreign = Scope {
            server_id: scope.server_id,
            device_id: uuid::Uuid::now_v7().to_string(),
        };
        assert_eq!(restarted.read(foreign, CreationKind::Session).pair, None);
    }
    #[test]
    fn invalid_documents_are_preserved() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("session-creation-preferences.json");
        let selected = scope();
        let record = Record {
            scope: selected.clone(),
            kind: CreationKind::Session,
            pair: pair(),
        };
        let duplicate = serde_json::to_string(&Document {
            version: 1,
            records: vec![record.clone(), record],
            devices: vec![],
        })
        .unwrap();
        for raw in [
            "{\"version\":1,\"version\":1,\"records\":[]}".to_owned(),
            "{\"version\":3,\"records\":[]}".into(),
            "{\"version\":1,\"records\":[],\"extra\":true}".into(),
            duplicate,
            "x".repeat((64 << 10) + 1),
        ] {
            fs::write(&path, &raw).unwrap();
            let store = Store::new(Some(temp.path().into()));
            let read = store.read(selected.clone(), CreationKind::Session);
            assert!(read.problem.is_some());
            let result = store.update(
                selected.clone(),
                CreationKind::Session,
                pair(),
                read.revision,
            );
            assert!(result.problem.is_some());
            assert_eq!(fs::read_to_string(&path).unwrap(), raw);
        }
    }
    #[test]
    fn capacity_failed_and_uncertain_writes_preserve_history_until_read() {
        let temp = tempfile::tempdir().unwrap();
        let store = Store::new(Some(temp.path().into()));
        let selected = scope();
        let original = pair();
        let read = store.read(selected.clone(), CreationKind::Session);
        store.update(
            selected.clone(),
            CreationKind::Session,
            original.clone(),
            read.revision,
        );
        let read = store.read(selected.clone(), CreationKind::Session);
        let failed = store.update_with(
            selected.clone(),
            CreationKind::Session,
            pair(),
            read.revision,
            |_, _, _, _| Err(Problem::WriteFailed),
        );
        assert_eq!(failed.pair, Some(original.clone()));
        assert_eq!(failed.problem, Some(Problem::WriteFailed));
        let read = store.read(selected.clone(), CreationKind::Session);
        let next = pair();
        let uncertain = store.update_with(
            selected.clone(),
            CreationKind::Session,
            next.clone(),
            read.revision,
            |path, rows, devices, guard| {
                persist_guarded(path, rows, devices, guard)?;
                Err(Problem::OutcomeUnknown)
            },
        );
        assert_eq!(uncertain.pair, Some(original));
        assert_eq!(uncertain.problem, Some(Problem::OutcomeUnknown));
        assert_eq!(store.read(selected, CreationKind::Session).pair, Some(next));
        let records: Vec<Record> = (0..128)
            .map(|_| Record {
                scope: scope(),
                kind: CreationKind::Session,
                pair: pair(),
            })
            .collect();
        persist(
            &temp.path().join("session-creation-preferences.json"),
            &records,
        )
        .unwrap();
        let selected = scope();
        let read = store.read(selected.clone(), CreationKind::Session);
        assert_eq!(
            store
                .update(selected, CreationKind::Session, pair(), read.revision)
                .problem,
            Some(Problem::Capacity)
        );
    }
    fn wait_for_marker(path: &Path) {
        for _ in 0..1000 {
            if path.exists() {
                return;
            }
            std::thread::sleep(Duration::from_millis(10));
        }
        panic!("timed out waiting for {}", path.display());
    }

    struct FixtureProcess(std::process::Child);
    impl Drop for FixtureProcess {
        fn drop(&mut self) {
            let _ = self.0.kill();
            let _ = self.0.wait();
        }
    }

    // Run only by the parent fixture below, in a separate OS process with its
    // own independently opened handles.
    #[test]
    fn cooperating_process_child() {
        let Some(directory) = std::env::var_os("DELIDEV_CREATION_LEASE_FIXTURE_DIR") else {
            return;
        };
        let directory = PathBuf::from(directory);
        let contender = Store::new(Some(directory.clone()));
        let stale = Store::new(Some(directory.clone()));
        let child_scope = Scope {
            server_id: fs::read_to_string(directory.join("child-server")).unwrap(),
            device_id: fs::read_to_string(directory.join("child-device")).unwrap(),
        };
        let child_pair = pair();
        let first = contender.read(child_scope.clone(), CreationKind::Session);
        let original = stale.read(child_scope.clone(), CreationKind::Session);
        assert_eq!(first.problem, None);
        assert_eq!(original.problem, None);
        fs::write(directory.join("child-ready"), b"ready").unwrap();
        wait_for_marker(&directory.join("attempt-while-locked"));
        let started = std::time::Instant::now();
        let refused = contender.update(
            child_scope.clone(),
            CreationKind::Session,
            child_pair.clone(),
            first.revision,
        );
        assert_eq!(refused.problem, Some(Problem::WriteFailed));
        assert_eq!(refused.pair, None);
        assert!(started.elapsed() < Duration::from_secs(5));
        fs::write(directory.join("child-refused"), b"refused").unwrap();
        wait_for_marker(&directory.join("attempt-after-unlock"));
        let changed = stale.update(
            child_scope.clone(),
            CreationKind::Session,
            child_pair.clone(),
            original.revision,
        );
        assert_eq!(changed.problem, Some(Problem::Changed));
        assert_eq!(changed.pair, None);
        let refreshed = stale.read(child_scope.clone(), CreationKind::Session);
        assert_eq!(refreshed.problem, None);
        let saved = stale.update(
            child_scope,
            CreationKind::Session,
            child_pair.clone(),
            refreshed.revision,
        );
        assert_eq!(saved.problem, None);
        assert_eq!(saved.pair, Some(child_pair));
    }

    #[test]
    fn cooperating_processes_keep_distinct_pairs_through_one_lease() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join(DOCUMENT_NAME);
        let parent_scope = scope();
        let child_scope = scope();
        fs::write(
            directory.path().join("child-server"),
            &child_scope.server_id,
        )
        .unwrap();
        fs::write(
            directory.path().join("child-device"),
            &child_scope.device_id,
        )
        .unwrap();
        let store = Store::new(Some(directory.path().into()));
        let initial = store.read(parent_scope.clone(), CreationKind::Session);
        let parent_pair = pair();
        let mut child = FixtureProcess(
            std::process::Command::new(std::env::current_exe().unwrap())
                .args([
                    "--exact",
                    "session_creation_preferences::tests::cooperating_process_child",
                    "--nocapture",
                ])
                .env("DELIDEV_CREATION_LEASE_FIXTURE_DIR", directory.path())
                .stdout(std::process::Stdio::null())
                .spawn()
                .unwrap(),
        );
        wait_for_marker(&directory.path().join("child-ready"));
        let committed = store.update_with(
            parent_scope.clone(),
            CreationKind::Session,
            parent_pair.clone(),
            initial.revision,
            |path, records, devices, guard| {
                fs::write(directory.path().join("attempt-while-locked"), b"go").unwrap();
                wait_for_marker(&directory.path().join("child-refused"));
                // The child could neither inspect nor publish while this
                // process retained its lease after original inspection.
                assert!(!path.exists());
                persist_guarded(path, records, devices, guard)
            },
        );
        assert_eq!(committed.problem, None);
        assert_eq!(committed.pair, Some(parent_pair.clone()));
        assert!(directory.path().join(LEASE_NAME).is_file());
        fs::write(directory.path().join("attempt-after-unlock"), b"go").unwrap();
        assert!(child.0.wait().unwrap().success());
        let restarted = Store::new(Some(directory.path().into()));
        assert_eq!(
            restarted.read(parent_scope, CreationKind::Session).pair,
            Some(parent_pair)
        );
        let child_read = restarted.read(child_scope, CreationKind::Session);
        assert!(child_read.pair.is_some());
        assert_eq!(inspect(&path).unwrap().0.len(), 2);
        assert!(directory.path().join(LEASE_NAME).is_file());
    }

    #[test]
    fn held_lease_refuses_updates_without_hanging_or_changing_document() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join(DOCUMENT_NAME);
        let store = Store::new(Some(directory.path().into()));
        let selected = scope();
        let original = pair();
        let read = store.read(selected.clone(), CreationKind::Session);
        assert_eq!(
            store
                .update(
                    selected.clone(),
                    CreationKind::Session,
                    original.clone(),
                    read.revision
                )
                .problem,
            None
        );
        let before = fs::read(&path).unwrap();
        let held = Lease::acquire(&path).unwrap();
        let started = std::time::Instant::now();
        let read = store.read(selected.clone(), CreationKind::Session);
        let refused = store.update(
            selected.clone(),
            CreationKind::Session,
            pair(),
            read.revision,
        );
        assert_eq!(refused.problem, Some(Problem::WriteFailed));
        assert_eq!(refused.pair, Some(original.clone()));
        let device_scope = selected.clone();
        let read = store.read_device(device_scope.clone(), CreationKind::Schedule);
        let refused_device = store.update_device(
            device_scope,
            CreationKind::Schedule,
            uuid::Uuid::now_v7().to_string(),
            read.revision,
        );
        assert_eq!(refused_device.problem, Some(Problem::WriteFailed));
        assert!(started.elapsed() < Duration::from_secs(5));
        assert_eq!(fs::read(&path).unwrap(), before);
        drop(held);
        let read = store.read(selected.clone(), CreationKind::Session);
        assert_eq!(read.problem, None);
        let next = pair();
        assert_eq!(
            store
                .update(selected, CreationKind::Session, next.clone(), read.revision)
                .pair,
            Some(next)
        );
        assert!(directory.path().join(LEASE_NAME).is_file());
    }

    #[cfg(unix)]
    #[test]
    fn lease_sidecar_is_private_and_never_followed_through_links() {
        use std::os::unix::fs::PermissionsExt;
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join(DOCUMENT_NAME);
        let store = Store::new(Some(directory.path().into()));
        let selected = scope();
        let read = store.read(selected.clone(), CreationKind::Session);
        assert_eq!(
            store
                .update(
                    selected.clone(),
                    CreationKind::Session,
                    pair(),
                    read.revision
                )
                .problem,
            None
        );
        let lock = directory.path().join(LEASE_NAME);
        assert_eq!(
            fs::metadata(&lock).unwrap().permissions().mode() & 0o777,
            0o600
        );
        fs::remove_file(&lock).unwrap();
        let foreign = directory.path().join("foreign");
        fs::write(&foreign, "foreign").unwrap();
        std::os::unix::fs::symlink(&foreign, &lock).unwrap();
        let before = fs::read(&path).unwrap();
        let read = store.read(selected.clone(), CreationKind::Session);
        let refused = store.update(selected, CreationKind::Session, pair(), read.revision);
        assert_eq!(refused.problem, Some(Problem::WriteFailed));
        assert_eq!(fs::read_to_string(&foreign).unwrap(), "foreign");
        assert!(
            fs::symlink_metadata(&lock)
                .unwrap()
                .file_type()
                .is_symlink()
        );
        assert_eq!(fs::read(&path).unwrap(), before);
    }

    #[cfg(unix)]
    #[test]
    fn replaced_lease_sidecar_blocks_publication() {
        use std::os::unix::fs::PermissionsExt;
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join(DOCUMENT_NAME);
        let lock = directory.path().join(LEASE_NAME);
        let store = Store::new(Some(directory.path().into()));
        let selected = scope();
        let original = pair();
        let read = store.read(selected.clone(), CreationKind::Session);
        assert_eq!(
            store
                .update(
                    selected.clone(),
                    CreationKind::Session,
                    original.clone(),
                    read.revision
                )
                .problem,
            None
        );
        let before = fs::read(&path).unwrap();
        let read = store.read(selected.clone(), CreationKind::Session);
        let replaced = store.update_with(
            selected.clone(),
            CreationKind::Session,
            pair(),
            read.revision,
            |path, records, devices, guard| {
                // Another process could now lock a fresh sidecar while this
                // process still holds the renamed one.
                fs::rename(&lock, directory.path().join("moved.lock")).unwrap();
                let fresh = fs::File::create(&lock).unwrap();
                fresh
                    .set_permissions(fs::Permissions::from_mode(0o600))
                    .unwrap();
                persist_guarded(path, records, devices, guard)
            },
        );
        assert_eq!(replaced.problem, Some(Problem::WriteFailed));
        assert_eq!(replaced.pair, Some(original));
        assert_eq!(fs::read(&path).unwrap(), before);
        let leftovers = fs::read_dir(directory.path())
            .unwrap()
            .filter_map(Result::ok)
            .filter(|entry| entry.file_name().to_string_lossy().ends_with(".tmp"))
            .count();
        assert_eq!(leftovers, 0);
    }

    #[cfg(windows)]
    #[test]
    fn held_lease_sidecar_cannot_be_renamed_or_deleted_on_windows() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join(DOCUMENT_NAME);
        let lock = directory.path().join(LEASE_NAME);
        let lease = Lease::acquire(&path).unwrap();
        assert!(fs::rename(&lock, directory.path().join("moved.lock")).is_err());
        assert!(fs::remove_file(&lock).is_err());
        lease.revalidate().unwrap();
        drop(lease);
        assert!(fs::rename(&lock, directory.path().join("moved.lock")).is_ok());
    }

    #[cfg(windows)]
    #[test]
    fn only_unsupported_errors_allow_the_legacy_identity_fallback() {
        // ERROR_INVALID_FUNCTION, ERROR_NOT_SUPPORTED, ERROR_INVALID_PARAMETER.
        for code in [1, 50, 87] {
            assert!(extended_identity_unsupported(code));
        }
        // ERROR_ACCESS_DENIED, ERROR_INVALID_HANDLE, ERROR_NOT_ENOUGH_MEMORY.
        for code in [5, 6, 8] {
            assert!(!extended_identity_unsupported(code));
        }
    }

    #[cfg(unix)]
    #[test]
    fn lease_replaced_during_publication_is_not_reported_committed() {
        use std::os::unix::fs::PermissionsExt;
        let directory = tempfile::tempdir().unwrap();
        let lock = directory.path().join(LEASE_NAME);
        let store = Store::new(Some(directory.path().into()));
        let selected = scope();
        let read = store.read(selected.clone(), CreationKind::Session);
        let swapped = store.update_with(
            selected.clone(),
            CreationKind::Session,
            pair(),
            read.revision,
            |path, records, devices, guard| {
                let calls = std::cell::Cell::new(0);
                persist_guarded(path, records, devices, &|| {
                    // The first call precedes the replacement; swap the
                    // sidecar right after it so only the second call sees it.
                    calls.set(calls.get() + 1);
                    let result = guard();
                    if calls.get() == 1 {
                        fs::rename(&lock, directory.path().join("moved.lock")).unwrap();
                        let fresh = fs::File::create(&lock).unwrap();
                        fresh
                            .set_permissions(fs::Permissions::from_mode(0o600))
                            .unwrap();
                    }
                    result
                })
            },
        );
        assert_eq!(swapped.problem, Some(Problem::OutcomeUnknown));
        assert_eq!(swapped.pair, None);
    }

    #[cfg(unix)]
    #[test]
    fn group_readable_lease_sidecar_is_rejected() {
        use std::os::unix::fs::PermissionsExt;
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join(DOCUMENT_NAME);
        let lock = directory.path().join(LEASE_NAME);
        fs::write(&lock, "").unwrap();
        fs::set_permissions(&lock, fs::Permissions::from_mode(0o644)).unwrap();
        assert!(matches!(Lease::acquire(&path), Err(Problem::WriteFailed)));
    }
    #[cfg(unix)]
    #[test]
    fn linked_file_is_not_read_or_replaced() {
        let temp = tempfile::tempdir().unwrap();
        let target = temp.path().join("other");
        fs::write(&target, "{}").unwrap();
        std::os::unix::fs::symlink(
            &target,
            temp.path().join("session-creation-preferences.json"),
        )
        .unwrap();
        let store = Store::new(Some(temp.path().into()));
        assert_eq!(
            store.read(scope(), CreationKind::Session).problem,
            Some(Problem::InvalidDocument)
        );
        assert_eq!(fs::read_to_string(target).unwrap(), "{}");
    }
}
