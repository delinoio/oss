use std::{
    collections::{BTreeMap, BTreeSet, VecDeque},
    path::{Component, Path, PathBuf},
};

use anyhow::{bail, ensure, Context, Result};
use base64::Engine;
use serde::{Deserialize, Serialize};

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
                let bytes = std::fs::read(path)?;
                total += bytes.len();
                ensure!(
                    total <= MAX_CACHE_BYTES,
                    "task outputs exceed cache size limit"
                );
                #[cfg(unix)]
                let executable = {
                    use std::os::unix::fs::PermissionsExt;
                    entry.metadata()?.permissions().mode() & 0o111 != 0
                };
                #[cfg(not(unix))]
                let executable = false;
                Content::File {
                    digest: files::digest(&bytes),
                    data: base64::engine::general_purpose::STANDARD.encode(bytes),
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
    Ok(files::digest(&serde_json::to_vec(entries)?))
}
pub fn output_state(project: &Project, task: &Task) -> Result<String> {
    output_digest(&snapshot(project, task)?)
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

    pub fn validate(&self, key: &str, id: &str, project: &Project, task: &Task) -> Result<()> {
        validate_artifact_outputs(task)?;
        ensure!(
            self.version == 1 && self.key == key && self.task == id,
            "cache identity mismatch"
        );
        ensure!(
            self.output_digest == output_digest(&self.files)?,
            "cache output digest mismatch"
        );
        if let Some((inventory, reports)) = &self.shards {
            let count = task
                .shard
                .as_ref()
                .context("unexpected cached shard results")?
                .count;
            crate::shard::validate_reports(inventory, count, reports)?;
        }
        let roots = anchors(task)?;
        let mut seen = BTreeSet::new();
        let mut links = vec![];
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
            ensure!(
                roots.iter().any(|r| path.starts_with(r)),
                "cache entry outside declared outputs"
            );
            match &entry.content {
                Content::File { data, digest, .. } => {
                    let bytes = base64::engine::general_purpose::STANDARD.decode(data)?;
                    total += bytes.len();
                    ensure!(
                        total <= MAX_CACHE_BYTES && files::digest(&bytes) == *digest,
                        "invalid cache file content"
                    );
                }
                Content::Link { target, .. } => {
                    ensure!(!Path::new(target).is_absolute(), "absolute cache link");
                    links.push(path);
                }
                Content::Directory => {}
            }
        }
        for entry in &self.files {
            ensure!(
                !links.iter().any(|link| Path::new(&entry.path) != *link
                    && Path::new(&entry.path).starts_with(link)),
                "cache file traverses a symlink"
            );
        }
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
                seen.contains(&files::slash(&root)),
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
    let _lock = lock(root, CacheLock::Exclusive)?;
    let object = files::digest(&bytes);
    files::atomic_write(
        &root
            .join(".taskflow/cache/objects")
            .join(format!("{object}.json")),
        &bytes,
    )?;
    files::atomic_write(
        &entry_path(root, &artifact.key),
        &serde_json::to_vec(&Entry { version: 1, object })?,
    )?;
    Ok(bytes)
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
