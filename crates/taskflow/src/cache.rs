use std::{
    collections::{BTreeMap, BTreeSet, VecDeque},
    io::{Read, Write},
    path::{Component, Path, PathBuf},
};

use anyhow::{bail, ensure, Context, Result};
use base64::Engine;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use crate::{config::Task, discover::Project, files};

pub const MAX_CACHE_BYTES: usize = 512 * 1024 * 1024;
const MAX_ENTRY_BYTES: u64 = 1024;

enum CacheLock {
    Shared,
    Exclusive,
}

fn lock(root: &Path, mode: CacheLock) -> Result<std::fs::File> {
    // The registry is outside all removable workspace state. Keep critical
    // sections limited to local cache I/O, never task execution or networking.
    let file = crate::coordination::open(root, "cache")?;
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
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub result_identity: Option<String>,
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
        ensure!(
            !pattern.split(['/', '\\']).any(files::reserved_name),
            "output must not address reserved state"
        );
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

enum OutputPattern {
    Tree(PathBuf),
    Glob(globset::GlobMatcher),
}

impl OutputPattern {
    fn new(project: &Project, pattern: &str) -> Result<Self> {
        if !pattern.contains(['*', '?', '[', '{'])
            || pattern
                .strip_suffix("/**")
                .is_some_and(|root| !root.contains(['*', '?', '[', '{']))
        {
            // Match exact roots using the same filesystem spelling as traversal
            // (for example Out/out on a case-insensitive destination).
            let root = files::within(
                &project.directory,
                &project.directory.join(files::output_anchor(pattern)),
            )?;
            Ok(Self::Tree(
                root.strip_prefix(&project.directory)?.to_path_buf(),
            ))
        } else {
            Ok(Self::Glob(globset::Glob::new(pattern)?.compile_matcher()))
        }
    }

    fn matches(&self, path: &str) -> bool {
        match self {
            Self::Tree(root) => Path::new(path).starts_with(root),
            Self::Glob(pattern) => pattern.is_match(path),
        }
    }
}

fn snapshot_inner(project: &Project, task: &Task, capture: bool) -> Result<Vec<FileRecord>> {
    snapshot_with_limit(project, task, capture, MAX_CACHE_BYTES)
}

fn snapshot_with_limit(
    project: &Project,
    task: &Task,
    capture: bool,
    limit: usize,
) -> Result<Vec<FileRecord>> {
    let mut entries = vec![];
    // Include array punctuation, every escaped record field, and Base64 bytes.
    // Uncached output identity hashing deliberately has no transfer-size bound.
    let mut budget = BoundedWriter {
        inner: std::io::sink(),
        remaining: limit,
    };
    if capture {
        budget.write_all(b"[]")?;
    }
    let patterns: Vec<_> = task
        .output
        .iter()
        .flatten()
        .map(|p| OutputPattern::new(project, p))
        .collect::<Result<_>>()?;
    let mut found = vec![false; patterns.len()];
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
            let relative = files::slash(path.strip_prefix(&project.directory)?)?;
            let mut owned = false;
            for (index, pattern) in patterns.iter().enumerate() {
                if pattern.matches(&relative) {
                    found[index] = true;
                    owned = true;
                }
            }
            // Literal anchors bound traversal, not ownership. In particular a
            // neighboring input cannot satisfy a required partial output or
            // change that task's output identity.
            if !owned {
                continue;
            }
            files::within(&project.directory, path)?;
            ensure!(
                !relative.split('/').any(files::reserved_name),
                "output must not capture reserved state"
            );
            if capture && !entries.is_empty() {
                budget.write_all(b",")?;
            }
            let content = if entry.file_type().is_symlink() {
                let target = files::slash(&std::fs::read_link(path)?)?;
                validate_portable_link(&target)?;
                Content::Link {
                    target,
                    directory: link_is_directory(path)?,
                }
            } else if entry.file_type().is_dir() {
                Content::Directory
            } else if entry.file_type().is_file() {
                #[cfg(unix)]
                let executable = {
                    use std::os::unix::fs::PermissionsExt;
                    entry.metadata()?.permissions().mode() & 0o111 != 0
                };
                #[cfg(not(unix))]
                let executable = false;
                let (digest, data) = if capture {
                    // Reserve metadata before reading even an empty file. The
                    // placeholder digest has the final SHA-256 encoded length.
                    serde_json::to_writer(
                        &mut budget,
                        &FileRecord {
                            path: relative.clone(),
                            content: Content::File {
                                data: String::new(),
                                digest: "0".repeat(64),
                                executable,
                            },
                        },
                    )?;
                    let raw_limit = budget.remaining / 4 * 3;
                    ensure!(
                        entry.metadata()?.len() <= raw_limit as u64,
                        "task outputs exceed cache size limit"
                    );
                    let mut bytes = Vec::new();
                    std::fs::File::open(path)?
                        .take(raw_limit as u64 + 1)
                        .read_to_end(&mut bytes)?;
                    let encoded_len = bytes.len().div_ceil(3) * 4;
                    ensure!(
                        encoded_len <= budget.remaining,
                        "task outputs exceed cache size limit"
                    );
                    budget.remaining -= encoded_len;
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
                Content::File {
                    digest,
                    data,
                    executable,
                }
            } else {
                bail!("unsupported special output file");
            };
            let record = FileRecord {
                path: relative,
                content,
            };
            if capture && !matches!(record.content, Content::File { .. }) {
                serde_json::to_writer(&mut budget, &record)?;
            }
            entries.push(record);
        }
    }
    for (pattern, found) in task.output.iter().flatten().zip(found) {
        ensure!(found, "required output pattern has no matches: {pattern}");
    }
    entries.sort_by(|a, b| a.path.cmp(&b.path));
    // Partial roots are never replaced as a whole. Link validation must inspect
    // unowned live ancestors instead of assuming the snapshot describes them.
    let replaced = if validate_artifact_outputs(task).is_ok() {
        anchors(task)?
    } else {
        vec![]
    };
    validate_links(project, &replaced, &entries)?;
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
            result_identity: files.is_empty().then(|| key.clone()),
            key,
            task: id,
            output_digest: output_digest(&files)?,
            files,
            shards: None,
        })
    }

    /// Outputless tasks have a semantic result even though their file snapshot
    /// is empty. Keep that identity distinct from the snapshot checksum.
    pub fn result_output(&self) -> &str {
        self.result_identity
            .as_deref()
            .unwrap_or(&self.output_digest)
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
        ensure!(
            if self.files.is_empty() {
                self.result_identity.as_deref().is_some_and(valid_hash)
            } else {
                self.result_identity.is_none()
            },
            "invalid or missing outputless cache result identity"
        );
        if let Some((inventory, reports)) = &self.shards {
            let count = reports
                .first()
                .context("missing cached shard results")?
                .count;
            ensure!((1..=256).contains(&count), "invalid cached shard count");
            ensure!(
                reports.len() == 1 || reports.len() == count,
                "cached shards must contain one partition or the complete suite"
            );
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
            let identity: PathBuf = path.components().collect();
            ensure!(
                !entry.path.is_empty()
                    && !entry.path.contains(['\\', '\0'])
                    && crate::config::project_relative(&entry.path)
                    && path.components().all(|c| matches!(c, Component::Normal(_)))
                    && files::slash(&identity)? == entry.path
                    && !entry.path.split('/').any(files::reserved_name),
                "unsafe cache entry path"
            );
            // Serialized paths have one portable spelling. Filesystem APIs
            // otherwise collapse dot segments and repeated separators while the
            // artifact digest still counts separate records for the same file.
            ensure!(seen.insert(identity), "duplicate cache entry path");
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
                    validate_portable_link(target)?;
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
        self.validate_owned_roots(key, id, project, task)
            .map(|_| ())
    }

    fn validate_owned_roots(
        &self,
        key: &str,
        id: &str,
        project: &Project,
        task: &Task,
    ) -> Result<Vec<PathBuf>> {
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
        let roots = self.destination_roots(project, task)?;
        for entry in &self.files {
            ensure!(
                roots
                    .iter()
                    .any(|root| Path::new(&entry.path).starts_with(root)),
                "cache entry outside declared outputs"
            );
        }
        validate_links(project, &roots, &self.files)?;
        let seen: BTreeSet<_> = self.files.iter().map(|entry| entry.path.as_str()).collect();
        for root in &roots {
            ensure!(
                seen.contains(files::slash(root)?.as_str()),
                "cache is missing a required output root"
            );
        }
        Ok(roots)
    }

    pub fn restore(&self, key: &str, id: &str, project: &Project, task: &Task) -> Result<()> {
        let roots = self.validate_owned_roots(key, id, project, task)?;
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

    fn destination_roots(&self, project: &Project, task: &Task) -> Result<Vec<PathBuf>> {
        let roots = anchors(task)?;
        let recorded: BTreeSet<_> = self
            .files
            .iter()
            .map(|entry| Path::new(&entry.path))
            .collect();
        if roots.iter().all(|root| recorded.contains(root.as_path())) {
            return Ok(roots);
        }
        // Captures retain the filesystem's actual spelling. Resolve declarations
        // against an isolated copy of those names, including on a clean runner
        // where no output exists yet. Never lowercase names or follow live output
        // links to infer ownership; equivalence belongs to this destination.
        let probe = tempfile::Builder::new()
            .prefix(".taskflow-restore-roots-")
            .tempdir_in(&project.directory)?;
        let names = self.validate_destination_paths(probe.path())?;
        let records: BTreeMap<_, _> = self
            .files
            .iter()
            .map(|entry| {
                Ok((
                    names.join(&entry.path).canonicalize()?,
                    PathBuf::from(&entry.path),
                ))
            })
            .collect::<Result<_>>()?;
        roots
            .iter()
            .map(|root| {
                let identity = names
                    .join(root)
                    .canonicalize()
                    .context("cache is missing a required output root")?;
                records
                    .get(&identity)
                    .cloned()
                    .context("cache is missing a required output root")
            })
            .collect()
    }

    fn validate_destination_paths(&self, staging: &Path) -> Result<PathBuf> {
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
                            prefix.display()
                        )
                    })?;
                }
            }
        }
        Ok(names)
    }
}

fn validate_portable_link(target: &str) -> Result<()> {
    ensure!(
        !target.is_empty()
            && !target.contains(['\\', '\0'])
            && crate::config::project_relative(target),
        "cache link must have a portable relative target"
    );
    Ok(())
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

fn validate_links(project: &Project, roots: &[PathBuf], entries: &[FileRecord]) -> Result<()> {
    let contents: BTreeMap<_, _> = entries
        .iter()
        .map(|entry| (Path::new(&entry.path), &entry.content))
        .collect();
    for (path, content) in &contents {
        if let Content::Link { target, .. } = content {
            validate_link_target(
                project,
                roots,
                &contents,
                &path.parent().unwrap().join(target),
            )?;
        }
    }
    Ok(())
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
struct BoundedWriter<W> {
    inner: W,
    remaining: usize,
}

impl<W: Write> Write for BoundedWriter<W> {
    fn write(&mut self, bytes: &[u8]) -> std::io::Result<usize> {
        if bytes.len() > self.remaining {
            return Err(std::io::Error::other(
                "encoded cache artifact exceeds size limit",
            ));
        }
        let count = self.inner.write(bytes)?;
        self.remaining -= count;
        Ok(count)
    }

    fn flush(&mut self) -> std::io::Result<()> {
        self.inner.flush()
    }
}

pub fn encode(artifact: &Artifact) -> Result<Vec<u8>> {
    let mut writer = BoundedWriter {
        inner: Vec::new(),
        remaining: MAX_CACHE_BYTES,
    };
    serde_json::to_writer(&mut writer, artifact)?;
    Ok(writer.inner)
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
    let previous = match files::read_regular_limited(&entry, MAX_ENTRY_BYTES) {
        Ok(bytes) => Some(bytes),
        Err(error) => {
            if error
                .downcast_ref::<std::io::Error>()
                .is_none_or(|e| e.kind() != std::io::ErrorKind::NotFound)
            {
                tracing::warn!(
                    code = "invalid-cache-manifest",
                    "Replacing unreadable local cache binding"
                );
            }
            // A corrupt binding is not a rollback baseline. Atomic replacement
            // repairs the leaf without following a link or opening a FIFO.
            None
        }
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
    let bytes = match files::read_regular_limited(&path, MAX_ENTRY_BYTES) {
        Ok(bytes) => bytes,
        Err(error)
            if error
                .downcast_ref::<std::io::Error>()
                .is_some_and(|e| e.kind() == std::io::ErrorKind::NotFound) =>
        {
            return Ok(None)
        }
        Err(error) => return Err(error.context("invalid cache manifest")),
    };
    let entry: Entry = serde_json::from_slice(&bytes)?;
    ensure!(
        entry.version == 1 && valid_hash(&entry.object),
        "invalid cache manifest"
    );
    let file = root
        .join(".taskflow/cache/objects")
        .join(format!("{}.json", entry.object));
    let bytes = files::read_regular_limited(&file, MAX_CACHE_BYTES as u64)
        .context("invalid cache object file")?;
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
    fn local_cache_reads_reject_and_repair_unsafe_manifests() {
        let root = tempfile::tempdir().unwrap();
        let artifact = Artifact {
            version: 1,
            key: files::digest(b"key"),
            task: "app#test".into(),
            output_digest: files::digest(b"output"),
            result_identity: None,
            files: vec![],
            shards: None,
        };
        let bytes = store(root.path(), &artifact).unwrap();
        let path = entry_path(root.path(), &artifact.key);
        std::fs::File::create(&path)
            .unwrap()
            .set_len(MAX_ENTRY_BYTES + 1)
            .unwrap();
        assert!(load(root.path(), &artifact.key).is_err());
        store(root.path(), &artifact).unwrap();
        assert!(load(root.path(), &artifact.key).unwrap().is_some());
        #[cfg(unix)]
        {
            use std::os::unix::ffi::OsStrExt;
            std::fs::remove_file(&path).unwrap();
            let native = std::ffi::CString::new(path.as_os_str().as_bytes()).unwrap();
            assert_eq!(unsafe { nix::libc::mkfifo(native.as_ptr(), 0o600) }, 0);
            assert!(load(root.path(), &artifact.key).is_err());
            store(root.path(), &artifact).unwrap();
            assert!(load(root.path(), &artifact.key).unwrap().is_some());
            let target = root.path().join("binding.json");
            std::fs::rename(&path, &target).unwrap();
            let original = std::fs::read(&target).unwrap();
            std::os::unix::fs::symlink(&target, &path).unwrap();
            assert!(load(root.path(), &artifact.key).is_err());
            store(root.path(), &artifact).unwrap();
            assert_eq!(std::fs::read(&target).unwrap(), original);
            assert!(load(root.path(), &artifact.key).unwrap().is_some());
        }
        let object = root
            .path()
            .join(".taskflow/cache/objects")
            .join(format!("{}.json", files::digest(&bytes)));
        std::fs::File::create(&object)
            .unwrap()
            .set_len(MAX_CACHE_BYTES as u64 + 1)
            .unwrap();
        assert!(load(root.path(), &artifact.key).is_err());
        #[cfg(unix)]
        {
            use std::os::unix::ffi::OsStrExt;
            std::fs::remove_file(&object).unwrap();
            let native = std::ffi::CString::new(object.as_os_str().as_bytes()).unwrap();
            assert_eq!(unsafe { nix::libc::mkfifo(native.as_ptr(), 0o600) }, 0);
            assert!(load(root.path(), &artifact.key).is_err());
        }
    }

    fn capture_fixture(root: &Path) -> (Project, Task) {
        (
            Project {
                id: "app".into(),
                directory: root.canonicalize().unwrap(),
                native: BTreeSet::new(),
                config: None,
            },
            serde_json::from_value(
                serde_json::json!({"command":["unused"],"input":[],"output":["out/**"]}),
            )
            .unwrap(),
        )
    }

    #[test]
    fn capture_budget_counts_empty_records_and_base64_before_retaining_them() {
        let root = tempfile::tempdir().unwrap();
        std::fs::create_dir(root.path().join("out")).unwrap();
        let (project, task) = capture_fixture(root.path());
        for size in [0, 1, 2, 3, 4, 200] {
            std::fs::write(root.path().join("out/file"), vec![42; size]).unwrap();
            let entries = snapshot(&project, &task).unwrap();
            let encoded = serde_json::to_vec(&entries).unwrap();
            assert!(snapshot_with_limit(&project, &task, true, encoded.len()).is_ok());
            assert!(snapshot_with_limit(&project, &task, true, encoded.len() - 1).is_err());
        }
        std::fs::remove_file(root.path().join("out/file")).unwrap();
        for i in 0..32 {
            std::fs::create_dir(root.path().join(format!("out/directory-{i}"))).unwrap();
            std::fs::write(root.path().join(format!("out/empty-{i}")), []).unwrap();
        }
        assert!(snapshot_with_limit(&project, &task, true, 1024).is_err());
        // Identity-only snapshots remain available for large uncached outputs.
        assert!(snapshot_with_limit(&project, &task, false, 0).is_ok());
    }

    #[cfg(unix)]
    #[test]
    fn capture_budget_counts_escaped_paths_and_link_metadata() {
        let root = tempfile::tempdir().unwrap();
        std::fs::create_dir(root.path().join("out")).unwrap();
        let target = "long-target-name-".repeat(8);
        std::fs::write(root.path().join("out").join(&target), []).unwrap();
        std::os::unix::fs::symlink(&target, root.path().join("out/link\"\n")).unwrap();
        let (project, task) = capture_fixture(root.path());
        let size = serde_json::to_vec(&snapshot(&project, &task).unwrap())
            .unwrap()
            .len();
        assert!(snapshot_with_limit(&project, &task, true, size).is_ok());
        assert!(snapshot_with_limit(&project, &task, true, size - 1).is_err());
    }

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
