use std::{
    collections::BTreeMap,
    fmt,
    fs::{self, File},
    io::Read,
    path::{Path, PathBuf},
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use globset::Glob;
use walkdir::WalkDir;

use crate::error::{Result, WithWatchError};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ChangeDetectionMode {
    ContentHash,
    MtimeOnly,
}

impl ChangeDetectionMode {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::ContentHash => "content-hash",
            Self::MtimeOnly => "mtime-only",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CommandSource {
    Argv,
    Shell,
    Exec,
}

impl CommandSource {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Argv => "argv",
            Self::Shell => "shell",
            Self::Exec => "exec",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum WatchInputKind {
    Explicit,
    Inferred,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum PathSnapshotMode {
    ContentPath,
    ContentTree,
    MetadataPath,
    MetadataChildren,
    MetadataTree,
}

impl PathSnapshotMode {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::ContentPath => "content-path",
            Self::ContentTree => "content-tree",
            Self::MetadataPath => "metadata-path",
            Self::MetadataChildren => "metadata-children",
            Self::MetadataTree => "metadata-tree",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum WatchInput {
    Path {
        kind: WatchInputKind,
        path: PathBuf,
        watch_anchor: PathBuf,
        snapshot_mode: PathSnapshotMode,
    },
    Glob {
        kind: WatchInputKind,
        raw: String,
        absolute_pattern: String,
        watch_anchor: PathBuf,
    },
}

impl WatchInput {
    pub fn path(raw: &str, cwd: &Path, kind: WatchInputKind) -> Result<Self> {
        let absolute_path = absolutize(raw, cwd);
        let snapshot_mode = default_path_snapshot_mode(&absolute_path);
        Self::path_with_snapshot_mode(raw, cwd, kind, snapshot_mode)
    }

    pub fn path_with_snapshot_mode(
        raw: &str,
        cwd: &Path,
        kind: WatchInputKind,
        snapshot_mode: PathSnapshotMode,
    ) -> Result<Self> {
        let absolute_path = absolutize(raw, cwd);
        let watch_anchor = path_watch_anchor(&absolute_path).ok_or_else(|| {
            WithWatchError::MissingWatchAnchor {
                path: absolute_path.clone(),
            }
        })?;

        Ok(Self::Path {
            kind,
            path: absolute_path,
            watch_anchor,
            snapshot_mode,
        })
    }

    pub fn glob(raw: &str, cwd: &Path) -> Result<Self> {
        let absolute_pattern_path = absolutize(raw, cwd);
        let absolute_pattern = normalize_path_string(&absolute_pattern_path);
        Glob::new(&absolute_pattern).map_err(|error| WithWatchError::InvalidGlob {
            pattern: raw.to_string(),
            message: error.to_string(),
        })?;

        let anchor_candidate = glob_anchor(raw, cwd);
        let watch_anchor = nearest_existing_parent(&anchor_candidate).ok_or_else(|| {
            WithWatchError::MissingWatchAnchor {
                path: anchor_candidate.clone(),
            }
        })?;

        Ok(Self::Glob {
            kind: WatchInputKind::Explicit,
            raw: raw.to_string(),
            absolute_pattern,
            watch_anchor,
        })
    }

    pub fn kind(&self) -> WatchInputKind {
        match self {
            Self::Path { kind, .. } | Self::Glob { kind, .. } => *kind,
        }
    }

    pub fn watch_anchor(&self) -> &Path {
        match self {
            Self::Path { watch_anchor, .. } | Self::Glob { watch_anchor, .. } => watch_anchor,
        }
    }

    pub fn snapshot_mode_label(&self) -> &'static str {
        match self {
            Self::Path { snapshot_mode, .. } => snapshot_mode.as_str(),
            Self::Glob { .. } => PathSnapshotMode::ContentTree.as_str(),
        }
    }
}

#[derive(Debug, Clone)]
pub struct SnapshotState {
    entries: BTreeMap<PathBuf, SnapshotEntry>,
}

impl SnapshotState {
    pub fn is_meaningfully_different(
        &self,
        previous: &SnapshotState,
        mode: ChangeDetectionMode,
    ) -> bool {
        if self.entries.len() != previous.entries.len() {
            return true;
        }

        for (path, current) in &self.entries {
            let Some(previous_entry) = previous.entries.get(path) else {
                return true;
            };

            if !current.equivalent_to(previous_entry, mode) {
                return true;
            }
        }

        false
    }

    pub fn len(&self) -> usize {
        self.entries.len()
    }

    pub fn is_empty(&self) -> bool {
        self.entries.is_empty()
    }
}

#[derive(Debug, Clone)]
struct SnapshotEntry {
    kind: SnapshotEntryKind,
    modified: Option<SystemTime>,
    digest: Option<blake3::Hash>,
}

impl SnapshotEntry {
    fn equivalent_to(&self, previous: &SnapshotEntry, mode: ChangeDetectionMode) -> bool {
        if self.kind != previous.kind {
            return false;
        }

        match mode {
            ChangeDetectionMode::ContentHash => self.digest == previous.digest,
            ChangeDetectionMode::MtimeOnly => self.modified == previous.modified,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SnapshotEntryKind {
    File,
    Directory,
    Missing,
}

impl fmt::Display for SnapshotEntryKind {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::File => write!(f, "file"),
            Self::Directory => write!(f, "directory"),
            Self::Missing => write!(f, "missing"),
        }
    }
}

pub fn capture_snapshot(inputs: &[WatchInput], mode: ChangeDetectionMode) -> Result<SnapshotState> {
    let mut entries = BTreeMap::new();

    for input in inputs {
        match input {
            WatchInput::Path {
                path,
                snapshot_mode,
                ..
            } => {
                capture_path_input(path, *snapshot_mode, mode, &mut entries)?;
            }
            WatchInput::Glob {
                absolute_pattern,
                watch_anchor,
                ..
            } => {
                capture_glob_input(absolute_pattern, watch_anchor, mode, &mut entries)?;
            }
        }
    }

    Ok(SnapshotState { entries })
}

fn capture_path_input(
    path: &Path,
    snapshot_mode: PathSnapshotMode,
    mode: ChangeDetectionMode,
    entries: &mut BTreeMap<PathBuf, SnapshotEntry>,
) -> Result<()> {
    if !path.exists() {
        insert_missing_entry(path, snapshot_mode, mode, entries);
        return Ok(());
    }

    let metadata = fs::metadata(path).map_err(|source| WithWatchError::Metadata {
        path: path.to_path_buf(),
        source,
    })?;

    match snapshot_mode {
        PathSnapshotMode::ContentPath | PathSnapshotMode::MetadataPath => {
            insert_existing_entry(path, &metadata, snapshot_mode, mode, entries)?;
        }
        PathSnapshotMode::ContentTree | PathSnapshotMode::MetadataTree => {
            if metadata.is_dir() {
                capture_directory_tree(path, snapshot_mode, mode, entries)?;
            } else {
                insert_existing_entry(path, &metadata, snapshot_mode, mode, entries)?;
            }
        }
        PathSnapshotMode::MetadataChildren => {
            if metadata.is_dir() {
                capture_directory_children(path, &metadata, snapshot_mode, mode, entries)?;
            } else {
                insert_existing_entry(path, &metadata, snapshot_mode, mode, entries)?;
            }
        }
    }

    Ok(())
}

fn capture_glob_input(
    absolute_pattern: &str,
    watch_anchor: &Path,
    mode: ChangeDetectionMode,
    entries: &mut BTreeMap<PathBuf, SnapshotEntry>,
) -> Result<()> {
    let matcher = Glob::new(absolute_pattern)
        .map_err(|error| WithWatchError::InvalidGlob {
            pattern: absolute_pattern.to_string(),
            message: error.to_string(),
        })?
        .compile_matcher();

    if !watch_anchor.exists() {
        return Ok(());
    }

    for entry in WalkDir::new(watch_anchor).follow_links(true) {
        let entry = entry.map_err(|error| WithWatchError::Metadata {
            path: watch_anchor.to_path_buf(),
            source: std::io::Error::other(error.to_string()),
        })?;
        let path = entry.path().to_path_buf();
        let normalized = normalize_path_string(&path);
        if matcher.is_match(&normalized) {
            let metadata = fs::metadata(&path).map_err(|source| WithWatchError::Metadata {
                path: path.clone(),
                source,
            })?;
            insert_existing_entry(
                &path,
                &metadata,
                PathSnapshotMode::ContentTree,
                mode,
                entries,
            )?;
        }
    }

    Ok(())
}

fn capture_directory_tree(
    path: &Path,
    snapshot_mode: PathSnapshotMode,
    mode: ChangeDetectionMode,
    entries: &mut BTreeMap<PathBuf, SnapshotEntry>,
) -> Result<()> {
    for entry in WalkDir::new(path).follow_links(true) {
        let entry = entry.map_err(|error| WithWatchError::Metadata {
            path: path.to_path_buf(),
            source: std::io::Error::other(error.to_string()),
        })?;
        let entry_path = entry.path().to_path_buf();
        let metadata = fs::metadata(&entry_path).map_err(|source| WithWatchError::Metadata {
            path: entry_path.clone(),
            source,
        })?;
        insert_existing_entry(&entry_path, &metadata, snapshot_mode, mode, entries)?;
    }

    Ok(())
}

fn capture_directory_children(
    path: &Path,
    metadata: &fs::Metadata,
    snapshot_mode: PathSnapshotMode,
    mode: ChangeDetectionMode,
    entries: &mut BTreeMap<PathBuf, SnapshotEntry>,
) -> Result<()> {
    insert_existing_entry(path, metadata, snapshot_mode, mode, entries)?;

    let read_dir = fs::read_dir(path).map_err(|source| WithWatchError::Metadata {
        path: path.to_path_buf(),
        source,
    })?;
    for entry in read_dir {
        let entry = entry.map_err(|source| WithWatchError::Metadata {
            path: path.to_path_buf(),
            source,
        })?;
        let entry_path = entry.path();
        let child_metadata =
            fs::metadata(&entry_path).map_err(|source| WithWatchError::Metadata {
                path: entry_path.clone(),
                source,
            })?;
        insert_existing_entry(&entry_path, &child_metadata, snapshot_mode, mode, entries)?;
    }

    Ok(())
}

fn insert_existing_entry(
    path: &Path,
    metadata: &fs::Metadata,
    snapshot_mode: PathSnapshotMode,
    mode: ChangeDetectionMode,
    entries: &mut BTreeMap<PathBuf, SnapshotEntry>,
) -> Result<()> {
    let kind = if metadata.is_dir() {
        SnapshotEntryKind::Directory
    } else {
        SnapshotEntryKind::File
    };
    let modified = snapshot_entry_modified(kind, metadata);
    let size = snapshot_entry_size(kind, metadata);
    let digest = snapshot_digest(path, kind, modified, size, snapshot_mode, mode)?;

    entries.insert(
        path.to_path_buf(),
        SnapshotEntry {
            kind,
            modified,
            digest,
        },
    );

    Ok(())
}

fn insert_missing_entry(
    path: &Path,
    snapshot_mode: PathSnapshotMode,
    mode: ChangeDetectionMode,
    entries: &mut BTreeMap<PathBuf, SnapshotEntry>,
) {
    let digest = if mode == ChangeDetectionMode::ContentHash {
        Some(hash_metadata_tuple(
            SnapshotEntryKind::Missing,
            None,
            None,
            snapshot_mode,
        ))
    } else {
        None
    };

    entries.insert(
        path.to_path_buf(),
        SnapshotEntry {
            kind: SnapshotEntryKind::Missing,
            modified: None,
            digest,
        },
    );
}

fn snapshot_entry_size(kind: SnapshotEntryKind, metadata: &fs::Metadata) -> Option<u64> {
    match kind {
        SnapshotEntryKind::File => Some(metadata.len()),
        SnapshotEntryKind::Directory | SnapshotEntryKind::Missing => None,
    }
}

fn snapshot_entry_modified(kind: SnapshotEntryKind, metadata: &fs::Metadata) -> Option<SystemTime> {
    match kind {
        SnapshotEntryKind::File => metadata.modified().ok(),
        SnapshotEntryKind::Directory | SnapshotEntryKind::Missing => None,
    }
}

fn snapshot_digest(
    path: &Path,
    kind: SnapshotEntryKind,
    modified: Option<SystemTime>,
    size: Option<u64>,
    snapshot_mode: PathSnapshotMode,
    mode: ChangeDetectionMode,
) -> Result<Option<blake3::Hash>> {
    if mode != ChangeDetectionMode::ContentHash {
        return Ok(None);
    }

    match snapshot_mode {
        PathSnapshotMode::ContentPath | PathSnapshotMode::ContentTree => {
            if kind == SnapshotEntryKind::File {
                Ok(Some(hash_file(path)?))
            } else {
                Ok(None)
            }
        }
        PathSnapshotMode::MetadataPath
        | PathSnapshotMode::MetadataChildren
        | PathSnapshotMode::MetadataTree => Ok(Some(hash_metadata_tuple(
            kind,
            modified,
            size,
            snapshot_mode,
        ))),
    }
}

fn hash_file(path: &Path) -> Result<blake3::Hash> {
    let mut file = File::open(path).map_err(|source| WithWatchError::HashRead {
        path: path.to_path_buf(),
        source,
    })?;
    let mut hasher = blake3::Hasher::new();
    let mut buffer = [0u8; 8192];

    loop {
        let bytes_read = file
            .read(&mut buffer)
            .map_err(|source| WithWatchError::HashRead {
                path: path.to_path_buf(),
                source,
            })?;
        if bytes_read == 0 {
            break;
        }
        hasher.update(&buffer[..bytes_read]);
    }

    Ok(hasher.finalize())
}

fn hash_metadata_tuple(
    kind: SnapshotEntryKind,
    modified: Option<SystemTime>,
    size: Option<u64>,
    snapshot_mode: PathSnapshotMode,
) -> blake3::Hash {
    let mut hasher = blake3::Hasher::new();
    hasher.update(snapshot_mode.as_str().as_bytes());
    hasher.update(kind.to_string().as_bytes());

    if let Some(modified) = modified {
        let (sign, duration) = if let Ok(duration) = modified.duration_since(UNIX_EPOCH) {
            (0u8, duration)
        } else {
            (
                1u8,
                UNIX_EPOCH
                    .duration_since(modified)
                    .unwrap_or(Duration::ZERO),
            )
        };
        hasher.update(&[sign]);
        hasher.update(&duration.as_secs().to_le_bytes());
        hasher.update(&duration.subsec_nanos().to_le_bytes());
    } else {
        hasher.update(&[2u8]);
    }

    match size {
        Some(size) => {
            hasher.update(&[1u8]);
            hasher.update(&size.to_le_bytes());
        }
        None => {
            hasher.update(&[0u8]);
        }
    }

    hasher.finalize()
}

fn default_path_snapshot_mode(path: &Path) -> PathSnapshotMode {
    match fs::metadata(path) {
        Ok(metadata) if metadata.is_dir() => PathSnapshotMode::ContentTree,
        Ok(_) | Err(_) => PathSnapshotMode::ContentPath,
    }
}

pub(crate) fn absolutize(raw: &str, cwd: &Path) -> PathBuf {
    let expanded = expand_tilde(raw);
    let path = PathBuf::from(expanded);
    if path.is_absolute() {
        path
    } else {
        cwd.join(path)
    }
}

fn expand_tilde(raw: &str) -> String {
    if let Some(suffix) = raw.strip_prefix("~/") {
        if let Ok(home) = std::env::var("HOME") {
            return format!("{home}/{suffix}");
        }
    }
    raw.to_string()
}

fn nearest_existing_parent(path: &Path) -> Option<PathBuf> {
    let mut current = Some(path);
    while let Some(candidate) = current {
        if candidate.exists() {
            return Some(candidate.to_path_buf());
        }
        current = candidate.parent();
    }
    None
}

fn path_watch_anchor(path: &Path) -> Option<PathBuf> {
    let nearest = nearest_existing_parent(path)?;
    if nearest.is_dir() {
        return Some(nearest);
    }

    // Watch the containing directory for file inputs so replace-style writers such
    // as GNU `sed -i` do not orphan the watch after swapping the inode.
    nearest.parent().map(Path::to_path_buf)
}

fn glob_anchor(raw: &str, cwd: &Path) -> PathBuf {
    let expanded = expand_tilde(raw);
    let original_path = PathBuf::from(&expanded);
    let is_absolute = original_path.is_absolute();
    let mut prefix = PathBuf::new();

    for component in expanded.split(['/', '\\']) {
        if component.is_empty() {
            continue;
        }
        if component.contains('*') || component.contains('?') || component.contains('[') {
            break;
        }
        prefix.push(component);
    }

    if prefix.as_os_str().is_empty() {
        if is_absolute {
            PathBuf::from(std::path::MAIN_SEPARATOR.to_string())
        } else {
            cwd.to_path_buf()
        }
    } else if is_absolute {
        PathBuf::from(std::path::MAIN_SEPARATOR.to_string()).join(prefix)
    } else {
        cwd.join(prefix)
    }
}

fn normalize_path_string(path: &Path) -> String {
    path.to_string_lossy().replace('\\', "/")
}

#[cfg(test)]
mod tests {
    use std::{fs, thread, time::Duration};

    use super::{
        capture_snapshot, ChangeDetectionMode, PathSnapshotMode, SnapshotEntryKind, WatchInput,
        WatchInputKind,
    };

    #[test]
    fn glob_inputs_anchor_to_existing_parent() {
        let temp_dir = tempfile::tempdir().expect("create tempdir");
        let input = WatchInput::glob("src/**/*.rs", temp_dir.path()).expect("glob input");

        match input {
            WatchInput::Glob { watch_anchor, .. } => {
                assert_eq!(watch_anchor, temp_dir.path());
            }
            other => panic!("unexpected watch input: {other:?}"),
        }
    }

    #[test]
    fn path_inputs_anchor_to_parent_directory_for_files() {
        let temp_dir = tempfile::tempdir().expect("create tempdir");
        let input_path = temp_dir.path().join("input.txt");
        fs::write(&input_path, "alpha\n").expect("write file");

        let input = WatchInput::path(
            input_path.to_string_lossy().as_ref(),
            temp_dir.path(),
            WatchInputKind::Inferred,
        )
        .expect("path input");

        match input {
            WatchInput::Path { watch_anchor, .. } => {
                assert_eq!(watch_anchor, temp_dir.path());
            }
            other => panic!("unexpected watch input: {other:?}"),
        }
    }

    #[test]
    fn hash_mode_ignores_metadata_only_churn() {
        let temp_dir = tempfile::tempdir().expect("create tempdir");
        let file_path = temp_dir.path().join("input.txt");
        fs::write(&file_path, "hello").expect("write file");
        let input = WatchInput::path(
            file_path.to_string_lossy().as_ref(),
            temp_dir.path(),
            WatchInputKind::Explicit,
        )
        .expect("path input");

        let first = capture_snapshot(
            std::slice::from_ref(&input),
            ChangeDetectionMode::ContentHash,
        )
        .expect("first snapshot");
        thread::sleep(Duration::from_millis(20));
        fs::write(&file_path, "hello").expect("rewrite same content");
        let second =
            capture_snapshot(&[input], ChangeDetectionMode::ContentHash).expect("second snapshot");

        assert!(!second.is_meaningfully_different(&first, ChangeDetectionMode::ContentHash));
    }

    #[test]
    fn mtime_mode_detects_metadata_only_churn() {
        let temp_dir = tempfile::tempdir().expect("create tempdir");
        let file_path = temp_dir.path().join("input.txt");
        fs::write(&file_path, "hello").expect("write file");
        let input = WatchInput::path(
            file_path.to_string_lossy().as_ref(),
            temp_dir.path(),
            WatchInputKind::Explicit,
        )
        .expect("path input");

        let first = capture_snapshot(std::slice::from_ref(&input), ChangeDetectionMode::MtimeOnly)
            .expect("first snapshot");
        thread::sleep(Duration::from_millis(20));
        fs::write(&file_path, "hello").expect("rewrite same content");
        let second =
            capture_snapshot(&[input], ChangeDetectionMode::MtimeOnly).expect("second snapshot");

        assert!(second.is_meaningfully_different(&first, ChangeDetectionMode::MtimeOnly));
    }

    #[test]
    fn missing_paths_are_captured_explicitly() {
        let temp_dir = tempfile::tempdir().expect("create tempdir");
        let input = WatchInput::path("missing.txt", temp_dir.path(), WatchInputKind::Explicit)
            .expect("path input");
        let snapshot =
            capture_snapshot(&[input], ChangeDetectionMode::ContentHash).expect("capture snapshot");

        assert_eq!(snapshot.len(), 1);
        let entry = snapshot.entries.values().next().expect("snapshot entry");
        assert_eq!(entry.kind, SnapshotEntryKind::Missing);
    }

    #[test]
    fn metadata_children_excludes_nested_descendants() {
        let temp_dir = tempfile::tempdir().expect("create tempdir");
        let root = temp_dir.path().join("root");
        fs::create_dir_all(root.join("nested")).expect("create nested dir");
        fs::write(root.join("direct.txt"), "alpha").expect("write direct child");
        fs::write(root.join("nested").join("deep.txt"), "beta").expect("write nested child");

        let input =
            metadata_path_input(temp_dir.path(), "root", PathSnapshotMode::MetadataChildren);
        let snapshot =
            capture_snapshot(&[input], ChangeDetectionMode::ContentHash).expect("capture snapshot");

        assert!(snapshot.entries.contains_key(&root));
        assert!(snapshot.entries.contains_key(&root.join("direct.txt")));
        assert!(snapshot.entries.contains_key(&root.join("nested")));
        assert!(!snapshot
            .entries
            .contains_key(&root.join("nested").join("deep.txt")));
    }

    #[test]
    fn metadata_tree_includes_nested_descendants() {
        let temp_dir = tempfile::tempdir().expect("create tempdir");
        let root = temp_dir.path().join("root");
        fs::create_dir_all(root.join("nested")).expect("create nested dir");
        fs::write(root.join("nested").join("deep.txt"), "beta").expect("write nested child");

        let input = metadata_path_input(temp_dir.path(), "root", PathSnapshotMode::MetadataTree);
        let snapshot =
            capture_snapshot(&[input], ChangeDetectionMode::ContentHash).expect("capture snapshot");

        assert!(snapshot.entries.contains_key(&root));
        assert!(snapshot.entries.contains_key(&root.join("nested")));
        assert!(snapshot
            .entries
            .contains_key(&root.join("nested").join("deep.txt")));
    }

    #[test]
    fn metadata_listing_hash_mode_tracks_metadata_without_file_content_hashing() {
        let temp_dir = tempfile::tempdir().expect("create tempdir");
        let root = temp_dir.path().join("root");
        fs::create_dir_all(&root).expect("create root");
        let file_path = root.join("file.txt");
        fs::write(&file_path, "hello").expect("write file");

        let input =
            metadata_path_input(temp_dir.path(), "root", PathSnapshotMode::MetadataChildren);
        let first = capture_snapshot(
            std::slice::from_ref(&input),
            ChangeDetectionMode::ContentHash,
        )
        .expect("first snapshot");

        thread::sleep(Duration::from_millis(20));
        fs::write(&file_path, "hello").expect("rewrite same content");

        let second =
            capture_snapshot(&[input], ChangeDetectionMode::ContentHash).expect("second snapshot");

        assert!(second.is_meaningfully_different(&first, ChangeDetectionMode::ContentHash));
    }

    fn metadata_path_input(
        cwd: &std::path::Path,
        raw: &str,
        snapshot_mode: PathSnapshotMode,
    ) -> WatchInput {
        WatchInput::path_with_snapshot_mode(raw, cwd, WatchInputKind::Explicit, snapshot_mode)
            .expect("metadata path input")
    }
}
