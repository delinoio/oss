use std::{
    collections::{BTreeMap, BTreeSet, VecDeque},
    io::Read,
    path::{Component, Path, PathBuf},
};

use anyhow::{bail, ensure, Context, Result};
use base64::Engine;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use crate::{config::Task, discover::Project, files};

pub const MAX_CACHE_BYTES: usize = 512 * 1024 * 1024;

enum CacheLock {
    Shared,
    Exclusive,
}

fn lock(root: &Path, mode: CacheLock) -> Result<std::fs::File> {
    let directory = root.join(".taskflow/locks");
    std::fs::create_dir_all(&directory)?;
    // The lock must survive deletion of the cache directory. Keep critical
    // sections limited to local cache I/O, never task execution or networking.
    let file = std::fs::OpenOptions::new()
        .create(true)
        .truncate(false)
        .read(true)
        .write(true)
        .open(directory.join("cache"))?;
    match mode {
        CacheLock::Shared => file.lock_shared()?,
        CacheLock::Exclusive => file.lock()?,
    }
    Ok(file)
}

pub fn read_lock(root: &Path) -> Result<std::fs::File> {
    lock(root, CacheLock::Shared)
}

pub fn clean(root: &Path) -> Result<()> {
    let _lock = lock(root, CacheLock::Exclusive)?;
    remove_path(&root.join(".taskflow/cache"))
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Artifact {
    pub version: u32,
    pub key: String,
    pub task: String,
    pub output_digest: String,
    pub files: Vec<FileRecord>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub shards: Option<(crate::shard::Inventory, Vec<crate::shard::ShardResults>)>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FileRecord {
    pub path: String,
    pub content: Content,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "kebab-case", deny_unknown_fields)]
pub enum Content {
    File {
        data: String,
        digest: String,
        executable: bool,
    },
    Directory,
    Link {
        target: String,
        directory: bool,
    },
}
#[derive(Debug, Clone, Serialize, Deserialize)]
struct Entry {
    version: u32,
    object: String,
}

pub fn anchors(task: &Task) -> Result<Vec<PathBuf>> {
    if task.cache {
        validate_artifact_outputs(task)?;
    }
    let mut values: Vec<PathBuf> = vec![];
    for pattern in task.output.iter().flatten() {
        let anchor = files::output_anchor(pattern);
        ensure!(
            !anchor.as_os_str().is_empty() && anchor != Path::new("."),
            "output requires a literal owned root"
        );
        if !values.iter().any(|v| anchor.starts_with(v)) {
            values.retain(|v| !v.starts_with(&anchor));
            values.push(anchor);
        }
    }
    values.sort();
    Ok(values)
}

pub fn validate_artifact_outputs(task: &Task) -> Result<()> {
    // Both cache and CI restoration replace complete roots. Partial ownership
    // remains available for local uncached tasks, but cannot cross this boundary.
    for pattern in task.output.iter().flatten() {
        if pattern.contains(['*', '?', '[', '{']) {
            ensure!(
                pattern.ends_with("/**")
                    && !pattern[..pattern.len() - 3].contains(['*', '?', '[', '{']),
                "artifact outputs must be exact paths or complete directory/** trees"
            );
        }
    }
    Ok(())
}

pub fn snapshot(project: &Project, task: &Task) -> Result<Vec<FileRecord>> {
    snapshot_inner(project, task, true)
}

fn snapshot_inner(project: &Project, task: &Task, capture: bool) -> Result<Vec<FileRecord>> {
    let mut entries = vec![];
    let mut total = 0;
    for anchor in anchors(task)? {
        let path = files::within(&project.directory, &project.directory.join(&anchor))?;
        ensure!(
            path.exists() || path.is_symlink(),
            "required output is missing: {}",
            anchor.display()
        );
        for entry in walkdir::WalkDir::new(&path).follow_links(false) {
            let entry = entry?;
            let path = entry.path();
            files::within(&project.directory, path)?;
            let relative = files::slash(path.strip_prefix(&project.directory)?);
            let content = if entry.file_type().is_symlink() {
                let target = std::fs::read_link(path)?;
                ensure!(!target.is_absolute(), "cache output links must be relative");
                Content::Link {
                    target: files::slash(&target),
                    directory: link_is_directory(path)?,
                }
            } else if entry.file_type().is_dir() {
                Content::Directory
            } else if entry.file_type().is_file() {
                let (digest, data) = if capture {
                    ensure!(
                        entry.metadata()?.len() <= (MAX_CACHE_BYTES - total) as u64,
                        "task outputs exceed cache size limit"
                    );
                    let mut bytes = Vec::new();
                    std::fs::File::open(path)?
                        .take((MAX_CACHE_BYTES - total) as u64 + 1)
                        .read_to_end(&mut bytes)?;
                    total += bytes.len();
                    ensure!(
                        total <= MAX_CACHE_BYTES,
                        "task outputs exceed cache size limit"
                    );
                    (
                        files::digest(&bytes),
                        base64::engine::general_purpose::STANDARD.encode(bytes),
                    )
                } else {
                    let mut reader = std::fs::File::open(path)?;
                    let mut hash = Sha256::new();
                    let mut buffer = [0; 64 * 1024];
                    loop {
                        let count = reader.read(&mut buffer)?;
                        if count == 0 {
                            break;
                        }
                        hash.update(&buffer[..count]);
                    }
                    (format!("{:x}", hash.finalize()), String::new())
                };
                #[cfg(unix)]
                let executable = {
                    use std::os::unix::fs::PermissionsExt;
                    entry.metadata()?.permissions().mode() & 0o111 != 0
                };
                #[cfg(not(unix))]
                let executable = false;
                Content::File {
                    digest,
                    data,
                    executable,
                }
            } else {
                bail!("unsupported special output file");
            };
            entries.push(FileRecord {
                path: relative,
                content,
            });
        }
    }
    entries.sort_by(|a, b| a.path.cmp(&b.path));
    Ok(entries)
}
// Windows records file and directory links distinctly, including dangling
// links.
fn link_is_directory(path: &Path) -> Result<bool> {
    #[cfg(windows)]
    {
        use std::os::windows::fs::FileTypeExt;
        Ok(std::fs::symlink_metadata(path)?
            .file_type()
            .is_symlink_dir())
    }
    #[cfg(not(windows))]
    {
        Ok(path.is_dir())
    }
}
pub fn output_digest(entries: &[FileRecord]) -> Result<String> {
    // Output identity is independent of the transfer encoding and its size
    // limit. Version the digest domain so older payload-based identities miss
    // safely; integrity validation separately verifies every encoded file.
    #[derive(Serialize)]
    #[serde(tag = "type", rename_all = "kebab-case")]
    enum Identity<'a> {
        File { digest: &'a str, executable: bool },
        Directory,
        Link { target: &'a str, directory: bool },
    }
    let identities: Vec<_> = entries
        .iter()
        .map(|entry| {
            let identity = match &entry.content {
                Content::File {
                    digest, executable, ..
                } => Identity::File {
                    digest,
                    executable: *executable,
                },
                Content::Directory => Identity::Directory,
                Content::Link { target, directory } => Identity::Link {
                    target,
                    directory: *directory,
                },
            };
            (&entry.path, identity)
        })
        .collect();
    Ok(files::digest(&serde_json::to_vec(&(
        "output-state-v2",
        identities,
    ))?))
}
pub fn output_state(project: &Project, task: &Task) -> Result<String> {
    output_digest(&snapshot_inner(project, task, false)?)
}

impl Artifact {
    pub fn capture(key: String, id: String, project: &Project, task: &Task) -> Result<Self> {
        validate_artifact_outputs(task)?;
        let files = snapshot(project, task)?;
        Ok(Self {
            version: 1,
            key,
            task: id,
            output_digest: output_digest(&files)?,
            files,
            shards: None,
        })
    }

    /// Check the immutable artifact without requiring the current project
    /// config.
    pub fn validate_integrity(&self, key: &str) -> Result<()> {
        ensure!(
            self.version == 1
                && self.key == key
                && self
                    .task
                    .split_once('#')
                    .is_some_and(|(project, task)| crate::config::identifier(project)
                        && crate::config::identifier(task)),
            "cache identity mismatch"
        );
        ensure!(
            self.output_digest == output_digest(&self.files)?,
            "cache output digest mismatch"
        );
        if let Some((inventory, reports)) = &self.shards {
            let count = reports
                .first()
                .context("missing cached shard results")?
                .count;
            ensure!((1..=256).contains(&count), "invalid cached shard count");
            ensure!(
                crate::shard::validate_reports(inventory, count, reports)?,
                "cached shard results contain failure"
            );
        }
        let mut seen = BTreeSet::new();
        let mut leaves = vec![];
        let mut total = 0;
        for entry in &self.files {
            let path = Path::new(&entry.path);
            ensure!(
                !entry.path.is_empty()
                    && path.components().all(|c| matches!(c, Component::Normal(_))),
                "unsafe cache entry path"
            );
            ensure!(
                seen.insert(entry.path.clone()),
                "duplicate cache entry path"
            );
            match &entry.content {
                Content::File { data, digest, .. } => {
                    leaves.push(path);
                    let bytes = base64::engine::general_purpose::STANDARD.decode(data)?;
                    total += bytes.len();
                    ensure!(
                        total <= MAX_CACHE_BYTES && files::digest(&bytes) == *digest,
                        "invalid cache file content"
                    );
                }
                Content::Link { target, .. } => {
                    ensure!(!Path::new(target).is_absolute(), "absolute cache link");
                    leaves.push(path);
                }
                Content::Directory => {}
            }
        }
        for entry in &self.files {
            ensure!(
                !leaves.iter().any(|leaf| Path::new(&entry.path) != *leaf
                    && Path::new(&entry.path).starts_with(leaf)),
                "cache path traverses a non-directory record"
            );
        }
        Ok(())
    }

    pub fn validate(&self, key: &str, id: &str, project: &Project, task: &Task) -> Result<()> {
        validate_artifact_outputs(task)?;
        self.validate_integrity(key)?;
        ensure!(self.task == id, "cache task identity mismatch");
        if let Some((inventory, reports)) = &self.shards {
            let count = task
                .shard
                .as_ref()
                .context("unexpected cached shard results")?
                .count;
            crate::shard::validate_reports(inventory, count, reports)?;
        }
        let roots = anchors(task)?;
        for entry in &self.files {
            ensure!(
                roots
                    .iter()
                    .any(|root| Path::new(&entry.path).starts_with(root)),
                "cache entry outside declared outputs"
            );
        }
        let links: Vec<_> = self
            .files
            .iter()
            .filter(|entry| matches!(entry.content, Content::Link { .. }))
            .map(|entry| Path::new(&entry.path))
            .collect();
        let seen: BTreeSet<_> = self.files.iter().map(|entry| entry.path.as_str()).collect();
        let contents: BTreeMap<_, _> = self
            .files
            .iter()
            .map(|entry| (Path::new(&entry.path), &entry.content))
            .collect();
        for link in &links {
            let Content::Link { target, .. } = contents[link] else {
                unreachable!()
            };
            validate_link_target(
                project,
                &roots,
                &contents,
                &link.parent().unwrap().join(target),
            )?;
        }
        for root in roots {
            ensure!(
                seen.contains(files::slash(&root).as_str()),
                "cache is missing a required output root"
            );
        }
        Ok(())
    }

    pub fn restore(&self, key: &str, id: &str, project: &Project, task: &Task) -> Result<()> {
        self.validate(key, id, project, task)?;
        let roots = anchors(task)?;
        for root in &roots {
            files::within(&project.directory, &project.directory.join(root))?;
        }
        // Stage on the same filesystem, then retain every old root until the
        // complete multi-root transaction succeeds so failures can be rolled back.
        let staging = tempfile::Builder::new()
            .prefix(".taskflow-restore-")
            .tempdir_in(&project.directory)?;
        self.validate_destination_paths(staging.path())?;
        let staged = staging.path().join("new");
        let backup = staging.path().join("old");
        std::fs::create_dir_all(&staged)?;
        std::fs::create_dir_all(&backup)?;
        for entry in &self.files {
            let path = staged.join(&entry.path);
            std::fs::create_dir_all(path.parent().unwrap())?;
            match &entry.content {
                Content::Directory => std::fs::create_dir_all(&path)?,
                Content::File {
                    data, executable, ..
                } => {
                    std::fs::write(
                        &path,
                        base64::engine::general_purpose::STANDARD.decode(data)?,
                    )?;
                    #[cfg(unix)]
                    {
                        use std::os::unix::fs::PermissionsExt;
                        std::fs::set_permissions(
                            &path,
                            std::fs::Permissions::from_mode(if *executable {
                                0o755
                            } else {
                                0o644
                            }),
                        )?;
                    }
                    let _ = executable;
                }
                Content::Link { .. } => {}
            }
        }
        for entry in &self.files {
            if let Content::Link { target, directory } = &entry.content {
                let _ = directory;
                let path = staged.join(&entry.path);
                #[cfg(unix)]
                std::os::unix::fs::symlink(target, &path)?;
                #[cfg(windows)]
                {
                    // CreateSymbolicLinkW preserves relative target separators. Keep the
                    // artifact portable, but use native separators at the Windows boundary
                    // so the resulting link can be traversed by filesystem operations.
                    let target = target.replace('/', "\\");
                    if *directory {
                        std::os::windows::fs::symlink_dir(target, &path)?;
                    } else {
                        std::os::windows::fs::symlink_file(target, &path)?;
                    }
                }
            }
        }
        let mut moved: Vec<(PathBuf, bool)> = vec![];
        let result: Result<()> = (|| {
            for root in &roots {
                let destination = project.directory.join(root);
                let previous = backup.join(root);
                std::fs::create_dir_all(previous.parent().unwrap())?;
                std::fs::create_dir_all(destination.parent().unwrap())?;
                let existed = destination.exists() || destination.is_symlink();
                if existed {
                    std::fs::rename(&destination, &previous)?;
                }
                moved.push((root.clone(), existed));
                std::fs::rename(staged.join(root), destination)?;
            }
            Ok(())
        })();
        if let Err(error) = result {
            let mut rollback_failed = false;
            for (root, existed) in moved.iter().rev() {
                let destination = project.directory.join(root);
                if remove_path(&destination).is_err() {
                    rollback_failed = true;
                }
                if *existed && std::fs::rename(backup.join(root), destination).is_err() {
                    rollback_failed = true;
                }
            }
            if rollback_failed {
                let recovery = staging.keep();
                bail!(
                    "output restore and rollback failed; retained recovery directory {}: {error}",
                    recovery.display()
                );
            }
            return Err(error.context("output restoration rolled back"));
        }
        Ok(())
    }

    fn validate_destination_paths(&self, staging: &Path) -> Result<()> {
        // Probe names on the actual restore filesystem, without writing any
        // artifact payload or touching existing outputs. Case sensitivity and
        // Unicode equivalence can vary by volume/directory, not just by OS.
        // Include every prefix so aliased directories with different children
        // cannot silently merge even when no complete file names collide.
        let names = staging.join("names");
        std::fs::create_dir(&names)?;
        let mut seen = BTreeSet::new();
        for entry in &self.files {
            let mut prefix = PathBuf::new();
            for component in Path::new(&entry.path).components() {
                prefix.push(component);
                if seen.insert(prefix.clone()) {
                    std::fs::create_dir(names.join(&prefix)).with_context(|| {
                        format!(
                            "cache path aliases another path or is unsupported on the restore \
                             filesystem: {}",
                            files::slash(&prefix)
                        )
                    })?;
                }
            }
        }
        Ok(())
    }
}

fn link_components(path: &Path) -> Result<VecDeque<PathBuf>> {
    path.components()
        .filter(|part| !matches!(part, Component::CurDir))
        .map(|part| match part {
            Component::Normal(name) => Ok(PathBuf::from(name)),
            Component::ParentDir => Ok(PathBuf::from("..")),
            _ => bail!("cache link must have a relative target"),
        })
        .collect()
}

fn validate_link_target(
    project: &Project,
    roots: &[PathBuf],
    contents: &BTreeMap<&Path, &Content>,
    target: &Path,
) -> Result<()> {
    let mut pending = link_components(target)?;
    let mut resolved = PathBuf::new();
    let mut followed = 0;
    while let Some(part) = pending.pop_front() {
        if part == Path::new("..") {
            ensure!(resolved.pop(), "cache link escapes project");
            continue;
        }
        let candidate = resolved.join(part);
        if let Some(Content::Link { target, .. }) = contents.get(candidate.as_path()) {
            followed += 1;
            ensure!(followed <= 40, "cache link cycle or excessive link depth");
            let mut expansion = link_components(Path::new(target))?;
            expansion.append(&mut pending);
            pending = expansion;
        } else if roots.iter().any(|root| candidate.starts_with(root)) {
            // These paths will be replaced by the archive, so follow the new
            // records instead of stale links in the current output tree.
            resolved = candidate;
        } else {
            let disk = project.directory.join(&candidate);
            if disk.exists() || disk.is_symlink() {
                // Resolve each existing prefix before processing `..`; lexical
                // normalization first would hide escapes through directory links.
                let canonical = disk.canonicalize()?;
                resolved = canonical
                    .strip_prefix(&project.directory)
                    .context("cache link resolves outside project")?
                    .to_path_buf();
            } else {
                resolved = candidate;
            }
        }
    }
    Ok(())
}

pub fn remove_path(path: &Path) -> Result<()> {
    if path.is_symlink() || path.is_file() {
        std::fs::remove_file(path)?;
    } else if path.is_dir() {
        std::fs::remove_dir_all(path)?;
    }
    Ok(())
}
pub fn entry_path(root: &Path, key: &str) -> PathBuf {
    root.join(".taskflow/cache/entries")
        .join(format!("{key}.json"))
}
pub fn encode(artifact: &Artifact) -> Result<Vec<u8>> {
    let bytes = serde_json::to_vec(artifact)?;
    ensure!(
        bytes.len() <= MAX_CACHE_BYTES,
        "encoded cache artifact exceeds size limit"
    );
    Ok(bytes)
}
pub fn store(root: &Path, artifact: &Artifact) -> Result<Vec<u8>> {
    let bytes = encode(artifact)?;
    store_encoded_if(root, &artifact.key, &bytes, || Ok(true))?;
    Ok(bytes)
}

pub(crate) fn store_if(
    root: &Path,
    artifact: &Artifact,
    valid: impl FnMut() -> Result<bool>,
) -> Result<bool> {
    store_encoded_if(root, &artifact.key, &encode(artifact)?, valid)
}

#[derive(Debug)]
pub(crate) struct PublicationRollbackFailure;
impl std::fmt::Display for PublicationRollbackFailure {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("local cache publication rollback failed")
    }
}
impl std::error::Error for PublicationRollbackFailure {}

fn store_encoded_if(
    root: &Path,
    key: &str,
    bytes: &[u8],
    mut valid: impl FnMut() -> Result<bool>,
) -> Result<bool> {
    let _lock = lock(root, CacheLock::Exclusive)?;
    if !valid()? {
        return Ok(false);
    }
    let object = files::digest(bytes);
    files::atomic_write(
        &root
            .join(".taskflow/cache/objects")
            .join(format!("{object}.json")),
        bytes,
    )?;
    if !valid()? {
        return Ok(false);
    }
    let entry = entry_path(root, key);
    let previous = match std::fs::read(&entry) {
        Ok(bytes) => Some(bytes),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => None,
        Err(error) => return Err(error.into()),
    };
    files::atomic_write(&entry, &serde_json::to_vec(&Entry { version: 1, object })?)?;
    // Readers cannot observe the replacement until this lock is released. If
    // cancellation or input invalidation wins during synchronous publication,
    // restore the previous binding before exposing the cache again.
    let accepted = valid();
    if !matches!(accepted, Ok(true)) {
        match previous {
            Some(bytes) => files::atomic_write(&entry, &bytes),
            None => remove_path(&entry),
        }
        .context(PublicationRollbackFailure)?;
        tracing::info!(
            code = "cache-publication-rolled-back",
            "Discarded invalidated local cache publication"
        );
    }
    accepted
}

pub fn load(root: &Path, key: &str) -> Result<Option<Artifact>> {
    let _lock = read_lock(root)?;
    let path = entry_path(root, key);
    if !path.exists() {
        return Ok(None);
    }
    let entry: Entry = serde_json::from_slice(&std::fs::read(path)?)?;
    ensure!(
        entry.version == 1 && valid_hash(&entry.object),
        "invalid cache manifest"
    );
    let file = root
        .join(".taskflow/cache/objects")
        .join(format!("{}.json", entry.object));
    ensure!(
        std::fs::metadata(&file)?.len() <= MAX_CACHE_BYTES as u64,
        "cache object too large"
    );
    let bytes = std::fs::read(file)?;
    ensure!(
        files::digest(&bytes) == entry.object,
        "cache object digest mismatch"
    );
    Ok(Some(
        serde_json::from_slice(&bytes).context("invalid cache object")?,
    ))
}
pub fn valid_hash(value: &str) -> bool {
    value.len() == 64
        && value
            .bytes()
            .all(|c| c.is_ascii_hexdigit() && !c.is_ascii_uppercase())
}

#[cfg(test)]
mod publication_tests {
    use super::*;

    #[test]
    fn invalidation_at_each_publication_boundary_preserves_previous_entry() {
        for previous in [false, true] {
            for stop_at in 1..=3 {
                for fail_check in [false, true] {
                    let root = tempfile::tempdir().unwrap();
                    let key = files::digest(b"task");
                    if previous {
                        store_encoded_if(root.path(), &key, b"old", || Ok(true)).unwrap();
                    }
                    let before = std::fs::read(entry_path(root.path(), &key)).ok();
                    let cancel = tokio_util::sync::CancellationToken::new();
                    let mut calls = 0;
                    let result = store_encoded_if(root.path(), &key, b"new", || {
                        calls += 1;
                        if calls == stop_at {
                            cancel.cancel();
                        }
                        if calls == 3 {
                            // Exercise rollback after the entry's atomic replace,
                            // while cache readers are still excluded by the lock.
                            assert_ne!(std::fs::read(entry_path(root.path(), &key)).ok(), before);
                        }
                        if cancel.is_cancelled() && fail_check {
                            anyhow::bail!("input snapshot unavailable");
                        }
                        Ok(!cancel.is_cancelled())
                    });
                    assert_eq!(result.is_err(), fail_check);
                    assert!(!result.unwrap_or(false));
                    assert_eq!(calls, stop_at);
                    assert_eq!(std::fs::read(entry_path(root.path(), &key)).ok(), before);
                }
            }
        }
    }
}
