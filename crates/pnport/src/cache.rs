use std::{
    collections::BTreeMap,
    fs::{self, File, OpenOptions},
    io::{Read, Seek, SeekFrom, Write},
    path::{Component, Path, PathBuf},
};

use fs2::FileExt;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use crate::{
    diagnostic::{archive_error, cache_error, Result},
    graph::digest,
};

const OWNER: &[u8] = b"pnport cache\n1\n";
pub const FORMAT: &str = "v1";

#[derive(Clone)]
pub struct Cache {
    pub root: PathBuf,
}
pub struct Lease {
    pub content: PathBuf,
    pub archive: PathBuf,
    pub sha256: String,
    _lock: File,
}
#[derive(Serialize, Debug)]
#[serde(rename_all = "kebab-case")]
pub enum State {
    Complete,
    Corrupt,
    Active,
    Incomplete,
    Unknown,
    Removed,
    Failed,
}
#[derive(Serialize, Debug)]
pub struct Entry {
    pub name: String,
    pub state: State,
}
#[derive(Clone, Copy)]
pub enum Operation {
    List,
    Prune,
    Clean,
}

#[derive(Deserialize, Serialize, Eq, PartialEq)]
#[serde(tag = "kind", rename_all = "kebab-case")]
enum Item {
    Directory,
    File { sha256: String, executable: bool },
    Symlink { target: PathBuf },
}
#[derive(Deserialize, Serialize)]
struct Receipt {
    owner: String,
    cleanup_version: u32,
    format: String,
    sha256: String,
    items: BTreeMap<PathBuf, Item>,
}

pub fn default_path() -> Result<PathBuf> {
    dirs::cache_dir()
        .map(|p| p.join("pnport"))
        .ok_or_else(cache_error)
}

pub fn private_dir(path: &Path) -> Result<()> {
    if !path.exists() {
        let mut builder = fs::DirBuilder::new();
        builder.recursive(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::DirBuilderExt;
            builder.mode(0o700);
        }
        builder.create(path).map_err(|_| cache_error())?;
    }
    let meta = fs::symlink_metadata(path).map_err(|_| cache_error())?;
    if !meta.is_dir() || meta.file_type().is_symlink() {
        return Err(cache_error());
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        if meta.uid() != unsafe { libc::geteuid() } || meta.mode() & 0o077 != 0 {
            return Err(cache_error());
        }
    }
    Ok(())
}

fn private_file(path: &Path) -> Result<File> {
    let mut options = OpenOptions::new();
    options.read(true).write(true).create(true).truncate(false);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options
            .mode(0o600)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC);
    }
    let file = options.open(path).map_err(|_| cache_error())?;
    let meta = file.metadata().map_err(|_| cache_error())?;
    if !meta.is_file() {
        return Err(cache_error());
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        if meta.uid() != unsafe { libc::geteuid() } || meta.mode() & 0o077 != 0 || meta.nlink() != 1
        {
            return Err(cache_error());
        }
    }
    Ok(file)
}

impl Cache {
    pub fn open(root: PathBuf) -> Result<Self> {
        private_dir(&root)?;
        let lock = private_file(&root.join(".lock"))?;
        lock.lock_exclusive().map_err(|_| cache_error())?;
        let owner = root.join(".pnport-owner");
        match fs::read(&owner) {
            Ok(bytes) if bytes == OWNER => {}
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                // Refuse to adopt a directory with unrelated data. Never repurpose
                // an arbitrary user-supplied cache directory as owned storage.
                if fs::read_dir(&root)
                    .map_err(|_| cache_error())?
                    .any(|e| e.map_or(true, |e| e.file_name() != ".lock"))
                {
                    return Err(cache_error());
                }
                let mut file = private_file(&owner)?;
                file.write_all(OWNER)
                    .and_then(|_| file.sync_all())
                    .map_err(|_| cache_error())?;
            }
            _ => return Err(cache_error()),
        }
        private_dir(&root.join(FORMAT))?;
        let marker = root.join(FORMAT).join(".pnport-format");
        let expected = format!("pnport cache format\n{FORMAT}\n1\n");
        let mut file = private_file(&marker)?;
        if file.metadata().map_err(|_| cache_error())?.len() == 0 {
            file.write_all(expected.as_bytes())
                .and_then(|_| file.sync_all())
                .map_err(|_| cache_error())?;
        } else if fs::read(&marker).map_err(|_| cache_error())? != expected.as_bytes() {
            return Err(cache_error());
        }
        private_dir(&root.join("incomplete"))?;
        Ok(Self { root })
    }

    fn lock(&self) -> Result<File> {
        let file = private_file(&self.root.join(".lock"))?;
        file.lock_exclusive().map_err(|_| cache_error())?;
        Ok(file)
    }

    pub fn materialize(&self, archive: &Path) -> Result<Lease> {
        let mut file = File::open(archive).map_err(|_| archive_error())?;
        let mut hasher = Sha256::new();
        std::io::copy(&mut file, &mut hasher).map_err(|_| archive_error())?;
        let identity = format!("{:x}", hasher.finalize());
        let _guard = self.lock()?;
        let destination = self.root.join(FORMAT).join(&identity);
        if !destination.exists() {
            let stage = tempfile::Builder::new()
                .prefix("entry-")
                .tempdir_in(self.root.join("incomplete"))
                .map_err(|_| cache_error())?;
            let mut owner = private_file(&stage.path().join(".pnport-staging"))?;
            owner
                .write_all(OWNER)
                .and_then(|_| owner.sync_all())
                .map_err(|_| cache_error())?;
            let content = stage.path().join("content");
            fs::create_dir(&content).map_err(|_| cache_error())?;
            // A package manager can rewrite the open archive between hashing
            // and extraction. Extract only the private bytes whose digest was
            // checked, so even a change-and-restore race cannot poison a key.
            let snapshot = snapshot_archive(&mut file, stage.path(), &identity)?;
            extract(snapshot, &content)?;
            let receipt = Receipt {
                owner: "pnport".into(),
                cleanup_version: 1,
                format: FORMAT.into(),
                sha256: identity.clone(),
                items: inventory(&content)?,
            };
            let bytes = serde_json::to_vec(&receipt).map_err(|_| cache_error())?;
            let mut output = private_file(&stage.path().join("receipt.json"))?;
            output
                .write_all(&bytes)
                .and_then(|_| output.sync_all())
                .map_err(|_| cache_error())?;
            private_file(&stage.path().join("lease"))?;
            readonly_tree(&content)?;
            fs::rename(stage.path(), &destination).map_err(|_| cache_error())?;
            #[cfg(unix)]
            File::open(self.root.join(FORMAT))
                .and_then(|f| f.sync_all())
                .map_err(|_| cache_error())?;
            tracing::debug!(action = "cache_publish", identity = %identity);
        }
        verify(&destination, &identity, FORMAT)?;
        let lease = private_file(&destination.join("lease"))?;
        lease.lock_shared().map_err(|_| cache_error())?;
        tracing::debug!(action = "cache_lease", identity = %identity);
        Ok(Lease {
            content: destination.join("content"),
            archive: archive.to_path_buf(),
            sha256: identity,
            _lock: lease,
        })
    }

    pub fn entries(&self, operation: Operation) -> Result<Vec<Entry>> {
        let _guard = self.lock()?;
        let mut result = vec![];
        for namespace in fs::read_dir(&self.root).map_err(|_| cache_error())? {
            let namespace = namespace.map_err(|_| cache_error())?;
            let name = namespace.file_name().to_string_lossy().into_owned();
            if matches!(name.as_str(), ".lock" | ".pnport-owner") {
                continue;
            }
            if !namespace.file_type().map_err(|_| cache_error())?.is_dir()
                || (name != "incomplete"
                    && (!name
                        .strip_prefix('v')
                        .is_some_and(|n| !n.is_empty() && n.bytes().all(|c| c.is_ascii_digit()))
                        || fs::read(namespace.path().join(".pnport-format"))
                            .ok()
                            .as_deref()
                            != Some(format!("pnport cache format\n{name}\n1\n").as_bytes())))
            {
                result.push(Entry {
                    name,
                    state: State::Unknown,
                });
                continue;
            }
            for entry in fs::read_dir(namespace.path()).map_err(|_| cache_error())? {
                let entry = entry.map_err(|_| cache_error())?;
                let entry_name = entry.file_name().to_string_lossy().into_owned();
                if entry_name == ".pnport-format" {
                    continue;
                }
                let label = format!("{name}/{entry_name}");
                if !entry.file_type().map_err(|_| cache_error())?.is_dir() {
                    result.push(Entry {
                        name: label,
                        state: State::Unknown,
                    });
                    continue;
                }
                if name == "incomplete" {
                    // All materializers retain the global lock until publication;
                    // therefore staging directories observed here are abandoned.
                    // Only tempfile's exact owned prefix is eligible for removal.
                    let owned = entry_name.starts_with("entry-")
                        && fs::read(entry.path().join(".pnport-staging"))
                            .ok()
                            .as_deref()
                            == Some(OWNER);
                    let state = if !owned {
                        State::Unknown
                    } else if matches!(operation, Operation::List) {
                        State::Incomplete
                    } else {
                        remove_state(&entry.path())
                    };
                    result.push(Entry { name: label, state });
                    continue;
                }
                let owned = entry_name.len() == 64
                    && entry_name
                        .bytes()
                        .all(|b| b.is_ascii_hexdigit() && !b.is_ascii_uppercase())
                    && receipt(&entry.path(), &entry_name, &name).is_ok();
                if !owned {
                    result.push(Entry {
                        name: label,
                        state: State::Unknown,
                    });
                    continue;
                }
                let lease = private_file(&entry.path().join("lease"))?;
                let state = match lease.try_lock_exclusive() {
                    Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => State::Active,
                    Err(_) => State::Failed,
                    Ok(())
                        if (matches!(operation, Operation::Clean)
                            || matches!(operation, Operation::Prune) && name != FORMAT) =>
                    {
                        remove_state(&entry.path())
                    }
                    Ok(()) if verify(&entry.path(), &entry_name, &name).is_err() => State::Corrupt,
                    Ok(()) => State::Complete,
                };
                result.push(Entry { name: label, state });
            }
        }
        result.sort_by(|a, b| a.name.cmp(&b.name));
        Ok(result)
    }
}

fn remove_state(path: &Path) -> State {
    if writable_directories(path)
        .and_then(|_| fs::remove_dir_all(path).map_err(|_| cache_error()))
        .is_ok()
    {
        State::Removed
    } else {
        State::Failed
    }
}
fn writable_directories(path: &Path) -> Result<()> {
    let meta = fs::symlink_metadata(path).map_err(|_| cache_error())?;
    if meta.is_dir() {
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(path, fs::Permissions::from_mode(0o700))
                .map_err(|_| cache_error())?;
        }
        for item in fs::read_dir(path).map_err(|_| cache_error())? {
            writable_directories(&item.map_err(|_| cache_error())?.path())?;
        }
    }
    #[cfg(windows)]
    #[allow(
        clippy::permissions_set_readonly_false,
        reason = "Windows-only code clears FILE_ATTRIBUTE_READONLY; Unix mode changes use \
                  PermissionsExt"
    )]
    if meta.is_file() {
        let mut permissions = meta.permissions();
        permissions.set_readonly(false);
        fs::set_permissions(path, permissions).map_err(|_| cache_error())?;
    }
    Ok(())
}
fn readonly_tree(path: &Path) -> Result<()> {
    let meta = fs::symlink_metadata(path).map_err(|_| cache_error())?;
    if meta.file_type().is_symlink() {
        return Ok(());
    }
    if meta.is_dir() {
        for entry in fs::read_dir(path).map_err(|_| cache_error())? {
            readonly_tree(&entry.map_err(|_| cache_error())?.path())?;
        }
    }
    let mut mode = meta.permissions();
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        mode.set_mode(if meta.is_dir() || mode.mode() & 0o111 != 0 {
            0o500
        } else {
            0o400
        });
    }
    #[cfg(windows)]
    #[allow(
        clippy::permissions_set_readonly_false,
        reason = "Windows-only code clears FILE_ATTRIBUTE_READONLY; Unix mode changes use \
                  PermissionsExt"
    )]
    mode.set_readonly(true);
    fs::set_permissions(path, mode).map_err(|_| cache_error())
}
fn verify(path: &Path, identity: &str, format: &str) -> Result<()> {
    let receipt = receipt(path, identity, format)?;
    if receipt.items != inventory(&path.join("content"))? {
        return Err(cache_error());
    }
    Ok(())
}

fn receipt(path: &Path, identity: &str, format: &str) -> Result<Receipt> {
    if fs::symlink_metadata(path)
        .map_err(|_| cache_error())?
        .file_type()
        .is_symlink()
    {
        return Err(cache_error());
    }
    for name in ["receipt.json", "lease", "content"] {
        if fs::symlink_metadata(path.join(name))
            .map_err(|_| cache_error())?
            .file_type()
            .is_symlink()
        {
            return Err(cache_error());
        }
    }
    let receipt: Receipt =
        serde_json::from_slice(&fs::read(path.join("receipt.json")).map_err(|_| cache_error())?)
            .map_err(|_| cache_error())?;
    if receipt.owner != "pnport"
        || receipt.cleanup_version != 1
        || receipt.format != format
        || receipt.sha256 != identity
    {
        return Err(cache_error());
    }
    Ok(receipt)
}
fn inventory(root: &Path) -> Result<BTreeMap<PathBuf, Item>> {
    fn visit(root: &Path, path: &Path, items: &mut BTreeMap<PathBuf, Item>) -> Result<()> {
        for entry in fs::read_dir(path).map_err(|_| cache_error())? {
            let entry = entry.map_err(|_| cache_error())?;
            let path = entry.path();
            let relative = path
                .strip_prefix(root)
                .map_err(|_| cache_error())?
                .to_path_buf();
            let meta = fs::symlink_metadata(&path).map_err(|_| cache_error())?;
            let item = if meta.file_type().is_symlink() {
                confined_link(root, &path)?;
                Item::Symlink {
                    target: fs::read_link(&path).map_err(|_| cache_error())?,
                }
            } else if meta.is_dir() {
                visit(root, &path, items)?;
                Item::Directory
            } else if meta.is_file() {
                let mut file = File::open(&path).map_err(|_| cache_error())?;
                let mut hash = Sha256::new();
                std::io::copy(&mut file, &mut hash).map_err(|_| cache_error())?;
                #[cfg(unix)]
                let executable = {
                    use std::os::unix::fs::PermissionsExt;
                    meta.permissions().mode() & 0o111 != 0
                };
                #[cfg(windows)]
                #[allow(
                    clippy::permissions_set_readonly_false,
                    reason = "Windows-only code clears FILE_ATTRIBUTE_READONLY; Unix mode changes \
                              use PermissionsExt"
                )]
                let executable = false;
                Item::File {
                    sha256: format!("{:x}", hash.finalize()),
                    executable,
                }
            } else {
                return Err(archive_error());
            };
            items.insert(relative, item);
        }
        Ok(())
    }
    let mut items = BTreeMap::new();
    visit(root, root, &mut items)?;
    Ok(items)
}

fn confined_link(root: &Path, link: &Path) -> Result<()> {
    let canonical_root = fs::canonicalize(root).map_err(|_| archive_error())?;
    let relative = link.strip_prefix(root).map_err(|_| archive_error())?;
    let mut target = canonical_root.join(relative);
    for _ in 0..40 {
        let mut current = canonical_root.clone();
        let mut followed = false;
        let components: Vec<_> = target
            .strip_prefix(&canonical_root)
            .map_err(|_| archive_error())?
            .components()
            .map(|component| component.as_os_str().to_os_string())
            .collect();
        for component in components {
            current.push(component);
            match fs::symlink_metadata(&current) {
                Ok(metadata) if metadata.file_type().is_symlink() => {
                    let remainder = target
                        .strip_prefix(&current)
                        .map_err(|_| archive_error())?
                        .to_path_buf();
                    let destination = fs::read_link(&current).map_err(|_| archive_error())?;
                    target = crate::graph::normalize(
                        &current
                            .parent()
                            .ok_or_else(archive_error)?
                            .join(destination)
                            .join(remainder),
                    );
                    if !target.starts_with(&canonical_root) {
                        return Err(archive_error());
                    }
                    followed = true;
                    break;
                }
                Ok(_) => {}
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(()),
                Err(_) => return Err(archive_error()),
            }
        }
        if !followed {
            return Ok(());
        }
    }
    Err(archive_error())
}
fn safe_relative(name: &str) -> Result<PathBuf> {
    if name.is_empty()
        || name.contains(['\\', ':', '\0'])
        || name.split('/').any(|p| p == ".." || p == ".")
    {
        return Err(archive_error());
    }
    let path = PathBuf::from(name);
    if path.is_absolute() || !path.components().all(|p| matches!(p, Component::Normal(_))) {
        return Err(archive_error());
    }
    Ok(path)
}
fn snapshot_archive(source: &mut File, directory: &Path, identity: &str) -> Result<File> {
    source
        .seek(SeekFrom::Start(0))
        .map_err(|_| archive_error())?;
    let mut snapshot = tempfile::tempfile_in(directory).map_err(|_| cache_error())?;
    let mut hasher = Sha256::new();
    let mut buffer = [0u8; 64 * 1024];
    loop {
        let count = source.read(&mut buffer).map_err(|_| archive_error())?;
        if count == 0 {
            break;
        }
        snapshot
            .write_all(&buffer[..count])
            .map_err(|_| cache_error())?;
        hasher.update(&buffer[..count]);
    }
    if format!("{:x}", hasher.finalize()) != identity {
        tracing::debug!(
            action = "archive_changed",
            "Archive changed before extraction; refusing cache publication"
        );
        return Err(archive_error());
    }
    snapshot
        .seek(SeekFrom::Start(0))
        .map_err(|_| cache_error())?;
    Ok(snapshot)
}

fn extract(file: File, destination: &Path) -> Result<()> {
    let mut archive = zip::ZipArchive::new(file).map_err(|_| archive_error())?;
    let mut links = vec![];
    let mut seen = BTreeMap::new();
    for i in 0..archive.len() {
        let mut entry = archive.by_index(i).map_err(|_| archive_error())?;
        let relative = safe_relative(entry.name().trim_end_matches('/'))?;
        if seen.insert(relative.clone(), ()).is_some() {
            return Err(archive_error());
        }
        let path = destination.join(&relative);
        let mode = entry.unix_mode().unwrap_or(0o100644);
        match mode & 0o170000 {
            0 | 0o100000 | 0o040000 | 0o120000 => {}
            _ => return Err(archive_error()),
        }
        if entry.is_symlink() {
            if entry.size() > 65536 {
                return Err(archive_error());
            }
            let mut target = String::new();
            entry
                .read_to_string(&mut target)
                .map_err(|_| archive_error())?;
            let target_path = Path::new(&target);
            if target_path.is_absolute() || target.contains(['\\', ':', '\0']) {
                return Err(archive_error());
            }
            let normalized =
                crate::graph::normalize(&path.parent().ok_or_else(archive_error)?.join(&target));
            if !normalized.starts_with(destination) {
                return Err(archive_error());
            }
            links.push((path, target));
        } else if entry.is_dir() {
            fs::create_dir_all(&path).map_err(|_| archive_error())?;
        } else {
            fs::create_dir_all(path.parent().ok_or_else(archive_error)?)
                .map_err(|_| cache_error())?;
            let mut file = OpenOptions::new()
                .create_new(true)
                .write(true)
                .open(&path)
                .map_err(|_| archive_error())?;
            let written = std::io::copy(&mut entry, &mut file).map_err(|_| archive_error())?;
            if written != entry.size() {
                return Err(archive_error());
            }
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                file.set_permissions(fs::Permissions::from_mode(if mode & 0o111 != 0 {
                    0o700
                } else {
                    0o600
                }))
                .map_err(|_| cache_error())?;
            }
            file.sync_all().map_err(|_| cache_error())?;
        }
    }
    // Create links last so no archive-provided link can redirect extraction.
    for (path, target) in links {
        fs::create_dir_all(path.parent().ok_or_else(archive_error)?).map_err(|_| cache_error())?;
        #[cfg(unix)]
        std::os::unix::fs::symlink(&target, &path).map_err(|_| archive_error())?;
        #[cfg(windows)]
        #[allow(
            clippy::permissions_set_readonly_false,
            reason = "Windows-only code clears FILE_ATTRIBUTE_READONLY; Unix mode changes use \
                      PermissionsExt"
        )]
        {
            let target_path = path.parent().ok_or_else(archive_error)?.join(&target);
            if target_path.is_dir() {
                std::os::windows::fs::symlink_dir(&target, &path)
            } else {
                std::os::windows::fs::symlink_file(&target, &path)
            }
            .map_err(|_| archive_error())?;
        }
    }
    inventory(destination)?;
    Ok(())
}

pub fn archive_identity(path: &Path) -> Result<String> {
    let mut hash = Sha256::new();
    std::io::copy(
        &mut File::open(path).map_err(|_| archive_error())?,
        &mut hash,
    )
    .map_err(|_| archive_error())?;
    Ok(format!("{:x}", hash.finalize()))
}

pub fn path_identity(path: &Path) -> String {
    digest(path.to_string_lossy().as_bytes())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[cfg(unix)]
    #[test]
    fn dangling_link_with_escaping_existing_prefix_is_rejected() {
        let directory = tempfile::tempdir().unwrap();
        let content = directory.path().join("content");
        let outside = directory.path().join("outside");
        fs::create_dir(&content).unwrap();
        fs::create_dir(&outside).unwrap();
        std::os::unix::fs::symlink(&outside, content.join("redirect")).unwrap();
        std::os::unix::fs::symlink("redirect/missing", content.join("link")).unwrap();
        assert!(confined_link(&content, &content.join("link")).is_err());
    }

    fn archive_bytes(contents: &[u8]) -> Vec<u8> {
        let mut archive = zip::ZipWriter::new(std::io::Cursor::new(Vec::new()));
        archive
            .start_file(
                "node_modules/dep/file.txt",
                zip::write::SimpleFileOptions::default(),
            )
            .unwrap();
        archive.write_all(contents).unwrap();
        archive.finish().unwrap().into_inner()
    }

    #[test]
    fn rewritten_archive_cannot_be_extracted_under_the_original_digest() {
        let root = tempfile::tempdir().unwrap();
        let archive = root.path().join("source.zip");
        let original = archive_bytes(b"original");
        fs::write(&archive, &original).unwrap();
        let mut source = File::open(&archive).unwrap();
        let identity = archive_identity(&archive).unwrap();
        // Rewrite the same inode after the identity read, as a concurrent
        // package manager could do while the materializer waits for its lock.
        fs::write(&archive, archive_bytes(b"modified")).unwrap();
        let staging = tempfile::tempdir().unwrap();
        assert!(snapshot_archive(&mut source, staging.path(), &identity).is_err());
        assert_eq!(fs::read_dir(staging.path()).unwrap().count(), 0);
    }

    #[test]
    fn archive_rewrite_and_restore_cannot_change_the_extraction_snapshot() {
        let root = tempfile::tempdir().unwrap();
        let archive = root.path().join("source.zip");
        let original = archive_bytes(b"original");
        fs::write(&archive, &original).unwrap();
        let mut source = File::open(&archive).unwrap();
        let staging = tempfile::tempdir().unwrap();
        let mut snapshot =
            snapshot_archive(&mut source, staging.path(), &digest(&original)).unwrap();
        fs::write(&archive, archive_bytes(b"modified")).unwrap();
        let content = staging.path().join("content");
        fs::create_dir(&content).unwrap();
        let mut bytes = Vec::new();
        snapshot.read_to_end(&mut bytes).unwrap();
        assert_eq!(digest(&bytes), digest(&original));
        snapshot.seek(SeekFrom::Start(0)).unwrap();
        extract(snapshot, &content).unwrap();
        fs::write(&archive, &original).unwrap();
        assert_eq!(
            fs::read(content.join("node_modules/dep/file.txt")).unwrap(),
            b"original"
        );
        assert_eq!(archive_identity(&archive).unwrap(), digest(&original));
        assert_eq!(fs::read_dir(staging.path()).unwrap().count(), 1);
    }
}
