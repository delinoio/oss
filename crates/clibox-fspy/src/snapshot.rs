use std::{
    collections::{BTreeMap, BTreeSet},
    ffi::OsString,
    fs::{self, File},
    io::{self, Read as _, Write as _},
    os::unix::{ffi::OsStrExt as _, fs::symlink},
    path::{Component, Path, PathBuf},
};

use sha2::{Digest as _, Sha256};

use crate::selector::Selector;

#[derive(Debug)]
pub enum SnapshotError {
    Blocked,
    EscapingLink,
    Unavailable,
    Unstable,
    ResourceLimit,
    UnsafeLocation,
    Io(io::Error),
}

impl From<io::Error> for SnapshotError {
    fn from(error: io::Error) -> Self {
        Self::Io(error)
    }
}

impl SnapshotError {
    pub fn classification(&self) -> &'static str {
        match self {
            Self::Blocked => "blocked_input",
            Self::EscapingLink => "escaping_symlink",
            Self::Unavailable => "required_input_unavailable",
            Self::Unstable => "unstable_input",
            Self::ResourceLimit => "reproduction_limit",
            Self::UnsafeLocation => "snapshot_location_unavailable",
            Self::Io(error) if error.kind() == io::ErrorKind::PermissionDenied => {
                "reproduction_permission_denied"
            }
            Self::Io(_) => "reproduction_io",
        }
    }
}

#[derive(Clone)]
pub struct SnapshotFile {
    pub sha256: String,
    pub size: u64,
}

pub struct Snapshot {
    root: PathBuf,
    private: tempfile::TempDir,
    files: BTreeMap<PathBuf, SnapshotFile>,
    links: BTreeMap<PathBuf, PathBuf>,
    max_bytes: u64,
    max_files: usize,
    bytes: u64,
}

pub struct Resolution {
    pub final_path: PathBuf,
    pub links: Vec<(PathBuf, PathBuf)>,
}

impl Snapshot {
    pub fn create(
        selector: &Selector,
        max_bytes: u64,
        max_files: usize,
    ) -> Result<Self, SnapshotError> {
        let private_parent = [
            Some(std::env::temp_dir()),
            Some(PathBuf::from("/var/tmp")),
            std::env::var_os("HOME").map(PathBuf::from),
        ]
        .into_iter()
        .flatten()
        .filter_map(|path| fs::canonicalize(path).ok())
        .find(|path| path.is_dir() && !path.starts_with(selector.root()))
        .ok_or(SnapshotError::UnsafeLocation)?;
        let selected = selector.selected_files()?;
        let private = tempfile::Builder::new()
            .prefix("clibox-fspy-snapshot-")
            .tempdir_in(private_parent)?;
        let mut snapshot = Self {
            root: selector.root().to_path_buf(),
            private,
            files: BTreeMap::new(),
            links: BTreeMap::new(),
            max_bytes,
            max_files,
            bytes: 0,
        };
        for selected in selected {
            match snapshot.add_selected(&selected.logical) {
                Ok(()) | Err(SnapshotError::Blocked | SnapshotError::EscapingLink) => {}
                Err(error) => return Err(error),
            }
        }
        snapshot.materialize_links()?;
        Ok(snapshot)
    }

    pub const fn files(&self) -> &BTreeMap<PathBuf, SnapshotFile> {
        &self.files
    }

    pub const fn links(&self) -> &BTreeMap<PathBuf, PathBuf> {
        &self.links
    }

    fn add_selected(&mut self, relative: &Path) -> Result<(), SnapshotError> {
        if blocked(relative) {
            return Err(SnapshotError::Blocked);
        }
        let resolved = resolve(&self.root, relative)?;
        if blocked(&resolved.final_path) || resolved.links.iter().any(|(link, _)| blocked(link)) {
            return Err(SnapshotError::Blocked);
        }
        for (link, target) in resolved.links {
            self.links.insert(link, target);
        }
        if self.files.contains_key(&resolved.final_path) {
            return Ok(());
        }
        let source = self.root.join(&resolved.final_path);
        let metadata = fs::metadata(&source)?;
        if !metadata.is_file() {
            return Err(SnapshotError::Unavailable);
        }
        if self.files.len().saturating_add(self.links.len()) >= self.max_files
            || self.bytes.saturating_add(metadata.len()) > self.max_bytes
        {
            return Err(SnapshotError::ResourceLimit);
        }
        let destination = self.private.path().join(&resolved.final_path);
        if let Some(parent) = destination.parent() {
            fs::create_dir_all(parent)?;
        }
        let (size, sha256) = copy_with_hash(&source, &destination, self.max_bytes - self.bytes)?;
        self.bytes = self.bytes.saturating_add(size);
        self.files
            .insert(resolved.final_path, SnapshotFile { sha256, size });
        Ok(())
    }

    fn materialize_links(&self) -> Result<(), SnapshotError> {
        for (link, target) in &self.links {
            let path = self.private.path().join(link);
            if let Some(parent) = path.parent() {
                fs::create_dir_all(parent)?;
            }
            if self.files.len().saturating_add(self.links.len()) > self.max_files {
                return Err(SnapshotError::ResourceLimit);
            }
            symlink(target, path)?;
        }
        Ok(())
    }

    pub fn verify_required(&self, relative: &Path) -> Result<Resolution, SnapshotError> {
        if blocked(relative) {
            return Err(SnapshotError::Blocked);
        }
        let current = resolve(&self.root, relative)?;
        if blocked(&current.final_path) {
            return Err(SnapshotError::Blocked);
        }
        for (link, target) in &current.links {
            if self.links.get(link) != Some(target) {
                return Err(SnapshotError::Unstable);
            }
        }
        if let Some(expected) = self.files.get(&current.final_path) {
            let source = self.root.join(&current.final_path);
            let (size, sha256) = hash_file(&source, self.max_bytes)?;
            if size != expected.size || sha256 != expected.sha256 {
                return Err(SnapshotError::Unstable);
            }
        } else if !self.root.join(&current.final_path).is_dir() {
            return Err(SnapshotError::Unavailable);
        }
        Ok(current)
    }

    pub fn collect(
        &self,
        relative: &Path,
        destination: &Path,
        files: &mut BTreeSet<PathBuf>,
        links: &mut BTreeSet<PathBuf>,
    ) -> Result<Resolution, SnapshotError> {
        let resolved = self.verify_required(relative)?;
        for (link, _) in &resolved.links {
            if links.insert(link.clone()) {
                let source = self.private.path().join(link);
                let target = destination.join(link);
                if let Some(parent) = target.parent() {
                    fs::create_dir_all(parent)?;
                }
                symlink(fs::read_link(source)?, target)?;
            }
        }
        if let Some(_expected) = self.files.get(&resolved.final_path) {
            if files.insert(resolved.final_path.clone()) {
                let source = self.private.path().join(&resolved.final_path);
                let target = destination.join(&resolved.final_path);
                if let Some(parent) = target.parent() {
                    fs::create_dir_all(parent)?;
                }
                fs::copy(source, target)?;
            }
        } else {
            fs::create_dir_all(destination.join(&resolved.final_path))?;
        }
        Ok(resolved)
    }
}

fn resolve(root: &Path, relative: &Path) -> Result<Resolution, SnapshotError> {
    if relative.is_absolute()
        || relative
            .components()
            .any(|part| matches!(part, Component::ParentDir | Component::Prefix(_)))
    {
        return Err(SnapshotError::EscapingLink);
    }
    let mut current = relative.to_path_buf();
    let mut links = Vec::new();
    for _ in 0..40 {
        let components: Vec<OsString> = current
            .components()
            .filter_map(|part| match part {
                Component::Normal(value) => Some(value.to_os_string()),
                _ => None,
            })
            .collect();
        let mut prefix = PathBuf::new();
        let mut replacement = None;
        for (index, component) in components.iter().enumerate() {
            prefix.push(component);
            let metadata = fs::symlink_metadata(root.join(&prefix)).map_err(|error| {
                if error.kind() == io::ErrorKind::NotFound {
                    SnapshotError::Unavailable
                } else {
                    SnapshotError::Io(error)
                }
            })?;
            if metadata.file_type().is_symlink() {
                let target = fs::read_link(root.join(&prefix))?;
                let absolute = if target.is_absolute() {
                    normalize(&target)
                } else {
                    normalize(
                        &root
                            .join(prefix.parent().unwrap_or_else(|| Path::new("")))
                            .join(&target),
                    )
                };
                let target_relative = absolute
                    .strip_prefix(root)
                    .map_err(|_| SnapshotError::EscapingLink)?
                    .to_path_buf();
                if blocked(&prefix) || blocked(&target_relative) {
                    return Err(SnapshotError::Blocked);
                }
                let stored_target = if target.is_absolute() {
                    relative_between(
                        prefix.parent().unwrap_or_else(|| Path::new("")),
                        &target_relative,
                    )
                } else {
                    target
                };
                links.push((prefix.clone(), stored_target));
                let mut next = target_relative;
                for suffix in &components[index + 1..] {
                    next.push(suffix);
                }
                replacement = Some(next);
                break;
            }
        }
        if let Some(next) = replacement {
            current = next;
        } else {
            return Ok(Resolution {
                final_path: current,
                links,
            });
        }
    }
    Err(SnapshotError::Unavailable)
}

fn relative_between(from: &Path, to: &Path) -> PathBuf {
    let from: Vec<_> = from.components().collect();
    let to: Vec<_> = to.components().collect();
    let common = from
        .iter()
        .zip(&to)
        .take_while(|(left, right)| left == right)
        .count();
    let mut path = PathBuf::new();
    for _ in common..from.len() {
        path.push("..");
    }
    for component in &to[common..] {
        path.push(component.as_os_str());
    }
    if path.as_os_str().is_empty() {
        PathBuf::from(".")
    } else {
        path
    }
}

fn normalize(path: &Path) -> PathBuf {
    let mut result = PathBuf::new();
    for part in path.components() {
        match part {
            Component::ParentDir => {
                result.pop();
            }
            Component::CurDir => {}
            other => result.push(other.as_os_str()),
        }
    }
    result
}

pub fn blocked(path: &Path) -> bool {
    for part in path.components() {
        let Component::Normal(name) = part else {
            continue;
        };
        let bytes = name.as_bytes();
        if bytes == b".git" || bytes == b".ssh" {
            return true;
        }
    }
    let Some(name) = path.file_name() else {
        return false;
    };
    let name = name.as_bytes();
    if name == b".env"
        || name.starts_with(b".env.")
        || [b"id_rsa".as_slice(), b"id_dsa", b"id_ecdsa", b"id_ed25519"].contains(&name)
    {
        return true;
    }
    [b".pem".as_slice(), b".key", b".p12", b".pfx"]
        .iter()
        .any(|suffix| name.ends_with(suffix))
}

fn copy_with_hash(
    source: &Path,
    destination: &Path,
    max_bytes: u64,
) -> Result<(u64, String), SnapshotError> {
    let mut input = File::open(source)?;
    let mut output = File::create(destination)?;
    let mut digest = Sha256::new();
    let mut total = 0u64;
    let mut buffer = [0u8; 8192];
    loop {
        let size = input.read(&mut buffer)?;
        if size == 0 {
            break;
        }
        total = total
            .checked_add(u64::try_from(size).unwrap())
            .ok_or(SnapshotError::ResourceLimit)?;
        if total > max_bytes {
            return Err(SnapshotError::ResourceLimit);
        }
        digest.update(&buffer[..size]);
        output.write_all(&buffer[..size])?;
    }
    output.sync_all()?;
    fs::set_permissions(destination, fs::metadata(source)?.permissions())?;
    Ok((total, format!("{:x}", digest.finalize())))
}

fn hash_file(source: &Path, max_bytes: u64) -> Result<(u64, String), SnapshotError> {
    let mut input = File::open(source)?;
    let mut digest = Sha256::new();
    let mut total = 0u64;
    let mut buffer = [0u8; 8192];
    loop {
        let size = input.read(&mut buffer)?;
        if size == 0 {
            break;
        }
        total = total
            .checked_add(u64::try_from(size).unwrap())
            .ok_or(SnapshotError::ResourceLimit)?;
        if total > max_bytes {
            return Err(SnapshotError::ResourceLimit);
        }
        digest.update(&buffer[..size]);
    }
    Ok((total, format!("{:x}", digest.finalize())))
}

pub fn file_matches(path: &Path, expected: &SnapshotFile) -> bool {
    hash_file(path, expected.size)
        .is_ok_and(|(size, hash)| size == expected.size && hash == expected.sha256)
}

#[cfg(test)]
mod tests {
    use std::{fs, os::unix::fs::symlink};

    use super::{Snapshot, SnapshotError, blocked};
    use crate::selector::Selector;

    #[test]
    fn blocks_sensitive_paths_and_preserves_internal_symlink() {
        let root = tempfile::tempdir().unwrap();
        fs::create_dir(root.path().join("assets")).unwrap();
        fs::write(root.path().join("assets/a"), b"hello").unwrap();
        fs::write(root.path().join("assets/.env"), b"secret").unwrap();
        symlink("assets", root.path().join("alias")).unwrap();
        let selector = Selector::new(
            root.path(),
            &["alias/**".to_owned(), "assets/**".to_owned()],
            &[],
        )
        .unwrap();
        let snapshot = Snapshot::create(&selector, 1024, 100).unwrap();
        assert!(
            snapshot
                .files()
                .contains_key(std::path::Path::new("assets/a"))
        );
        assert!(snapshot.links().contains_key(std::path::Path::new("alias")));
        assert!(
            !snapshot
                .files()
                .contains_key(std::path::Path::new("assets/.env"))
        );
        let candidate = tempfile::tempdir().unwrap();
        let mut files = std::collections::BTreeSet::default();
        let mut links = std::collections::BTreeSet::default();
        snapshot
            .collect(
                std::path::Path::new("alias/a"),
                candidate.path(),
                &mut files,
                &mut links,
            )
            .unwrap();
        assert_eq!(
            fs::read(candidate.path().join("alias/a")).unwrap(),
            b"hello"
        );
        assert!(blocked(std::path::Path::new("src/.ssh/key")));
        assert!(matches!(
            snapshot.collect(
                std::path::Path::new("alias/.env"),
                candidate.path(),
                &mut files,
                &mut links
            ),
            Err(SnapshotError::Blocked)
        ));
    }
}
