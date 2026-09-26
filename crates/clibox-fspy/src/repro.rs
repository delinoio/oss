//! Private pre-execution snapshot for a verified observed-input reproduction.

use std::{
    collections::{BTreeMap, BTreeSet},
    fs::{self, File},
    io::{self, Read, Write},
    os::{fd::AsRawFd, unix::fs::symlink},
    path::{Component, Path, PathBuf},
};

use sha2::{Digest, Sha256};
use walkdir::WalkDir;

use crate::coverage::Selector;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ReproFailure {
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
}

pub struct Snapshot {
    root: PathBuf,
    private: tempfile::TempDir,
    eligible: BTreeSet<PathBuf>,
    files: BTreeMap<PathBuf, SnapshotFile>,
    links: BTreeMap<PathBuf, PathBuf>,
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

fn hash_file(path: &Path, root: &Path) -> Result<(String, u64), ReproFailure> {
    let mut file = File::open(path).map_err(|_| ReproFailure::Unavailable)?;
    let opened = fs::read_link(format!("/proc/self/fd/{}", file.as_raw_fd()))
        .map_err(|_| ReproFailure::Unavailable)?;
    if !opened.starts_with(root) {
        return Err(ReproFailure::ExternalLink);
    }
    let mut digest = Sha256::new();
    let mut length = 0_u64;
    let mut buffer = [0_u8; 65536];
    loop {
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
    Ok((format!("{:x}", digest.finalize()), length))
}

impl Snapshot {
    /// Copy all selected eligible files before running the original command.
    /// Only observed requirements are staged into the eventual candidate.
    pub fn take(
        root: &Path,
        selector: &Selector,
        max_bytes: u64,
        max_files: usize,
    ) -> Result<Self, ReproFailure> {
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
            root: root.clone(),
            private,
            eligible: BTreeSet::new(),
            files: BTreeMap::new(),
            links: BTreeMap::new(),
            directories: BTreeSet::new(),
            total_bytes: 0,
            max_bytes,
            max_files,
        };
        let mut external_selected = false;
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
                if denied(relative) {
                    return false;
                }
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
            if !entry.file_type().is_file() {
                continue;
            }
            let relative = entry
                .path()
                .strip_prefix(&root)
                .map_err(|_| ReproFailure::Unavailable)?;
            if !selector.matches(&native_relative(relative)) {
                continue;
            }
            if denied(relative) {
                continue;
            }
            snapshot.eligible.insert(relative.to_path_buf());
            snapshot.add_path(relative)?;
        }
        if external_selected {
            return Err(ReproFailure::ExternalLink);
        }
        Ok(snapshot)
    }

    fn claim_entry(&self) -> Result<(), ReproFailure> {
        if self.files.len() + self.links.len() + self.directories.len() >= self.max_files {
            Err(ReproFailure::FileLimit)
        } else {
            Ok(())
        }
    }

    fn add_path(&mut self, relative: &Path) -> Result<(), ReproFailure> {
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
                    self.links.insert(prefix.clone(), target.clone());
                }
                let suffix = relative
                    .strip_prefix(&prefix)
                    .map_err(|_| ReproFailure::Unavailable)?;
                let target_path = target.join(suffix);
                return self.add_path(&target_path);
            }
            if metadata.is_dir() {
                if !self.directories.contains(&prefix) {
                    self.claim_entry()?;
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
            let opened = fs::read_link(format!("/proc/self/fd/{}", input.as_raw_fd()))
                .map_err(|_| ReproFailure::Unavailable)?;
            if !opened.starts_with(&self.root) {
                return Err(ReproFailure::ExternalLink);
            }
            let mut output = File::create(&destination).map_err(|_| ReproFailure::Unavailable)?;
            let mut digest = Sha256::new();
            let mut size = 0_u64;
            let mut buffer = [0_u8; 65536];
            loop {
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
            if hash_file(&source, &self.root)? != (sha256.clone(), size) {
                return Err(ReproFailure::UnstableInput);
            }
            self.files.insert(
                prefix.clone(),
                SnapshotFile {
                    relative: prefix.clone(),
                    sha256,
                    size,
                },
            );
        }
        Ok(())
    }

    /// Recheck only collected source objects after the original execution.
    pub fn verify_required(&self, required: &BTreeSet<PathBuf>) -> Result<(), ReproFailure> {
        for relative in required {
            if !valid_relative(relative) {
                return Err(ReproFailure::UncollectedInput);
            }
            if denied(relative) {
                return Err(ReproFailure::BlockedInput);
            }
            if !self.eligible.contains(relative) {
                return Err(ReproFailure::UncollectedInput);
            }
            self.verify_path(relative)?;
        }
        Ok(())
    }

    fn verify_path(&self, relative: &Path) -> Result<(), ReproFailure> {
        let mut prefix = PathBuf::new();
        for component in relative.components() {
            prefix.push(component.as_os_str());
            if let Some(expected) = self.links.get(&prefix) {
                let actual = fs::canonicalize(self.root.join(&prefix))
                    .map_err(|_| ReproFailure::UnstableInput)?;
                let relative_target = actual
                    .strip_prefix(&self.root)
                    .map_err(|_| ReproFailure::UnstableInput)?;
                if relative_target != expected {
                    return Err(ReproFailure::UnstableInput);
                }
                let suffix = relative
                    .strip_prefix(&prefix)
                    .map_err(|_| ReproFailure::UnstableInput)?;
                return self.verify_path(&expected.join(suffix));
            }
        }
        let expected = self
            .files
            .get(relative)
            .ok_or(ReproFailure::UncollectedInput)?;
        let actual = hash_file(&self.root.join(relative), &self.root)
            .map_err(|_| ReproFailure::UnstableInput)?;
        if actual != (expected.sha256.clone(), expected.size) {
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
        for relative in required {
            self.stage_path(relative, candidate, &mut staged)?;
        }
        Ok(staged.into_values().collect())
    }

    fn stage_path(
        &self,
        relative: &Path,
        candidate: &Path,
        staged: &mut BTreeMap<PathBuf, SnapshotFile>,
    ) -> Result<(), ReproFailure> {
        let mut prefix = PathBuf::new();
        for component in relative.components() {
            prefix.push(component.as_os_str());
            if let Some(target) = self.links.get(&prefix) {
                let suffix = relative
                    .strip_prefix(&prefix)
                    .map_err(|_| ReproFailure::Unavailable)?;
                self.stage_path(&target.join(suffix), candidate, staged)?;
                let link = candidate.join(&prefix);
                if let Some(parent) = link.parent() {
                    fs::create_dir_all(parent).map_err(|_| ReproFailure::Unavailable)?;
                }
                if !link.is_symlink() {
                    let relative_target =
                        pathdiff::diff_paths(target, prefix.parent().unwrap_or(Path::new(".")))
                            .ok_or(ReproFailure::Unavailable)?;
                    symlink(relative_target, link).map_err(|_| ReproFailure::Unavailable)?;
                }
                return Ok(());
            }
        }
        let file = self
            .files
            .get(relative)
            .ok_or(ReproFailure::UncollectedInput)?;
        let destination = candidate.join(relative);
        if let Some(parent) = destination.parent() {
            fs::create_dir_all(parent).map_err(|_| ReproFailure::Unavailable)?;
        }
        fs::copy(self.private.path().join(relative), &destination)
            .map_err(|_| ReproFailure::Unavailable)?;
        staged.insert(relative.to_path_buf(), file.clone());
        Ok(())
    }
}

fn native_relative(path: &Path) -> crate::record::NativePath {
    use std::os::unix::ffi::OsStrExt;
    crate::record::NativePath::UnixBytes(path.as_os_str().as_bytes().to_vec())
}

#[cfg(test)]
mod tests {
    use super::*;

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
}
