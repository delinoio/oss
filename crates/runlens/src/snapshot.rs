use std::{
    fs,
    io::Read,
    path::{Path, PathBuf},
    sync::atomic::Ordering,
};

use sha2::{Digest, Sha256};

use crate::{
    config::{Limits, patterns},
    entries::Entries,
    error::{Error, Result},
    model::{ChangeKind, FileKind, FileState, Knowledge, ObservationIssue},
    privacy::Redactor,
};

pub struct Snapshot {
    pub entries: Entries<FileState>,
    pub complete: bool,
    pub bytes: u64,
}
pub fn excluded(
    path: &Path,
    root: &Path,
    exclusions: &globset::GlobSet,
    temporary: &[PathBuf],
) -> bool {
    let relative = path.strip_prefix(root).unwrap_or(path);
    relative.components().any(|part| part.as_os_str() == ".git")
        || temporary.iter().any(|p| path.starts_with(p))
        || exclusions.is_match(relative)
}
pub fn take(
    root: &Path,
    exclusions: &[String],
    temporary: &[PathBuf],
    redactor: &Redactor,
    limits: &Limits,
    cancelled: &tokio_util::sync::CancellationToken,
) -> Result<Snapshot> {
    let matcher = patterns(exclusions)?;
    let mut result = Snapshot {
        entries: Entries::new(limits.memory_bytes / 8),
        complete: true,
        bytes: 0,
    };
    let walker = walkdir::WalkDir::new(root)
        .follow_links(false)
        .into_iter()
        .filter_entry(|entry| !excluded(entry.path(), root, &matcher, temporary));
    for entry in walker {
        if cancelled.is_cancelled() {
            result.complete = false;
            break;
        }
        let entry = match entry {
            Ok(entry) => entry,
            Err(error) => {
                result.complete = false;
                if let Some(path) = error.path() {
                    result.entries.insert(
                        redactor.path(path),
                        FileState::unknown(ObservationIssue::PermissionDenied),
                    )?;
                }
                continue;
            }
        };
        if result.entries.len() >= limits.max_paths || result.bytes >= limits.total_bytes / 3 {
            result.complete = false;
            break;
        }
        let path = entry.path();
        let key = redactor.path(path);
        let state = inspect(path, root, &matcher, temporary, redactor, limits, cancelled);
        result.complete &= state.knowledge != Knowledge::Unknown;
        let charged = result.bytes
            + key.len() as u64
            + serde_json::to_vec(&state)
                .map_err(|_| Error::storage())?
                .len() as u64;
        if charged > limits.total_bytes / 3 {
            result.complete = false;
            break;
        }
        result.bytes = charged;
        if result.entries.get(&key)?.is_some() {
            result.complete = false;
            result
                .entries
                .insert(key, FileState::unknown(ObservationIssue::Redacted))?;
        } else {
            result.entries.insert(key, state)?;
        }
        if result.entries.len().is_multiple_of(10000) {
            tracing::info!(
                stage = "snapshot",
                paths = result.entries.len(),
                spilled = result.entries.spilled(),
                "scan progress"
            );
        }
    }
    if redactor.path_redacted.load(Ordering::Relaxed) {
        result.complete = false;
    }
    Ok(result)
}
fn inspect(
    path: &Path,
    root: &Path,
    exclusions: &globset::GlobSet,
    temporary: &[PathBuf],
    redactor: &Redactor,
    limits: &Limits,
    cancelled: &tokio_util::sync::CancellationToken,
) -> FileState {
    match inspect_inner(
        path, root, exclusions, temporary, redactor, limits, cancelled,
    ) {
        Ok(state) => state,
        Err(error) => FileState::unknown(if error.kind() == std::io::ErrorKind::PermissionDenied {
            ObservationIssue::PermissionDenied
        } else {
            ObservationIssue::Io
        }),
    }
}
fn inspect_inner(
    path: &Path,
    root: &Path,
    exclusions: &globset::GlobSet,
    temporary: &[PathBuf],
    redactor: &Redactor,
    limits: &Limits,
    cancelled: &tokio_util::sync::CancellationToken,
) -> std::io::Result<FileState> {
    let before = fs::symlink_metadata(path)?;
    if path.to_str().is_none() {
        return Ok(FileState::unknown(ObservationIssue::NonUnicode));
    }
    let mut state = FileState {
        knowledge: Knowledge::Known,
        kind: None,
        size: None,
        sha256: None,
        executable: None,
        link_target: None,
        reason: None,
    };
    if before.is_symlink() {
        state.kind = Some(FileKind::Symlink);
        let target = fs::read_link(path)?;
        let Some(target) = target.to_str() else {
            return Ok(FileState::unknown(ObservationIssue::NonUnicode));
        };
        let target = redactor.text(target);
        if target.contains("[redacted]") {
            redactor.path_redacted.store(true, Ordering::Relaxed);
            return Ok(FileState::unknown(ObservationIssue::Redacted));
        }
        state.link_target = Some(target);
    } else if before.is_file() {
        state.kind = Some(FileKind::File);
        state.size = Some(before.len());
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            state.executable = Some(before.permissions().mode() & 0o111 != 0);
        }
        // Do not follow a symlink swapped in between lstat and open. Rechecking
        // identity around streaming reads preserves unknown for concurrent edits.
        let mut options = fs::OpenOptions::new();
        options.read(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK);
        }
        #[cfg(windows)]
        {
            use std::os::windows::fs::OpenOptionsExt;
            options.custom_flags(0x00200000);
        }
        let mut file = options.open(path)?;
        if !same(&before, &file.metadata()?) {
            return Ok(FileState::unknown(ObservationIssue::Unstable));
        }
        let mut digest = Sha256::new();
        let mut buffer = [0u8; 65536];
        loop {
            if cancelled.is_cancelled() {
                return Ok(FileState::unknown(ObservationIssue::Cancelled));
            }
            let count = file.read(&mut buffer)?;
            if count == 0 {
                break;
            }
            digest.update(&buffer[..count]);
        }
        if !same(&before, &file.metadata()?) {
            return Ok(FileState::unknown(ObservationIssue::Unstable));
        }
        state.sha256 = Some(hex::encode(digest.finalize()));
    } else if before.is_dir() {
        state.kind = Some(FileKind::Directory);
        // Membership sorting spills through the same bounded metadata abstraction.
        let mut names: Entries<bool> = Entries::new(limits.memory_bytes / 8);
        let mut bytes = 0u64;
        for entry in fs::read_dir(path)? {
            if cancelled.is_cancelled() {
                return Ok(FileState::unknown(ObservationIssue::Cancelled));
            }
            let entry = entry?;
            if excluded(&entry.path(), root, exclusions, temporary) {
                continue;
            }
            let Some(name) = entry.file_name().to_str().map(str::to_owned) else {
                return Ok(FileState::unknown(ObservationIssue::NonUnicode));
            };
            bytes += name.len() as u64;
            if names.len() >= limits.max_paths || bytes > limits.total_bytes / 3 {
                return Ok(FileState::unknown(ObservationIssue::CollectionLimit));
            }
            names
                .insert(name, true)
                .map_err(|_| std::io::Error::other("directory observation failed"))?;
        }
        let mut digest = Sha256::new();
        for entry in names.iter() {
            let (name, _) =
                entry.map_err(|_| std::io::Error::other("directory observation failed"))?;
            digest.update((name.len() as u64).to_le_bytes());
            digest.update(name.as_bytes());
        }
        state.sha256 = Some(hex::encode(digest.finalize()));
    } else {
        state.kind = Some(FileKind::Other);
        state.knowledge = Knowledge::Unknown;
        state.reason = Some(ObservationIssue::OutsideScope);
    }
    if !same(&before, &fs::symlink_metadata(path)?) {
        return Ok(FileState::unknown(ObservationIssue::Unstable));
    }
    Ok(state)
}
pub(crate) fn same(left: &fs::Metadata, right: &fs::Metadata) -> bool {
    let basic = left.file_type() == right.file_type()
        && left.len() == right.len()
        && left.modified().ok() == right.modified().ok();
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        basic
            && left.dev() == right.dev()
            && left.ino() == right.ino()
            && left.ctime() == right.ctime()
            && left.ctime_nsec() == right.ctime_nsec()
            && left.mode() == right.mode()
    }
    #[cfg(not(unix))]
    {
        basic
    }
}
pub fn difference(before: &FileState, after: &FileState) -> Option<ChangeKind> {
    if before.knowledge == Knowledge::Unknown || after.knowledge == Knowledge::Unknown {
        return Some(ChangeKind::Unknown);
    }
    match (before.knowledge, after.knowledge) {
        (Knowledge::Missing, Knowledge::Missing) => None,
        (Knowledge::Missing, _) => Some(ChangeKind::Created),
        (_, Knowledge::Missing) => Some(ChangeKind::Deleted),
        _ if before.kind != after.kind => Some(ChangeKind::TypeChanged),
        _ if before != after => Some(ChangeKind::Modified),
        _ => None,
    }
}
pub fn changes(
    before: &Entries<FileState>,
    after: &Entries<FileState>,
    before_complete: bool,
    after_complete: bool,
) -> Result<Entries<ChangeKind>> {
    let mut changes = Entries::default();
    for entry in before.iter() {
        let (path, old) = entry?;
        let new = after.get(&path)?.unwrap_or_else(|| {
            if after_complete {
                FileState::missing()
            } else {
                FileState::unknown(ObservationIssue::CollectionLimit)
            }
        });
        if let Some(change) = difference(&old, &new) {
            changes.insert(path, change)?;
        }
    }
    for entry in after.iter() {
        let (path, new) = entry?;
        if before.get(&path)?.is_none() {
            let old = if before_complete {
                FileState::missing()
            } else {
                FileState::unknown(ObservationIssue::CollectionLimit)
            };
            if let Some(change) = difference(&old, &new) {
                changes.insert(path, change)?;
            }
        }
    }
    Ok(changes)
}
