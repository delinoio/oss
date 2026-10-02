//! Dependency extraction and loss-aware native file watching.

use std::{
    collections::{BTreeSet, HashMap},
    path::{Path, PathBuf},
    sync::{
        atomic::{AtomicBool, Ordering},
        mpsc::{self, Receiver, RecvTimeoutError, Sender},
    },
    time::{Duration, Instant},
};

use notify::{Event, RecommendedWatcher, RecursiveMode, Watcher};

use crate::{
    coverage::Selector,
    record::{AccessPath, CompleteRecord, NativePath, Operation, PathClass},
};

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum WatchFailure {
    EmptyDependencies,
    WatchUnavailable,
    WatchLoss,
    UnsafeAmbiguity,
}

impl std::fmt::Display for WatchFailure {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(match self {
            Self::EmptyDependencies => "no_watchable_input: adjust --include and --exclude",
            Self::WatchUnavailable => "watch_unavailable",
            Self::WatchLoss => "watch_event_loss",
            Self::UnsafeAmbiguity => "self_write_ambiguity",
        })
    }
}

#[derive(Debug, Clone, Default)]
pub struct Dependencies {
    files: BTreeSet<PathBuf>,
    directories: BTreeSet<PathBuf>,
    absent: BTreeSet<PathBuf>,
    writes: BTreeSet<PathBuf>,
    equivalent_prefixes: HashMap<PathBuf, bool>,
}

#[derive(Clone, Copy)]
struct Observed {
    operation: Operation,
    open_mutates: bool,
    native_result: i64,
    native_error: Option<i32>,
}

fn native_relative(path: &NativePath) -> Option<PathBuf> {
    #[cfg(unix)]
    {
        use std::{ffi::OsStr, os::unix::ffi::OsStrExt};
        match path {
            NativePath::UnixBytes(bytes) => Some(PathBuf::from(OsStr::from_bytes(bytes))),
            NativePath::WindowsUtf16(_) => None,
        }
    }
    #[cfg(windows)]
    {
        use std::{ffi::OsString, os::windows::ffi::OsStringExt};
        match path {
            NativePath::WindowsUtf16(units) => Some(PathBuf::from(OsString::from_wide(units))),
            NativePath::UnixBytes(_) => None,
        }
    }
}

#[cfg(windows)]
fn same_component(left: &std::ffi::OsStr, right: &std::ffi::OsStr) -> bool {
    use std::os::windows::ffi::OsStrExt;

    let left = left.encode_wide().collect::<Vec<_>>();
    let right = right.encode_wide().collect::<Vec<_>>();
    let (Ok(left_len), Ok(right_len)) = (i32::try_from(left.len()), i32::try_from(right.len()))
    else {
        return false;
    };
    // CompareStringOrdinal follows Windows' case-insensitive filesystem spelling.
    unsafe {
        winapi::um::stringapiset::CompareStringOrdinal(
            left.as_ptr(),
            left_len,
            right.as_ptr(),
            right_len,
            1,
        ) == 2
    }
}

#[cfg(windows)]
fn path_prefix(prefix: &Path, path: &Path) -> bool {
    let mut path = path.components();
    prefix.components().all(|component| {
        path.next()
            .is_some_and(|other| same_component(component.as_os_str(), other.as_os_str()))
    })
}

#[cfg(not(windows))]
fn path_prefix(prefix: &Path, path: &Path) -> bool {
    path.starts_with(prefix)
}

fn same_path(left: &Path, right: &Path) -> bool {
    path_prefix(left, right) && path_prefix(right, left)
}

#[cfg(test)]
fn logical_relative(path: &AccessPath, root: &Path) -> Option<PathBuf> {
    let mut equivalent_prefixes = HashMap::new();
    logical_relative_with_cache(path, root, &mut equivalent_prefixes)
}

fn logical_relative_with_cache(
    path: &AccessPath,
    root: &Path,
    equivalent_prefixes: &mut HashMap<PathBuf, bool>,
) -> Option<PathBuf> {
    #[cfg(not(windows))]
    let logical = native_relative(&path.logical)?;
    #[cfg(windows)]
    let logical = crate::windows::watch_logical_path(&path.logical)?;
    let prefix = if path_prefix(root, &logical) {
        root
    } else {
        // Resolve only ancestors that could name the captured root. Resolving
        // the full input would replace an internal alias with its target.
        if !logical.is_absolute() {
            return None;
        }
        // Prefer the outermost equivalent prefix, retaining even an internal
        // directory alias that happens to point back to the root itself.
        let prefix = logical
            .ancestors()
            .skip(1)
            .collect::<Vec<_>>()
            .into_iter()
            .rev()
            .find(|prefix| {
                *equivalent_prefixes
                    .entry(prefix.to_path_buf())
                    .or_insert_with(|| {
                        std::fs::canonicalize(prefix)
                            .is_ok_and(|resolved| same_path(&resolved, root))
                    })
            })?;
        tracing::debug!(
            stage = "watch_alias_prefix",
            "verified equivalent root prefix"
        );
        prefix
    };
    let relative = logical
        .components()
        .skip(prefix.components().count())
        .collect::<PathBuf>();
    if relative.as_os_str().is_empty()
        || !relative
            .components()
            .all(|component| matches!(component, std::path::Component::Normal(_)))
    {
        return None;
    }
    Some(relative)
}

impl Dependencies {
    fn include_path(
        &mut self,
        path: &AccessPath,
        selector: &Selector,
        observed: Observed,
        root: Option<&Path>,
    ) {
        let Observed {
            operation,
            open_mutates,
            native_result,
            native_error,
        } = observed;
        if path.class != PathClass::Project {
            return;
        }
        let Some(relative) = path.project_relative.as_ref() else {
            return;
        };
        if !selector.matches(relative) {
            return;
        }
        let Some(relative) = native_relative(relative) else {
            return;
        };
        let alias = root.and_then(|root| {
            logical_relative_with_cache(path, root, &mut self.equivalent_prefixes)
        });
        if matches!(
            operation,
            Operation::Write | Operation::PositionalWrite | Operation::Mutation
        ) && native_result >= 0
        {
            self.writes.insert(relative.clone());
        }
        if native_result < 0 {
            // A missing queried path is a dependency because its later
            // creation can change the command's behavior.
            if matches!(
                operation,
                Operation::Open | Operation::Metadata | Operation::Directory | Operation::Exec
            ) && missing_path_error(native_error)
            {
                self.absent.insert(relative);
                if let Some(alias) = alias {
                    self.absent.insert(alias);
                }
            }
            return;
        }
        if operation == Operation::Open && open_mutates {
            self.writes.insert(relative);
            if let Some(alias) = alias {
                self.writes.insert(alias);
            }
            return;
        }
        match operation {
            Operation::Read
            | Operation::PositionalRead
            | Operation::Open
            | Operation::Metadata
            | Operation::Exec => {
                self.files.insert(relative);
                if let Some(alias) = alias {
                    self.files.insert(alias);
                }
            }
            Operation::Directory => {
                self.directories.insert(relative);
                if let Some(alias) = alias {
                    self.directories.insert(alias);
                }
            }
            Operation::Close
            | Operation::Write
            | Operation::PositionalWrite
            | Operation::Mutation => {}
        }
    }

    pub fn from_record(record: &CompleteRecord, selector: &Selector) -> Self {
        let mut dependencies = Self::default();
        let root = native_relative(&record.header.root);
        for pair in &record.operations {
            for path in &pair.start.paths {
                dependencies.include_path(
                    path,
                    selector,
                    Observed {
                        operation: pair.start.operation,
                        open_mutates: pair.start.open_mutates,
                        native_result: pair.completion.native_result,
                        native_error: pair.completion.native_error,
                    },
                    root.as_deref(),
                );
            }
        }
        dependencies
    }

    pub fn merge(&mut self, other: Self) {
        self.files.extend(other.files);
        self.directories.extend(other.directories);
        self.absent.extend(other.absent);
    }

    pub fn is_empty(&self) -> bool {
        self.files.is_empty() && self.directories.is_empty() && self.absent.is_empty()
    }

    fn relevant(&self, path: &Path) -> bool {
        self.files
            .iter()
            .any(|file| same_path(file, path) || path_prefix(path, file))
            || self
                .directories
                .iter()
                .any(|directory| path_prefix(directory, path) || path_prefix(path, directory))
            || self
                .absent
                .iter()
                .any(|missing| path_prefix(missing, path) || path_prefix(path, missing))
    }

    fn self_written(&self, path: &Path) -> bool {
        self.writes
            .iter()
            .any(|written| path_prefix(written, path) || path_prefix(path, written))
    }

    fn anchors(&self, root: &Path) -> BTreeSet<PathBuf> {
        let mut anchors = BTreeSet::new();
        for relative in self
            .files
            .iter()
            .chain(&self.directories)
            .chain(&self.absent)
        {
            let path = root.join(relative);
            let mut anchor = if self.directories.contains(relative) && path.is_dir() {
                path.clone()
            } else {
                path.parent().unwrap_or(root).to_path_buf()
            };
            while !anchor.is_dir() && anchor.starts_with(root) {
                anchor = anchor.parent().unwrap_or(root).to_path_buf();
            }
            if anchor.starts_with(root) {
                anchors.insert(anchor);
            }
            // A nonrecursive watch on a symlinked directory follows its
            // target. Also watch each lexical parent so replacing the link
            // itself invalidates dependencies beneath that suffix.
            let mut component_path = root.to_path_buf();
            for component in relative.components() {
                let std::path::Component::Normal(component) = component else {
                    break;
                };
                component_path.push(component);
                if std::fs::symlink_metadata(&component_path)
                    .is_ok_and(|metadata| metadata.file_type().is_symlink())
                {
                    if let Some(parent) = component_path.parent() {
                        if parent.starts_with(root) {
                            anchors.insert(parent.to_path_buf());
                        }
                    }
                }
            }
            // Directory deletion must also be noticed through its parent.
            if self.directories.contains(relative) {
                anchors.insert(path.parent().unwrap_or(root).to_path_buf());
            }
        }
        anchors
    }
}

#[cfg(unix)]
fn missing_path_error(error: Option<i32>) -> bool {
    matches!(error, Some(libc::ENOENT | libc::ENOTDIR))
}

#[cfg(windows)]
fn missing_path_error(error: Option<i32>) -> bool {
    // STATUS_OBJECT_NAME_NOT_FOUND, STATUS_OBJECT_PATH_NOT_FOUND, and
    // STATUS_OBJECT_PATH_SYNTAX_BAD are the NT equivalents of an absent input.
    matches!(
        error.map(|status| status as u32),
        Some(0xc0000034 | 0xc000003a | 0xc000003b)
    )
}

type WatchEvent = notify::Result<Event>;

pub struct WatchSession {
    root: PathBuf,
    tx: Sender<WatchEvent>,
    rx: Receiver<WatchEvent>,
    targets: Option<RecommendedWatcher>,
    discovery: Option<RecommendedWatcher>,
}

impl WatchSession {
    pub fn new(root: PathBuf) -> Self {
        let (tx, rx) = mpsc::channel();
        Self {
            root,
            tx,
            rx,
            targets: None,
            discovery: None,
        }
    }

    fn watcher(&self) -> Result<RecommendedWatcher, WatchFailure> {
        let tx = self.tx.clone();
        notify::recommended_watcher(move |event| {
            let _ = tx.send(event);
        })
        .map_err(|_| WatchFailure::WatchUnavailable)
    }

    /// Observe the entire root only during dependency discovery. The stable
    /// idle watch is installed on the recorded dependency anchors.
    pub fn start_discovery(&mut self) -> Result<(), WatchFailure> {
        let mut watcher = self.watcher()?;
        watcher
            .watch(&self.root, RecursiveMode::Recursive)
            .map_err(|_| WatchFailure::WatchUnavailable)?;
        self.discovery = Some(watcher);
        Ok(())
    }

    pub fn install(&mut self, dependencies: &Dependencies) -> Result<(), WatchFailure> {
        if dependencies.is_empty() {
            return Err(WatchFailure::EmptyDependencies);
        }
        let mut watcher = self.watcher()?;
        for anchor in dependencies.anchors(&self.root) {
            watcher
                .watch(&anchor, RecursiveMode::NonRecursive)
                .map_err(|_| WatchFailure::WatchUnavailable)?;
        }
        // Install replacement before dropping the old watcher; the discovery
        // watcher is still active through this handoff.
        self.targets = Some(watcher);
        self.discovery = None;
        Ok(())
    }

    fn receive_until(
        &self,
        until: Instant,
        cancelled: &AtomicBool,
    ) -> Result<Option<WatchEvent>, WatchFailure> {
        loop {
            if cancelled.load(Ordering::SeqCst) {
                return Ok(None);
            }
            let remaining = until.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                return Ok(None);
            }
            match self
                .rx
                .recv_timeout(remaining.min(Duration::from_millis(20)))
            {
                Ok(event) => return Ok(Some(event)),
                Err(RecvTimeoutError::Timeout) => {}
                Err(RecvTimeoutError::Disconnected) => return Err(WatchFailure::WatchLoss),
            }
        }
    }

    pub fn collect(
        &self,
        dependencies: &Dependencies,
        debounce: Duration,
        deadline: Duration,
        cancelled: &AtomicBool,
    ) -> Result<bool, WatchFailure> {
        let first = match self.receive_until(Instant::now() + deadline, cancelled)? {
            Some(event) => event,
            None => return Ok(false),
        };
        let mut events = vec![first];
        let until = Instant::now() + debounce;
        while let Some(event) = self.receive_until(until, cancelled)? {
            events.push(event);
        }
        if cancelled.load(Ordering::SeqCst) {
            return Ok(false);
        }
        let mut relevant = false;
        for event in events {
            let event = event.map_err(|_| WatchFailure::WatchLoss)?;
            if event.need_rescan() || event.paths.is_empty() {
                return Err(WatchFailure::WatchLoss);
            }
            for path in event.paths {
                let Ok(relative) = path.strip_prefix(&self.root) else {
                    continue;
                };
                if dependencies.relevant(relative) {
                    if dependencies.self_written(relative) {
                        return Err(WatchFailure::UnsafeAmbiguity);
                    }
                    relevant = true;
                }
            }
        }
        Ok(relevant)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[cfg(unix)]
    fn native(path: &Path) -> NativePath {
        use std::os::unix::ffi::OsStrExt;
        NativePath::UnixBytes(path.as_os_str().as_bytes().to_vec())
    }

    #[cfg(windows)]
    fn native(path: &Path) -> NativePath {
        use std::os::windows::ffi::OsStrExt;
        NativePath::WindowsUtf16(path.as_os_str().encode_wide().collect())
    }

    #[test]
    fn internal_link_alias_and_target_are_both_dependencies() {
        #[cfg(unix)]
        let root = Path::new("/project");
        #[cfg(windows)]
        let root = Path::new(r"C:\project");
        let alias = Path::new("config");
        let target = Path::new("configs/dev.json");
        let access = AccessPath {
            class: PathClass::Project,
            logical: native(&root.join(alias)),
            resolved: Some(native(&root.join(target))),
            project_relative: Some(native(target)),
            identity: None,
        };
        let selector = Selector::new(&["**".to_owned()], &[]).unwrap();
        let mut dependencies = Dependencies::default();
        dependencies.include_path(
            &access,
            &selector,
            Observed {
                operation: Operation::Read,
                open_mutates: false,
                native_result: 1,
                native_error: None,
            },
            Some(root),
        );
        assert!(dependencies.files.contains(alias));
        assert!(dependencies.files.contains(target));
        assert!(dependencies.relevant(alias));
    }

    #[test]
    fn internal_directory_alias_and_target_are_both_dependencies() {
        #[cfg(unix)]
        let root = Path::new("/project");
        #[cfg(windows)]
        let root = Path::new(r"C:\project");
        let alias = Path::new("current");
        let target = Path::new("targets/input");
        let access = AccessPath {
            class: PathClass::Project,
            logical: native(&root.join(alias)),
            resolved: Some(native(&root.join(target))),
            project_relative: Some(native(target)),
            identity: None,
        };
        let selector = Selector::new(&["**".to_owned()], &[]).unwrap();
        let mut dependencies = Dependencies::default();
        dependencies.include_path(
            &access,
            &selector,
            Observed {
                operation: Operation::Directory,
                open_mutates: false,
                native_result: 0,
                native_error: None,
            },
            Some(root),
        );
        assert!(dependencies.directories.contains(alias));
        assert!(dependencies.directories.contains(target));
    }

    #[cfg(unix)]
    #[test]
    fn equivalent_root_prefix_keeps_lossless_alias_and_missing_suffix() {
        use std::{
            ffi::OsStr,
            fs,
            os::unix::{ffi::OsStrExt, fs::symlink},
        };

        let directory = tempfile::tempdir().unwrap();
        let base = directory.path().canonicalize().unwrap();
        let root = base.join("root");
        fs::create_dir(&root).unwrap();
        fs::write(root.join("one"), b"input").unwrap();
        let alias = Path::new("current");
        symlink("one", root.join(alias)).unwrap();
        symlink(".", root.join("again")).unwrap();
        symlink("root", base.join("outer")).unwrap();
        symlink("outer", base.join("nested")).unwrap();
        symlink(&base, base.join("parent-prefix")).unwrap();
        let selector = Selector::new(&["**".to_owned()], &[]).unwrap();
        for prefix in [
            root.clone(),
            base.join("outer"),
            base.join("nested"),
            base.join("parent-prefix/root"),
        ] {
            let access = AccessPath {
                class: PathClass::Project,
                logical: native(&prefix.join(alias)),
                resolved: Some(native(&root.join("one"))),
                project_relative: Some(native(Path::new("one"))),
                identity: None,
            };
            let original = access.logical.clone();
            let mut dependencies = Dependencies::default();
            dependencies.include_path(
                &access,
                &selector,
                Observed {
                    operation: Operation::Read,
                    open_mutates: false,
                    native_result: 1,
                    native_error: None,
                },
                Some(&root),
            );
            assert_eq!(
                dependencies.files,
                BTreeSet::from([alias.to_path_buf(), PathBuf::from("one")])
            );
            assert_eq!(access.logical, original);

            // Invalid UTF-8 need not be accepted by the host filesystem to
            // remain lossless during dependency extraction.
            let raw_alias = Path::new(OsStr::from_bytes(b"current-\xff"));
            let raw_access = AccessPath {
                logical: native(&prefix.join(raw_alias)),
                ..access.clone()
            };
            assert_eq!(
                logical_relative(&raw_access, &root),
                Some(raw_alias.to_path_buf())
            );
            assert_eq!(raw_access.logical, native(&prefix.join(raw_alias)));

            let nested_alias = AccessPath {
                logical: native(&prefix.join("again").join(alias)),
                ..access.clone()
            };
            assert_eq!(
                logical_relative(&nested_alias, &root),
                Some(PathBuf::from("again").join(alias))
            );

            let missing = AccessPath {
                logical: native(&prefix.join("missing/leaf")),
                resolved: Some(native(&root.join("missing/leaf"))),
                project_relative: Some(native(Path::new("missing/leaf"))),
                ..access
            };
            assert_eq!(
                logical_relative(&missing, &root),
                Some(PathBuf::from("missing/leaf"))
            );
        }
    }

    #[cfg(unix)]
    #[test]
    fn unverifiable_and_outside_prefixes_cannot_promote_aliases() {
        use std::{fs, os::unix::fs::symlink};

        let directory = tempfile::tempdir().unwrap();
        let base = directory.path().canonicalize().unwrap();
        let root = base.join("root");
        fs::create_dir(&root).unwrap();
        fs::create_dir(base.join("outside")).unwrap();
        symlink("outside", base.join("outer")).unwrap();
        symlink("loop", base.join("loop")).unwrap();
        let selector = Selector::new(&["**".to_owned()], &[]).unwrap();
        for logical in [
            base.join("outer/input"),
            base.join("absent/input"),
            base.join("loop/input"),
        ] {
            let access = AccessPath {
                class: PathClass::Project,
                logical: native(&logical),
                resolved: Some(native(&root.join("input"))),
                project_relative: Some(native(Path::new("input"))),
                identity: None,
            };
            assert_eq!(logical_relative(&access, &root), None);
            let mut dependencies = Dependencies::default();
            dependencies.include_path(
                &access,
                &selector,
                Observed {
                    operation: Operation::Read,
                    open_mutates: false,
                    native_result: 1,
                    native_error: None,
                },
                Some(&root),
            );
            assert_eq!(dependencies.files, BTreeSet::from([PathBuf::from("input")]));
            let external = AccessPath {
                class: PathClass::External,
                ..access
            };
            let mut dependencies = Dependencies::default();
            dependencies.include_path(
                &external,
                &selector,
                Observed {
                    operation: Operation::Read,
                    open_mutates: false,
                    native_result: 1,
                    native_error: None,
                },
                Some(&root),
            );
            assert!(dependencies.is_empty());
        }
        let escaping = AccessPath {
            class: PathClass::Project,
            logical: native(&base.join("root/../outside/input")),
            resolved: None,
            project_relative: None,
            identity: None,
        };
        assert_eq!(logical_relative(&escaping, &root), None);
    }

    #[cfg(unix)]
    #[test]
    fn symlinked_suffix_watches_lexical_parent() {
        use std::{fs, os::unix::fs::symlink};

        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().join("root");
        fs::create_dir(&root).unwrap();
        fs::create_dir(root.join("target")).unwrap();
        symlink("target", root.join("link")).unwrap();

        let mut dependencies = Dependencies::default();
        dependencies.files.insert(PathBuf::from("link/input"));
        let anchors = dependencies.anchors(&root);

        assert!(anchors.contains(&root.join("link")));
        assert!(anchors.contains(&root));
        assert!(dependencies.relevant(Path::new("link")));
    }

    #[test]
    fn project_executable_is_a_watchable_dependency() {
        #[cfg(unix)]
        let native = NativePath::UnixBytes(b"tool".to_vec());
        #[cfg(windows)]
        let native = NativePath::WindowsUtf16("tool".encode_utf16().collect());
        let path = AccessPath {
            class: PathClass::Project,
            logical: native.clone(),
            resolved: None,
            project_relative: Some(native),
            identity: None,
        };
        let selector = Selector::new(&["**".to_owned()], &[]).unwrap();
        let mut dependencies = Dependencies::default();
        dependencies.include_path(
            &path,
            &selector,
            Observed {
                operation: Operation::Exec,
                open_mutates: false,
                native_result: 0,
                native_error: None,
            },
            None,
        );
        assert!(dependencies.files.contains(Path::new("tool")));
        assert!(!dependencies.is_empty());

        let mut missing = Dependencies::default();
        #[cfg(unix)]
        let missing_error = libc::ENOENT;
        #[cfg(windows)]
        let missing_error = 0xc0000034_u32 as i32;
        missing.include_path(
            &path,
            &selector,
            Observed {
                operation: Operation::Exec,
                open_mutates: false,
                native_result: -1,
                native_error: Some(missing_error),
            },
            None,
        );
        assert!(missing.absent.contains(Path::new("tool")));
    }

    #[test]
    fn mutating_open_without_a_write_is_an_output() {
        let path = AccessPath {
            class: PathClass::Project,
            logical: native(Path::new("stamp")),
            resolved: None,
            project_relative: Some(native(Path::new("stamp"))),
            identity: None,
        };
        let selector = Selector::new(&["**".to_owned()], &[]).unwrap();
        let mut dependencies = Dependencies::default();
        dependencies.include_path(
            &path,
            &selector,
            Observed {
                operation: Operation::Open,
                open_mutates: true,
                native_result: 0,
                native_error: None,
            },
            None,
        );
        assert!(dependencies.writes.contains(Path::new("stamp")));
        assert!(!dependencies.relevant(Path::new("stamp")));
    }

    #[test]
    fn failed_run_dependency_union_preserves_old_inputs() {
        let mut next = Dependencies::default();
        next.files.insert(PathBuf::from("new.txt"));
        let mut previous = Dependencies::default();
        previous.files.insert(PathBuf::from("old.txt"));
        next.merge(previous);
        assert!(next.files.contains(Path::new("new.txt")));
        assert!(next.files.contains(Path::new("old.txt")));
    }

    #[cfg(windows)]
    #[test]
    fn missing_dependency_matches_created_path_with_different_case() {
        let mut dependencies = Dependencies::default();
        dependencies.absent.insert(PathBuf::from("Config.json"));
        assert!(dependencies.relevant(Path::new("config.json")));
        dependencies.writes.insert(PathBuf::from("Output.txt"));
        assert!(dependencies.self_written(Path::new("output.txt")));
    }

    #[test]
    fn rescan_notification_fails_instead_of_losing_changes() {
        use notify::{event::Flag, EventKind};

        let session = WatchSession::new(PathBuf::from("/project"));
        session
            .tx
            .send(Ok(Event::new(EventKind::Other).set_flag(Flag::Rescan)))
            .unwrap();
        assert_eq!(
            session.collect(
                &Dependencies::default(),
                Duration::from_millis(1),
                Duration::from_millis(1),
                &AtomicBool::new(false),
            ),
            Err(WatchFailure::WatchLoss)
        );
    }

    #[test]
    fn long_debounce_observes_cancellation() {
        use notify::EventKind;

        let session = WatchSession::new(PathBuf::from("/project"));
        session
            .tx
            .send(Ok(
                Event::new(EventKind::Other).add_path(PathBuf::from("/project/input"))
            ))
            .unwrap();
        let cancelled = AtomicBool::new(false);
        let began = Instant::now();
        std::thread::scope(|scope| {
            scope.spawn(|| {
                std::thread::sleep(Duration::from_millis(30));
                cancelled.store(true, Ordering::SeqCst);
            });
            assert_eq!(
                session.collect(
                    &Dependencies::default(),
                    Duration::from_secs(3600),
                    Duration::from_millis(100),
                    &cancelled,
                ),
                Ok(false),
            );
        });
        assert!(began.elapsed() < Duration::from_secs(1));
    }
}
