//! Private pre-execution snapshot for a verified observed-input reproduction.

#[cfg(windows)]
use std::os::windows::{ffi::OsStringExt, io::AsRawHandle};
#[cfg(unix)]
use std::os::{
    fd::AsRawFd,
    unix::fs::{symlink, MetadataExt},
};
use std::{
    collections::{BTreeMap, BTreeSet},
    fs::{self, File},
    io::{self, Read, Write},
    path::{Component, Path, PathBuf},
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
};

use sha2::{Digest, Sha256};
use walkdir::WalkDir;

use crate::{coverage::Selector, record::FileIdentity};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ReproFailure {
    Cancellation,
    Unavailable,
    BlockedInput,
    ExternalLink,
    FileLimit,
    ByteLimit,
    UnstableInput,
    UncollectedInput,
}

impl std::fmt::Display for ReproFailure {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(match self {
            Self::Cancellation => "cancelled",
            Self::Unavailable => "snapshot_unavailable",
            Self::BlockedInput => "blocked_input",
            Self::ExternalLink => "external_link",
            Self::FileLimit => "snapshot_file_limit",
            Self::ByteLimit => "snapshot_byte_limit",
            Self::UnstableInput => "unstable_input",
            Self::UncollectedInput => "uncollected_input",
        })
    }
}

impl std::error::Error for ReproFailure {}

#[derive(Debug, Clone)]
pub struct SnapshotFile {
    pub relative: PathBuf,
    pub sha256: String,
    pub size: u64,
    pub identity: FileIdentity,
    pub hard_link_to: Option<PathBuf>,
}

#[derive(Debug, Clone)]
pub struct SnapshotLink {
    /// Canonical project-relative target used for containment and identity
    /// checks.
    pub target: PathBuf,
    /// Exact target spelling read from the source symlink for candidate
    /// recreation.
    pub raw_target: PathBuf,
}

pub struct Snapshot {
    cancelled: Arc<AtomicBool>,
    root: PathBuf,
    private: tempfile::TempDir,
    eligible: BTreeSet<PathBuf>,
    eligible_directories: BTreeMap<PathBuf, FileIdentity>,
    selected_identities: BTreeMap<FileIdentity, PathBuf>,
    files: BTreeMap<PathBuf, SnapshotFile>,
    links: BTreeMap<PathBuf, SnapshotLink>,
    directories: BTreeSet<PathBuf>,
    total_bytes: u64,
    max_bytes: u64,
    max_files: usize,
}

fn denied(relative: &Path) -> bool {
    relative.components().any(|component| {
        let Component::Normal(name) = component else {
            return false;
        };
        let name = name.to_string_lossy().to_ascii_lowercase();
        matches!(
            name.as_str(),
            ".git" | ".ssh" | ".env" | "id_rsa" | "id_dsa" | "id_ecdsa" | "id_ed25519"
        ) || name.starts_with(".env.")
            || [".pem", ".key", ".p12", ".pfx"]
                .iter()
                .any(|suffix| name.ends_with(suffix))
    })
}

fn valid_relative(relative: &Path) -> bool {
    !relative.as_os_str().is_empty()
        && !relative.is_absolute()
        && relative
            .components()
            .all(|component| matches!(component, Component::Normal(_)))
}

fn append_link_suffix(target: &Path, suffix: &Path) -> PathBuf {
    // Joining an empty suffix adds a trailing separator, which makes native
    // file opens treat an unchanged regular-file target as a directory.
    if suffix.as_os_str().is_empty() {
        target.to_path_buf()
    } else {
        target.join(suffix)
    }
}

fn staged_link_target(
    raw_target: &Path,
    canonical_target: &Path,
    root: &Path,
    candidate: &Path,
    link: &Path,
) -> Result<PathBuf, ReproFailure> {
    if !raw_target.is_absolute() {
        return Ok(raw_target.to_path_buf());
    }
    // Absolute links inside the project must point into the candidate rather
    // than back into the source tree. Preserve their spelling after the
    // project-root prefix when it can be identified lexically.
    if let Ok(suffix) = raw_target.strip_prefix(root) {
        return Ok(candidate.join(suffix));
    }
    pathdiff::diff_paths(canonical_target, link.parent().unwrap_or(Path::new(".")))
        .ok_or(ReproFailure::Unavailable)
}

#[cfg(target_os = "linux")]
fn opened_path(file: &File) -> Result<PathBuf, ReproFailure> {
    fs::read_link(format!("/proc/self/fd/{}", file.as_raw_fd()))
        .map_err(|_| ReproFailure::Unavailable)
}

#[cfg(target_os = "macos")]
fn opened_path(file: &File) -> Result<PathBuf, ReproFailure> {
    use std::{ffi::OsString, os::unix::ffi::OsStringExt};

    let mut path = [0_u8; libc::MAXPATHLEN as usize];
    // SAFETY: F_GETPATH writes a NUL-terminated absolute pathname into this
    // exact buffer for the still-open descriptor.
    if unsafe { libc::fcntl(file.as_raw_fd(), libc::F_GETPATH, path.as_mut_ptr()) } != 0 {
        return Err(ReproFailure::Unavailable);
    }
    let end = path
        .iter()
        .position(|byte| *byte == 0)
        .ok_or(ReproFailure::Unavailable)?;
    Ok(PathBuf::from(OsString::from_vec(path[..end].to_vec())))
}

#[cfg(windows)]
fn opened_path(file: &File) -> Result<PathBuf, ReproFailure> {
    use std::ffi::OsString;

    use winapi::um::fileapi::GetFinalPathNameByHandleW;

    let mut path = vec![0_u16; 32768];
    // SAFETY: the file owns a valid handle and this buffer holds the maximum
    // Windows extended path length plus its terminating code unit.
    let length = unsafe {
        GetFinalPathNameByHandleW(
            file.as_raw_handle().cast(),
            path.as_mut_ptr(),
            path.len() as u32,
            0,
        )
    } as usize;
    if length == 0 || length >= path.len() {
        return Err(ReproFailure::Unavailable);
    }
    path.truncate(length);
    let path = PathBuf::from(OsString::from_wide(&path));
    // GetFinalPathNameByHandleW uses the extended-length prefix even when
    // the source root uses the ordinary drive-letter spelling. Remove only
    // that transport prefix so containment checks compare equivalent paths.
    Ok(path
        .strip_prefix(Path::new("\\\\?\\"))
        .map(Path::to_path_buf)
        .unwrap_or(path))
}

#[cfg(unix)]
fn source_identity(_path: &Path, metadata: &fs::Metadata) -> Result<FileIdentity, ReproFailure> {
    Ok(FileIdentity::Inode {
        device: metadata.dev(),
        inode: metadata.ino(),
    })
}

#[cfg(windows)]
fn source_identity(path: &Path, _metadata: &fs::Metadata) -> Result<FileIdentity, ReproFailure> {
    file_id::get_file_id(path)
        .map(FileIdentity::from)
        .map_err(|_| ReproFailure::Unavailable)
}

#[cfg(unix)]
fn stage_symlink(source: &Path, target: &Path, link: &Path) -> io::Result<()> {
    let _ = source;
    symlink(target, link)
}

#[cfg(windows)]
fn stage_symlink(source: &Path, target: &Path, link: &Path) -> io::Result<()> {
    if source.is_dir() {
        std::os::windows::fs::symlink_dir(target, link)
    } else {
        std::os::windows::fs::symlink_file(target, link)
    }
}

fn check_cancelled(cancelled: &AtomicBool) -> Result<(), ReproFailure> {
    #[cfg(windows)]
    let requested =
        cancelled.load(Ordering::SeqCst) || crate::cli::WINDOWS_CANCELLED.load(Ordering::SeqCst);
    #[cfg(not(windows))]
    let requested = cancelled.load(Ordering::SeqCst);
    if requested {
        Err(ReproFailure::Cancellation)
    } else {
        Ok(())
    }
}

fn hash_file(
    path: &Path,
    root: &Path,
    cancelled: &AtomicBool,
) -> Result<(String, u64, FileIdentity), ReproFailure> {
    check_cancelled(cancelled)?;
    let mut file = File::open(path).map_err(|_| ReproFailure::Unavailable)?;
    let opened = opened_path(&file)?;
    if !opened.starts_with(root) {
        return Err(ReproFailure::ExternalLink);
    }
    let metadata = file.metadata().map_err(|_| ReproFailure::Unavailable)?;
    let identity = source_identity(path, &metadata)?;
    let mut digest = Sha256::new();
    let mut length = 0_u64;
    let mut buffer = [0_u8; 65536];
    loop {
        check_cancelled(cancelled)?;
        let read = file
            .read(&mut buffer)
            .map_err(|_| ReproFailure::Unavailable)?;
        if read == 0 {
            break;
        }
        digest.update(&buffer[..read]);
        length = length
            .checked_add(read as u64)
            .ok_or(ReproFailure::ByteLimit)?;
    }
    Ok((format!("{:x}", digest.finalize()), length, identity))
}

impl Snapshot {
    pub fn check_cancelled(&self) -> Result<(), ReproFailure> {
        check_cancelled(&self.cancelled)
    }

    pub fn selected_alias_for_identity(&self, identity: FileIdentity) -> Option<&Path> {
        self.selected_identities
            .get(&identity)
            .map(PathBuf::as_path)
    }

    pub fn contains_selected(&self, relative: &Path) -> bool {
        self.eligible.contains(relative)
    }

    pub fn selected_entries_within<'a>(
        &'a self,
        directory: &'a Path,
    ) -> impl Iterator<Item = &'a PathBuf> + 'a {
        self.eligible_directories
            .keys()
            .chain(self.eligible.iter())
            .filter(move |relative| relative.starts_with(directory))
    }

    pub fn contains_selected_directory(&self, relative: &Path) -> bool {
        self.eligible_directories.contains_key(relative)
    }

    pub fn selected_directory_has_identity(&self, relative: &Path, identity: FileIdentity) -> bool {
        self.eligible_directories.get(relative) == Some(&identity)
    }

    pub fn selected_path_has_identity(&self, relative: &Path, identity: FileIdentity) -> bool {
        valid_relative(relative)
            && self.eligible.contains(relative)
            && self.snapshot_file(relative).map(|file| file.identity) == Some(identity)
    }

    fn snapshot_file(&self, relative: &Path) -> Option<&SnapshotFile> {
        let mut prefix = PathBuf::new();
        for component in relative.components() {
            prefix.push(component.as_os_str());
            if let Some(link) = self.links.get(&prefix) {
                let suffix = relative.strip_prefix(&prefix).ok()?;
                return self.snapshot_file(&append_link_suffix(&link.target, suffix));
            }
        }
        self.files.get(relative)
    }

    pub fn is_blocked(relative: &Path) -> bool {
        denied(relative)
    }

    pub fn links(&self) -> &BTreeMap<PathBuf, SnapshotLink> {
        &self.links
    }

    /// Copy all selected eligible files before running the original command.
    /// Only observed requirements are staged into the eventual candidate.
    pub fn take(
        root: &Path,
        selector: &Selector,
        max_bytes: u64,
        max_files: usize,
    ) -> Result<Self, ReproFailure> {
        Self::take_with_cancel(
            root,
            selector,
            max_bytes,
            max_files,
            Arc::new(AtomicBool::new(false)),
        )
    }

    pub fn take_with_cancel(
        root: &Path,
        selector: &Selector,
        max_bytes: u64,
        max_files: usize,
        cancelled: Arc<AtomicBool>,
    ) -> Result<Self, ReproFailure> {
        check_cancelled(&cancelled)?;
        if max_bytes == 0 {
            return Err(ReproFailure::ByteLimit);
        }
        if max_files == 0 {
            return Err(ReproFailure::FileLimit);
        }
        let root = fs::canonicalize(root).map_err(|_| ReproFailure::Unavailable)?;
        if !root.is_dir() {
            return Err(ReproFailure::Unavailable);
        }
        let private = tempfile::Builder::new()
            .prefix("clibox-fspy-repro-")
            .tempdir()
            .map_err(|_| ReproFailure::Unavailable)?;
        let mut snapshot = Self {
            cancelled,
            root: root.clone(),
            private,
            eligible: BTreeSet::new(),
            eligible_directories: BTreeMap::new(),
            selected_identities: BTreeMap::new(),
            files: BTreeMap::new(),
            links: BTreeMap::new(),
            directories: BTreeSet::new(),
            total_bytes: 0,
            max_bytes,
            max_files,
        };
        let mut external_selected = false;
        let mut blocked_identities = BTreeSet::new();
        let walker = WalkDir::new(&root)
            .follow_links(true)
            .into_iter()
            .filter_entry(|entry| {
                if entry.path() == root {
                    return true;
                }
                let relative = match entry.path().strip_prefix(&root) {
                    Ok(relative) => relative,
                    Err(_) => return false,
                };
                match fs::canonicalize(entry.path()) {
                    Ok(resolved) if resolved.starts_with(&root) => true,
                    Ok(_) => {
                        if selector.matches(&native_relative(relative)) {
                            external_selected = true;
                        }
                        false
                    }
                    Err(error)
                        if error.kind() == io::ErrorKind::NotFound && entry.path_is_symlink() =>
                    {
                        false
                    }
                    Err(_) => true,
                }
            });
        for entry in walker {
            check_cancelled(&snapshot.cancelled)?;
            let entry = match entry {
                Ok(entry) => entry,
                Err(error)
                    if error.path().is_some_and(|path| {
                        path.is_symlink()
                            && fs::metadata(path)
                                .is_err_and(|error| error.kind() == io::ErrorKind::NotFound)
                    }) =>
                {
                    continue
                }
                Err(_) => return Err(ReproFailure::Unavailable),
            };
            let relative = entry
                .path()
                .strip_prefix(&root)
                .map_err(|_| ReproFailure::Unavailable)?;
            if denied(relative) {
                if entry.file_type().is_file() {
                    let metadata =
                        fs::metadata(entry.path()).map_err(|_| ReproFailure::Unavailable)?;
                    blocked_identities.insert(source_identity(entry.path(), &metadata)?);
                }
                continue;
            }
            if entry.file_type().is_dir() && !relative.as_os_str().is_empty() {
                snapshot.add_path(relative)?;
                let metadata = fs::metadata(entry.path()).map_err(|_| ReproFailure::Unavailable)?;
                snapshot.claim_entry()?;
                snapshot.eligible_directories.insert(
                    relative.to_path_buf(),
                    source_identity(entry.path(), &metadata)?,
                );
                continue;
            }
            if !entry.file_type().is_file() {
                continue;
            }
            if !selector.matches(&native_relative(relative)) {
                continue;
            }
            if denied(relative) {
                continue;
            }
            snapshot.eligible.insert(relative.to_path_buf());
            snapshot.add_path(relative)?;
            let resolved = fs::canonicalize(entry.path()).map_err(|_| ReproFailure::Unavailable)?;
            let target = resolved
                .strip_prefix(&root)
                .map_err(|_| ReproFailure::ExternalLink)?;
            let file = snapshot
                .files
                .get(target)
                .ok_or(ReproFailure::Unavailable)?;
            snapshot
                .selected_identities
                .entry(file.identity)
                .or_insert_with(|| relative.to_path_buf());
        }
        if external_selected {
            return Err(ReproFailure::ExternalLink);
        }
        if snapshot
            .selected_identities
            .keys()
            .any(|identity| blocked_identities.contains(identity))
        {
            return Err(ReproFailure::BlockedInput);
        }
        Ok(snapshot)
    }

    fn claim_entry(&self) -> Result<(), ReproFailure> {
        if self.files.len() + self.links.len() + self.eligible_directories.len() >= self.max_files {
            Err(ReproFailure::FileLimit)
        } else {
            Ok(())
        }
    }

    fn add_path(&mut self, relative: &Path) -> Result<(), ReproFailure> {
        check_cancelled(&self.cancelled)?;
        if !valid_relative(relative) {
            return Err(ReproFailure::Unavailable);
        }
        if denied(relative) {
            return Err(ReproFailure::BlockedInput);
        }
        let mut prefix = PathBuf::new();
        for component in relative.components() {
            prefix.push(component.as_os_str());
            let source = self.root.join(&prefix);
            let metadata = fs::symlink_metadata(&source).map_err(|_| ReproFailure::Unavailable)?;
            if metadata.file_type().is_symlink() {
                let raw_target = fs::read_link(&source).map_err(|_| ReproFailure::Unavailable)?;
                let resolved = fs::canonicalize(&source).map_err(|_| ReproFailure::Unavailable)?;
                let target = resolved
                    .strip_prefix(&self.root)
                    .map_err(|_| ReproFailure::ExternalLink)?
                    .to_path_buf();
                if denied(&target) {
                    return Err(ReproFailure::BlockedInput);
                }
                if !self.links.contains_key(&prefix) {
                    self.claim_entry()?;
                    self.links.insert(
                        prefix.clone(),
                        SnapshotLink {
                            target: target.clone(),
                            raw_target,
                        },
                    );
                }
                let suffix = relative
                    .strip_prefix(&prefix)
                    .map_err(|_| ReproFailure::Unavailable)?;
                let target_path = append_link_suffix(&target, suffix);
                return self.add_path(&target_path);
            }
            if metadata.is_dir() {
                if !self.directories.contains(&prefix) {
                    self.directories.insert(prefix.clone());
                }
                continue;
            }
            if prefix != relative || !metadata.is_file() {
                return Err(ReproFailure::Unavailable);
            }
            if self.files.contains_key(&prefix) {
                return Ok(());
            }
            self.claim_entry()?;
            let canonical = fs::canonicalize(&source).map_err(|_| ReproFailure::Unavailable)?;
            if !canonical.starts_with(&self.root) {
                return Err(ReproFailure::ExternalLink);
            }
            let destination = self.private.path().join(&prefix);
            if let Some(parent) = destination.parent() {
                fs::create_dir_all(parent).map_err(|_| ReproFailure::Unavailable)?;
            }
            let mut input = File::open(&source).map_err(|_| ReproFailure::Unavailable)?;
            let opened = opened_path(&input)?;
            if !opened.starts_with(&self.root) {
                return Err(ReproFailure::ExternalLink);
            }
            let metadata = input.metadata().map_err(|_| ReproFailure::Unavailable)?;
            let identity = source_identity(&source, &metadata)?;
            let mut output = File::create(&destination).map_err(|_| ReproFailure::Unavailable)?;
            let mut digest = Sha256::new();
            let mut size = 0_u64;
            let mut buffer = [0_u8; 65536];
            loop {
                check_cancelled(&self.cancelled)?;
                let read = input
                    .read(&mut buffer)
                    .map_err(|_| ReproFailure::Unavailable)?;
                if read == 0 {
                    break;
                }
                size = size
                    .checked_add(read as u64)
                    .ok_or(ReproFailure::ByteLimit)?;
                if self
                    .total_bytes
                    .checked_add(size)
                    .is_none_or(|total| total > self.max_bytes)
                {
                    return Err(ReproFailure::ByteLimit);
                }
                digest.update(&buffer[..read]);
                output
                    .write_all(&buffer[..read])
                    .map_err(|_| ReproFailure::Unavailable)?;
            }
            output
                .set_permissions(metadata.permissions())
                .map_err(|_| ReproFailure::Unavailable)?;
            output.sync_all().map_err(|_| ReproFailure::Unavailable)?;
            self.total_bytes += size;
            let sha256 = format!("{:x}", digest.finalize());
            if hash_file(&source, &self.root, &self.cancelled)? != (sha256.clone(), size, identity)
            {
                return Err(ReproFailure::UnstableInput);
            }
            self.files.insert(
                prefix.clone(),
                SnapshotFile {
                    relative: prefix.clone(),
                    sha256,
                    size,
                    identity,
                    hard_link_to: None,
                },
            );
        }
        Ok(())
    }

    /// Recheck only collected source objects after the original execution.
    pub fn verify_required(&self, required: &BTreeSet<PathBuf>) -> Result<(), ReproFailure> {
        for relative in required {
            check_cancelled(&self.cancelled)?;
            if !valid_relative(relative) {
                return Err(ReproFailure::UncollectedInput);
            }
            if denied(relative) {
                return Err(ReproFailure::BlockedInput);
            }
            if !self.eligible.contains(relative)
                && !self.eligible_directories.contains_key(relative)
            {
                return Err(ReproFailure::UncollectedInput);
            }
            self.verify_path(relative)?;
        }
        Ok(())
    }

    fn verify_path(&self, relative: &Path) -> Result<(), ReproFailure> {
        check_cancelled(&self.cancelled)?;
        let mut prefix = PathBuf::new();
        for component in relative.components() {
            prefix.push(component.as_os_str());
            if let Some(link) = self.links.get(&prefix) {
                let actual = fs::canonicalize(self.root.join(&prefix))
                    .map_err(|_| ReproFailure::UnstableInput)?;
                let relative_target = actual
                    .strip_prefix(&self.root)
                    .map_err(|_| ReproFailure::UnstableInput)?;
                if relative_target != link.target {
                    return Err(ReproFailure::UnstableInput);
                }
                let suffix = relative
                    .strip_prefix(&prefix)
                    .map_err(|_| ReproFailure::UnstableInput)?;
                return self.verify_path(&append_link_suffix(&link.target, suffix));
            }
        }
        if let Some(expected) = self.eligible_directories.get(relative) {
            let path = self.root.join(relative);
            let metadata = fs::metadata(&path).map_err(|_| ReproFailure::UnstableInput)?;
            if !metadata.is_dir()
                || source_identity(&path, &metadata).map_err(|_| ReproFailure::UnstableInput)?
                    != *expected
            {
                return Err(ReproFailure::UnstableInput);
            }
            return Ok(());
        }
        let expected = self
            .files
            .get(relative)
            .ok_or(ReproFailure::UncollectedInput)?;
        let actual =
            hash_file(&self.root.join(relative), &self.root, &self.cancelled).map_err(|error| {
                if error == ReproFailure::Cancellation {
                    error
                } else {
                    ReproFailure::UnstableInput
                }
            })?;
        if actual != (expected.sha256.clone(), expected.size, expected.identity) {
            return Err(ReproFailure::UnstableInput);
        }
        Ok(())
    }

    /// Stage only the observed selected inputs. The candidate is a separate
    /// working directory owned by the caller; no source file is moved.
    pub fn stage_required(
        &self,
        required: &BTreeSet<PathBuf>,
        candidate: &Path,
    ) -> Result<Vec<SnapshotFile>, ReproFailure> {
        self.verify_required(required)?;
        let mut staged = BTreeMap::new();
        let mut staged_identities = BTreeMap::new();
        for relative in required {
            self.stage_path(relative, candidate, &mut staged, &mut staged_identities)?;
        }
        Ok(staged.into_values().collect())
    }

    fn stage_path(
        &self,
        relative: &Path,
        candidate: &Path,
        staged: &mut BTreeMap<PathBuf, SnapshotFile>,
        staged_identities: &mut BTreeMap<FileIdentity, PathBuf>,
    ) -> Result<(), ReproFailure> {
        check_cancelled(&self.cancelled)?;
        let mut prefix = PathBuf::new();
        for component in relative.components() {
            prefix.push(component.as_os_str());
            if let Some(link) = self.links.get(&prefix) {
                let suffix = relative
                    .strip_prefix(&prefix)
                    .map_err(|_| ReproFailure::Unavailable)?;
                self.stage_path(
                    &append_link_suffix(&link.target, suffix),
                    candidate,
                    staged,
                    staged_identities,
                )?;
                let candidate_link = candidate.join(&prefix);
                if let Some(parent) = candidate_link.parent() {
                    fs::create_dir_all(parent).map_err(|_| ReproFailure::Unavailable)?;
                }
                if !candidate_link.is_symlink() {
                    let relative_target = staged_link_target(
                        &link.raw_target,
                        &link.target,
                        &self.root,
                        candidate,
                        &prefix,
                    )?;
                    stage_symlink(&self.root.join(&prefix), &relative_target, &candidate_link)
                        .map_err(|_| ReproFailure::Unavailable)?;
                }
                return Ok(());
            }
        }
        if self.eligible_directories.contains_key(relative) {
            fs::create_dir_all(candidate.join(relative)).map_err(|_| ReproFailure::Unavailable)?;
            return Ok(());
        }
        let file = self
            .files
            .get(relative)
            .ok_or(ReproFailure::UncollectedInput)?;
        if staged.contains_key(relative) {
            return Ok(());
        }
        let destination = candidate.join(relative);
        if let Some(parent) = destination.parent() {
            fs::create_dir_all(parent).map_err(|_| ReproFailure::Unavailable)?;
        }
        let mut staged_file = file.clone();
        if let Some(first) = staged_identities.get(&file.identity) {
            let source = staged.get(first).ok_or(ReproFailure::Unavailable)?;
            if (source.sha256.as_str(), source.size) != (file.sha256.as_str(), file.size) {
                return Err(ReproFailure::UnstableInput);
            }
            fs::hard_link(candidate.join(first), &destination)
                .map_err(|_| ReproFailure::Unavailable)?;
            staged_file.hard_link_to = Some(first.clone());
        } else {
            let mut input = File::open(self.private.path().join(relative))
                .map_err(|_| ReproFailure::Unavailable)?;
            let mut output = File::create(&destination).map_err(|_| ReproFailure::Unavailable)?;
            let mut buffer = [0_u8; 65536];
            loop {
                check_cancelled(&self.cancelled)?;
                let count = input
                    .read(&mut buffer)
                    .map_err(|_| ReproFailure::Unavailable)?;
                if count == 0 {
                    break;
                }
                output
                    .write_all(&buffer[..count])
                    .map_err(|_| ReproFailure::Unavailable)?;
            }
            output
                .set_permissions(
                    input
                        .metadata()
                        .map_err(|_| ReproFailure::Unavailable)?
                        .permissions(),
                )
                .map_err(|_| ReproFailure::Unavailable)?;
            staged_identities.insert(file.identity, relative.to_path_buf());
        }
        staged.insert(relative.to_path_buf(), staged_file);
        Ok(())
    }
}

#[cfg(unix)]
fn native_relative(path: &Path) -> crate::record::NativePath {
    use std::os::unix::ffi::OsStrExt;
    crate::record::NativePath::UnixBytes(path.as_os_str().as_bytes().to_vec())
}

#[cfg(windows)]
fn native_relative(path: &Path) -> crate::record::NativePath {
    use std::os::windows::ffi::OsStrExt;
    crate::record::NativePath::WindowsUtf16(path.as_os_str().encode_wide().collect())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn enumeration_stages_empty_and_subdirectories() {
        let directory = tempfile::tempdir().unwrap();
        fs::create_dir_all(directory.path().join("listing/empty")).unwrap();
        fs::create_dir_all(directory.path().join("listing/nested/leaf")).unwrap();
        let selector = Selector::new(&["**".into()], &[]).unwrap();
        let snapshot = Snapshot::take(directory.path(), &selector, 1024, 16).unwrap();
        let required = snapshot
            .selected_entries_within(Path::new("listing"))
            .cloned()
            .collect::<BTreeSet<_>>();
        assert_eq!(required.len(), 4);
        let candidate = tempfile::tempdir().unwrap();
        assert!(snapshot
            .stage_required(&required, candidate.path())
            .unwrap()
            .is_empty());
        assert!(candidate.path().join("listing/empty").is_dir());
        assert!(candidate.path().join("listing/nested/leaf").is_dir());
    }

    #[test]
    fn cancellation_stops_snapshot_and_staging() {
        let directory = tempfile::tempdir().unwrap();
        fs::write(directory.path().join("input.txt"), vec![b'x'; 256 * 1024]).unwrap();
        let selector = Selector::new(&["*.txt".into()], &[]).unwrap();
        let cancelled = Arc::new(AtomicBool::new(true));
        assert!(matches!(
            Snapshot::take_with_cancel(
                directory.path(),
                &selector,
                1024 * 1024,
                10,
                Arc::clone(&cancelled)
            ),
            Err(ReproFailure::Cancellation)
        ));
        cancelled.store(false, Ordering::SeqCst);
        let snapshot = Snapshot::take_with_cancel(
            directory.path(),
            &selector,
            1024 * 1024,
            10,
            Arc::clone(&cancelled),
        )
        .unwrap();
        let candidate = tempfile::tempdir().unwrap();
        cancelled.store(true, Ordering::SeqCst);
        assert!(matches!(
            snapshot.stage_required(
                &BTreeSet::from([PathBuf::from("input.txt")]),
                candidate.path()
            ),
            Err(ReproFailure::Cancellation)
        ));
        assert!(!candidate.path().join("input.txt").exists());
    }

    #[test]
    fn stages_required_hardlink_aliases_as_one_file() {
        let directory = tempfile::tempdir().unwrap();
        fs::write(directory.path().join("a.txt"), b"input").unwrap();
        fs::hard_link(
            directory.path().join("a.txt"),
            directory.path().join("b.txt"),
        )
        .unwrap();
        let selector = Selector::new(&["*.txt".into()], &[]).unwrap();
        let snapshot = Snapshot::take(directory.path(), &selector, 1024, 10).unwrap();
        let required = BTreeSet::from([PathBuf::from("a.txt"), PathBuf::from("b.txt")]);
        let candidate = tempfile::tempdir().unwrap();
        let staged = snapshot
            .stage_required(&required, candidate.path())
            .unwrap();
        assert_eq!(staged.len(), 2);
        assert_eq!(staged[1].hard_link_to, Some(PathBuf::from("a.txt")));
        let first = candidate.path().join("a.txt");
        let second = candidate.path().join("b.txt");
        assert_eq!(
            source_identity(&first, &fs::metadata(&first).unwrap()).unwrap(),
            source_identity(&second, &fs::metadata(&second).unwrap()).unwrap()
        );
        fs::write(first, b"changed").unwrap();
        assert_eq!(fs::read(second).unwrap(), b"changed");
    }

    #[test]
    fn stages_selected_file_symlink_and_target() {
        let directory = tempfile::tempdir().unwrap();
        fs::create_dir(directory.path().join("real")).unwrap();
        fs::write(directory.path().join("real/input.txt"), b"input").unwrap();
        stage_symlink(
            &directory.path().join("real/input.txt"),
            Path::new("real/input.txt"),
            &directory.path().join("alias.txt"),
        )
        .unwrap();
        let selector = Selector::new(&["alias.txt".into()], &[]).unwrap();
        let snapshot = Snapshot::take(directory.path(), &selector, 1024, 10).unwrap();
        let target = PathBuf::from("real/input.txt");
        let expected = snapshot.files.get(&target).unwrap();
        assert!(snapshot.selected_path_has_identity(Path::new("alias.txt"), expected.identity));
        let required = BTreeSet::from([PathBuf::from("alias.txt")]);
        snapshot.verify_required(&required).unwrap();
        let candidate = tempfile::tempdir().unwrap();
        let staged = snapshot
            .stage_required(&required, candidate.path())
            .unwrap();
        assert_eq!(staged.len(), 1);
        assert_eq!(staged[0].relative, target);
        assert_eq!(staged[0].sha256, expected.sha256);
        assert_eq!(staged[0].size, 5);
        assert_eq!(
            fs::read_link(candidate.path().join("alias.txt")).unwrap(),
            target
        );
        assert_eq!(
            fs::read(candidate.path().join("alias.txt")).unwrap(),
            b"input"
        );
    }

    #[test]
    fn preserves_raw_file_symlink_target_spelling() {
        let directory = tempfile::tempdir().unwrap();
        fs::write(directory.path().join("input.txt"), b"input").unwrap();
        stage_symlink(
            &directory.path().join("input.txt"),
            Path::new("./input.txt"),
            &directory.path().join("alias.txt"),
        )
        .unwrap();
        let selector = Selector::new(&["alias.txt".into()], &[]).unwrap();
        let snapshot = Snapshot::take(directory.path(), &selector, 1024, 10).unwrap();
        let candidate = tempfile::tempdir().unwrap();
        snapshot
            .stage_required(
                &BTreeSet::from([PathBuf::from("alias.txt")]),
                candidate.path(),
            )
            .unwrap();
        assert_eq!(
            fs::read_link(candidate.path().join("alias.txt")).unwrap(),
            Path::new("./input.txt")
        );
    }

    #[test]
    fn rejects_changed_file_symlink_targets_before_staging() {
        enum Change {
            Content,
            Identity,
            LinkTarget,
        }
        for change in [Change::Content, Change::Identity, Change::LinkTarget] {
            let directory = tempfile::tempdir().unwrap();
            let input = directory.path().join("input.txt");
            let alias = directory.path().join("alias.txt");
            fs::write(&input, b"input").unwrap();
            stage_symlink(&input, Path::new("input.txt"), &alias).unwrap();
            let selector = Selector::new(&["alias.txt".into()], &[]).unwrap();
            let snapshot = Snapshot::take(directory.path(), &selector, 1024, 10).unwrap();
            let required = BTreeSet::from([PathBuf::from("alias.txt")]);
            snapshot.verify_required(&required).unwrap();
            match change {
                Change::Content => fs::write(&input, b"other").unwrap(),
                Change::Identity => {
                    let replacement = directory.path().join("replacement.txt");
                    fs::write(&replacement, b"input").unwrap();
                    fs::remove_file(&input).unwrap();
                    fs::rename(replacement, &input).unwrap();
                }
                Change::LinkTarget => {
                    let replacement = directory.path().join("replacement.txt");
                    fs::write(&replacement, b"input").unwrap();
                    fs::remove_file(&alias).unwrap();
                    stage_symlink(&replacement, Path::new("replacement.txt"), &alias).unwrap();
                }
            }
            let candidate = tempfile::tempdir().unwrap();
            assert!(matches!(
                snapshot.stage_required(&required, candidate.path()),
                Err(ReproFailure::UnstableInput)
            ));
            assert_eq!(fs::read_dir(candidate.path()).unwrap().count(), 0);
        }
    }

    #[test]
    fn rejects_selected_file_symlinks_to_blocked_or_external_targets() {
        for expected in [ReproFailure::BlockedInput, ReproFailure::ExternalLink] {
            let directory = tempfile::tempdir().unwrap();
            let root = directory.path().join("project");
            fs::create_dir(&root).unwrap();
            let target = match expected {
                ReproFailure::BlockedInput => root.join(".env"),
                ReproFailure::ExternalLink => directory.path().join("external.txt"),
                _ => unreachable!(),
            };
            fs::write(&target, b"fixture").unwrap();
            stage_symlink(&target, &target, &root.join("alias.txt")).unwrap();
            let selector = Selector::new(&["alias.txt".into()], &[]).unwrap();
            assert!(matches!(
                Snapshot::take(&root, &selector, 1024, 10),
                Err(error) if error == expected
            ));
        }
    }

    #[cfg(unix)]
    #[test]
    fn stages_observed_internal_link_and_target_without_unobserved_files() {
        let directory = tempfile::tempdir().unwrap();
        fs::create_dir(directory.path().join("real")).unwrap();
        fs::write(directory.path().join("real/a.txt"), b"alpha").unwrap();
        fs::write(directory.path().join("real/b.txt"), b"beta").unwrap();
        symlink("real", directory.path().join("alias")).unwrap();
        let selector = Selector::new(&["alias/**".into()], &[]).unwrap();
        let snapshot = Snapshot::take(directory.path(), &selector, 1024, 100).unwrap();
        let required = BTreeSet::from([PathBuf::from("alias/a.txt")]);
        let candidate = tempfile::tempdir().unwrap();
        let staged = snapshot
            .stage_required(&required, candidate.path())
            .unwrap();
        assert_eq!(staged.len(), 1);
        assert!(candidate.path().join("alias").is_symlink());
        assert_eq!(
            fs::read(candidate.path().join("alias/a.txt")).unwrap(),
            b"alpha"
        );
        assert!(!candidate.path().join("real/b.txt").exists());
    }

    #[test]
    fn blocks_required_credentials_and_changed_inputs() {
        let directory = tempfile::tempdir().unwrap();
        fs::write(directory.path().join(".env"), b"TOKEN=secret").unwrap();
        fs::write(directory.path().join("input.txt"), b"before").unwrap();
        let selector = Selector::new(&["*".into()], &[]).unwrap();
        let snapshot = Snapshot::take(directory.path(), &selector, 1024, 100).unwrap();
        assert!(matches!(
            snapshot.verify_required(&BTreeSet::from([PathBuf::from(".env")])),
            Err(ReproFailure::BlockedInput)
        ));
        fs::write(directory.path().join("input.txt"), b"after").unwrap();
        assert!(matches!(
            snapshot.verify_required(&BTreeSet::from([PathBuf::from("input.txt")])),
            Err(ReproFailure::UnstableInput)
        ));
    }

    #[test]
    fn rejects_replaced_input_with_identical_bytes() {
        let directory = tempfile::tempdir().unwrap();
        let input = directory.path().join("input.txt");
        fs::write(&input, b"same bytes").unwrap();
        let selector = Selector::new(&["*.txt".into()], &[]).unwrap();
        let snapshot = Snapshot::take(directory.path(), &selector, 1024, 10).unwrap();
        let relative = PathBuf::from("input.txt");
        let expected = snapshot.files.get(&relative).unwrap().identity;
        assert!(snapshot.selected_path_has_identity(&relative, expected));
        let replacement = directory.path().join("replacement.tmp");
        fs::write(&replacement, b"same bytes").unwrap();
        fs::remove_file(&input).unwrap();
        fs::rename(&replacement, &input).unwrap();
        let observed = source_identity(&input, &fs::metadata(&input).unwrap()).unwrap();
        assert_ne!(observed, expected);
        assert!(!snapshot.selected_path_has_identity(&relative, observed));
        assert!(snapshot.selected_path_has_identity(&relative, expected));
        assert!(matches!(
            snapshot.verify_required(&BTreeSet::from([relative])),
            Err(ReproFailure::UnstableInput)
        ));
    }

    #[test]
    fn rejects_selected_hardlink_to_blocked_credential_path() {
        let directory = tempfile::tempdir().unwrap();
        let blocked = directory.path().join(".env");
        fs::write(&blocked, b"SECRET=value").unwrap();
        fs::hard_link(&blocked, directory.path().join("input.txt")).unwrap();
        let selector = Selector::new(&["*.txt".into()], &[]).unwrap();
        assert!(matches!(
            Snapshot::take(directory.path(), &selector, 1024, 10),
            Err(ReproFailure::BlockedInput)
        ));

        fs::remove_file(&blocked).unwrap();
        fs::create_dir(directory.path().join(".ssh")).unwrap();
        fs::hard_link(
            directory.path().join("input.txt"),
            directory.path().join(".ssh/key"),
        )
        .unwrap();
        assert!(matches!(
            Snapshot::take(directory.path(), &selector, 1024, 10),
            Err(ReproFailure::BlockedInput)
        ));
    }
}
