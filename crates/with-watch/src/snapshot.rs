use std::{
    collections::BTreeMap,
    fmt,
    fs::{self, File},
    io::Read,
    path::{Path, PathBuf},
    time::SystemTime,
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

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum WatchInput {
    Path {
        kind: WatchInputKind,
        path: PathBuf,
        watch_anchor: PathBuf,
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
        let watch_anchor = nearest_existing_parent(&absolute_path).ok_or_else(|| {
            WithWatchError::MissingWatchAnchor {
                path: absolute_path.clone(),
            }
        })?;

        Ok(Self::Path {
            kind,
            path: absolute_path,
            watch_anchor,
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
            ChangeDetectionMode::ContentHash => match self.kind {
                SnapshotEntryKind::File => self.digest == previous.digest,
                SnapshotEntryKind::Directory | SnapshotEntryKind::Missing => true,
            },
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
            WatchInput::Path { path, .. } => {
                capture_path_input(path, mode, &mut entries)?;
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
    mode: ChangeDetectionMode,
    entries: &mut BTreeMap<PathBuf, SnapshotEntry>,
) -> Result<()> {
    if !path.exists() {
        entries.insert(
            path.to_path_buf(),
            SnapshotEntry {
                kind: SnapshotEntryKind::Missing,
                modified: None,
                digest: None,
            },
        );
        return Ok(());
    }

    let metadata = fs::metadata(path).map_err(|source| WithWatchError::Metadata {
        path: path.to_path_buf(),
        source,
    })?;
    if metadata.is_dir() {
        for entry in WalkDir::new(path).follow_links(true) {
            let entry = entry.map_err(|error| WithWatchError::Metadata {
                path: path.to_path_buf(),
                source: std::io::Error::new(std::io::ErrorKind::Other, error.to_string()),
            })?;
            let entry_path = entry.path().to_path_buf();
            insert_existing_entry(&entry_path, mode, entries)?;
        }
    } else {
        insert_existing_entry(path, mode, entries)?;
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
            source: std::io::Error::new(std::io::ErrorKind::Other, error.to_string()),
        })?;
        let path = entry.path().to_path_buf();
        let normalized = normalize_path_string(&path);
        if matcher.is_match(&normalized) {
            insert_existing_entry(&path, mode, entries)?;
        }
    }

    Ok(())
}

fn insert_existing_entry(
    path: &Path,
    mode: ChangeDetectionMode,
    entries: &mut BTreeMap<PathBuf, SnapshotEntry>,
) -> Result<()> {
    let metadata = fs::metadata(path).map_err(|source| WithWatchError::Metadata {
        path: path.to_path_buf(),
        source,
    })?;

    let kind = if metadata.is_dir() {
        SnapshotEntryKind::Directory
    } else {
        SnapshotEntryKind::File
    };
    let modified = metadata.modified().ok();
    let digest = if mode == ChangeDetectionMode::ContentHash && kind == SnapshotEntryKind::File {
        Some(hash_file(path)?)
    } else {
        None
    };

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

fn absolutize(raw: &str, cwd: &Path) -> PathBuf {
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
        capture_snapshot, ChangeDetectionMode, SnapshotEntryKind, WatchInput, WatchInputKind,
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

        let first = capture_snapshot(&[input.clone()], ChangeDetectionMode::ContentHash)
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

        let first = capture_snapshot(&[input.clone()], ChangeDetectionMode::MtimeOnly)
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
}
