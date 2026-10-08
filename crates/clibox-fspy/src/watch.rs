//! Dependency extraction and loss-aware native file watching.

use std::{
    collections::{BTreeSet, HashMap, VecDeque},
    path::{Component, Path, PathBuf},
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
    links: BTreeSet<PathBuf>,
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
    // CompareStringOrdinal follows Windows' case-insensitive filesystem
    // spelling.
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

fn normalize_absolute_target(root: &Path, target: &Path) -> Option<PathBuf> {
    if path_prefix(root, target) {
        return Some(target.to_path_buf());
    }
    if !target.is_absolute() {
        return Some(target.to_path_buf());
    }

    // Resolve only the outer prefix that names the canonical root. Resolving
    // the full target would hide nested links that must remain watchable.
    let prefix = target
        .ancestors()
        .collect::<Vec<_>>()
        .into_iter()
        .rev()
        .find(|prefix| {
            std::fs::canonicalize(prefix).is_ok_and(|resolved| same_path(&resolved, root))
        })?;
    let suffix = target.strip_prefix(prefix).ok()?;
    Some(root.join(suffix))
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
    fn include_links(&mut self, root: &Path, relative: &Path) {
        if self.files.contains(relative)
            || self.directories.contains(relative)
            || self.absent.contains(relative)
        {
            return;
        }
        // Walk only this input's components, including link targets. Retain
        // each link's own directory entry rather than its resolved directory:
        // a nonrecursive watch there must see replacement of the link itself.
        let mut pending = relative
            .components()
            .map(|component| component.as_os_str().to_os_string())
            .collect::<VecDeque<_>>();
        let mut cursor = PathBuf::new();
        let mut hops = 0;
        while let Some(component) = pending.pop_front() {
            match Path::new(&component).components().next() {
                Some(Component::CurDir) => continue,
                Some(Component::ParentDir) => {
                    if !cursor.pop() {
                        return;
                    }
                    continue;
                }
                Some(Component::Normal(_)) => cursor.push(component),
                _ => return,
            }
            let Ok(metadata) = std::fs::symlink_metadata(root.join(&cursor)) else {
                // Missing leaves retain the links already encountered; the
                // absent-path dependency supplies their nearest live anchor.
                return;
            };
            if !metadata.file_type().is_symlink() {
                continue;
            }
            self.links.insert(cursor.clone());
            hops += 1;
            if hops > 40 {
                return;
            }
            let Ok(target) = std::fs::read_link(root.join(&cursor)) else {
                return;
            };
            cursor.pop();
            let target = if target.is_absolute() {
                let Some(target) = normalize_absolute_target(root, &target) else {
                    return;
                };
                cursor.clear();
                target
                    .strip_prefix(root)
                    .ok()
                    .map(Path::to_path_buf)
                    .unwrap_or_default()
            } else {
                target
            };
            for component in target.components().rev() {
                pending.push_front(component.as_os_str().to_os_string());
            }
        }
    }

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
        let selected = selector.matches(relative);
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
            if let Some(alias) = &alias {
                self.writes.insert(alias.clone());
            }
        }
        if native_result < 0 {
            // A missing queried path is a dependency because its later
            // creation can change the command's behavior.
            if selected
                && matches!(
                    operation,
                    Operation::Open | Operation::Metadata | Operation::Directory | Operation::Exec
                )
                && missing_path_error(native_error)
            {
                if let (Some(root), Some(alias)) = (root, &alias) {
                    self.include_links(root, alias);
                }
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
        // Writes to an input's ancestor can overlap notifications even when
        // the ancestor itself is excluded by the input selector.
        if !selected {
            return;
        }
        match operation {
            Operation::Read
            | Operation::PositionalRead
            | Operation::Open
            | Operation::Metadata
            | Operation::Exec => {
                if let (Some(root), Some(alias)) = (root, &alias) {
                    self.include_links(root, alias);
                }
                self.files.insert(relative);
                if let Some(alias) = alias {
                    self.files.insert(alias);
                }
            }
            Operation::Directory => {
                if let (Some(root), Some(alias)) = (root, &alias) {
                    self.include_links(root, alias);
                }
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
        tracing::debug!(
            files = dependencies.files.len(),
            links = dependencies.links.len(),
            directories = dependencies.directories.len(),
            absent = dependencies.absent.len(),
            "watch dependencies extracted"
        );
        dependencies
    }

    pub fn merge(&mut self, other: Self) {
        self.files.extend(other.files);
        self.links.extend(other.links);
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
            || self.links.iter().any(|link| same_path(link, path))
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
            .chain(&self.links)
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

// notify may deliver callbacks after a watcher is replaced while its backend
// drains. Tag each watcher lifecycle so an obsolete target watch cannot cause
// a rerun after dependencies move; the discovery generation from the run that
// just completed remains accepted so external changes during that run are not
// lost.
struct WatchEvent {
    generation: u64,
    result: notify::Result<Event>,
}

pub struct WatchSession {
    root: PathBuf,
    tx: Sender<WatchEvent>,
    rx: Receiver<WatchEvent>,
    targets: Option<RecommendedWatcher>,
    discovery: Option<RecommendedWatcher>,
    next_generation: u64,
    target_generation: Option<u64>,
    accepted_discovery_generation: Option<u64>,
    discovery_generation: Option<u64>,
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
            next_generation: 0,
            target_generation: None,
            accepted_discovery_generation: None,
            discovery_generation: None,
        }
    }

    fn watcher(&self, generation: u64) -> Result<RecommendedWatcher, WatchFailure> {
        let tx = self.tx.clone();
        notify::recommended_watcher(move |event| {
            let _ = tx.send(WatchEvent {
                generation,
                result: event,
            });
        })
        .map_err(|_| WatchFailure::WatchUnavailable)
    }

    fn next_generation(&mut self) -> u64 {
        let generation = self.next_generation;
        self.next_generation = self.next_generation.wrapping_add(1);
        generation
    }

    /// Observe the entire root only during dependency discovery. The stable
    /// idle watch is installed on the recorded dependency anchors.
    pub fn start_discovery(&mut self) -> Result<(), WatchFailure> {
        let generation = self.next_generation();
        let mut watcher = self.watcher(generation)?;
        watcher
            .watch(&self.root, RecursiveMode::Recursive)
            .map_err(|_| WatchFailure::WatchUnavailable)?;
        self.discovery = Some(watcher);
        self.discovery_generation = Some(generation);
        Ok(())
    }

    pub fn install(&mut self, dependencies: &Dependencies) -> Result<(), WatchFailure> {
        if dependencies.is_empty() {
            return Err(WatchFailure::EmptyDependencies);
        }
        let generation = self.next_generation();
        let mut watcher = self.watcher(generation)?;
        for anchor in dependencies.anchors(&self.root) {
            watcher
                .watch(&anchor, RecursiveMode::NonRecursive)
                .map_err(|_| WatchFailure::WatchUnavailable)?;
        }
        // Install replacement before dropping the old watcher; the discovery
        // watcher is still active through this handoff.
        self.target_generation = Some(generation);
        self.accepted_discovery_generation = self.discovery_generation.take();
        self.targets = Some(watcher);
        self.discovery = None;
        Ok(())
    }

    fn receive_until(
        &self,
        until: Instant,
        cancelled: &AtomicBool,
    ) -> Result<Option<notify::Result<Event>>, WatchFailure> {
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
                Ok(event)
                    if Some(event.generation) == self.target_generation
                        || Some(event.generation) == self.accepted_discovery_generation =>
                {
                    return Ok(Some(event.result));
                }
                Ok(_) => {}
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
            // notify's inotify backend includes open and close notifications in
            // every watch mask. Reads by the supervised command must not
            // restart autowatch; only create, remove, rename, and
            // content/metadata changes can invalidate the captured
            // dependency set.
            if event.kind.is_access() {
                continue;
            }
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
    fn observed_input(root: &Path, logical: &str, resolved: &str, missing: bool) -> Dependencies {
        let access = AccessPath {
            class: PathClass::Project,
            logical: native(&root.join(logical)),
            resolved: Some(native(&root.join(resolved))),
            project_relative: Some(native(Path::new(resolved))),
            identity: None,
        };
        // Ancestor links need not match the file selector themselves.
        let selector = Selector::new(&["**/input".to_owned()], &[]).unwrap();
        let mut dependencies = Dependencies::default();
        dependencies.include_path(
            &access,
            &selector,
            Observed {
                operation: Operation::Open,
                open_mutates: false,
                native_result: if missing { -1 } else { 0 },
                native_error: missing.then_some(libc::ENOENT),
            },
            Some(root),
        );
        dependencies
    }

    #[cfg(unix)]
    #[test]
    fn nested_link_ancestors_keep_their_own_parent_anchors() {
        use std::{fs, os::unix::fs::symlink};

        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().canonicalize().unwrap();
        fs::create_dir(root.join("one")).unwrap();
        fs::create_dir(root.join("two")).unwrap();
        fs::write(root.join("two/input"), b"fixture").unwrap();
        symlink("one", root.join("current")).unwrap();
        symlink("../two", root.join("one/nested")).unwrap();
        let dependencies = observed_input(&root, "current/nested/input", "two/input", false);
        assert_eq!(
            dependencies.links,
            BTreeSet::from([PathBuf::from("current"), PathBuf::from("one/nested")])
        );
        let anchors = dependencies.anchors(&root);
        assert!(anchors.contains(&root));
        assert!(anchors.contains(&root.join("one")));
        assert!(anchors.contains(&root.join("two")));
        assert!(dependencies.relevant(Path::new("current")));
        assert!(dependencies.relevant(Path::new("one/nested")));
        assert!(!dependencies.relevant(Path::new("one/sibling")));
        assert!(!dependencies.relevant(Path::new("two/sibling")));
        assert!(!dependencies.relevant(Path::new("unrelated")));
    }

    #[cfg(unix)]
    #[test]
    fn missing_leaf_keeps_ancestor_links_and_failed_run_union() {
        use std::{fs, os::unix::fs::symlink};

        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().canonicalize().unwrap();
        fs::create_dir(root.join("one")).unwrap();
        symlink("one", root.join("current")).unwrap();
        let previous = observed_input(&root, "current/input", "one/input", true);
        assert!(previous.links.contains(Path::new("current")));
        assert!(previous.anchors(&root).contains(&root));
        assert!(previous.relevant(Path::new("one/input")));
        fs::remove_file(root.join("current")).unwrap();
        let mut next = observed_input(&root, "current/input", "current/input", true);
        next.merge(previous);
        assert!(next.links.contains(Path::new("current")));
        assert!(next.relevant(Path::new("one/input")));
        symlink("one", root.join("current")).unwrap();
        fs::write(root.join("one/input"), b"fixture").unwrap();
        let recovered = observed_input(&root, "current/input", "one/input", false);
        assert!(recovered.absent.is_empty());
        assert!(recovered.links.contains(Path::new("current")));
    }

    #[cfg(unix)]
    #[test]
    fn link_target_chains_stop_at_root_and_cycles_are_bounded() {
        use std::{fs, os::unix::fs::symlink};

        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().canonicalize().unwrap();
        fs::create_dir(root.join("one")).unwrap();
        symlink("one", root.join("second")).unwrap();
        symlink(root.join("second"), root.join("current")).unwrap();
        let dependencies = observed_input(&root, "current/input", "one/input", true);
        assert!(dependencies.links.contains(Path::new("current")));
        assert!(dependencies.links.contains(Path::new("second")));
        symlink("../outside", root.join("escape")).unwrap();
        let mut bounded = Dependencies::default();
        bounded.include_links(&root, Path::new("escape/input"));
        assert_eq!(bounded.links, BTreeSet::from([PathBuf::from("escape")]));
        assert_eq!(bounded.anchors(&root), BTreeSet::from([root.clone()]));
        symlink("loop", root.join("loop")).unwrap();
        bounded.include_links(&root, Path::new("loop/input"));
        assert!(bounded.links.contains(Path::new("loop")));
        assert!(bounded.links.iter().all(|path| !path.is_absolute()));
    }

    #[cfg(unix)]
    #[test]
    fn absolute_link_targets_use_canonical_root_aliases() {
        use std::{fs, os::unix::fs::symlink};

        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().join("root");
        fs::create_dir(&root).unwrap();
        let alias = directory.path().join("alias");
        symlink(&root, &alias).unwrap();
        let root = root.canonicalize().unwrap();
        fs::create_dir(root.join("one")).unwrap();
        symlink("one", root.join("nested")).unwrap();

        symlink(alias.join("nested"), root.join("current")).unwrap();

        let mut dependencies = Dependencies::default();
        dependencies.include_links(&root, Path::new("current/input"));

        assert_eq!(
            dependencies.links,
            BTreeSet::from([PathBuf::from("current"), PathBuf::from("nested")])
        );
    }

    #[cfg(unix)]
    #[test]
    fn child_written_link_with_native_notification_is_ambiguous() {
        use std::{fs, os::unix::fs::symlink};

        use notify::EventKind;

        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().canonicalize().unwrap();
        fs::create_dir(root.join("one")).unwrap();
        symlink("one", root.join("current")).unwrap();
        let mut dependencies = observed_input(&root, "current/input", "one/input", true);
        let access = AccessPath {
            class: PathClass::Project,
            logical: native(&root.join("current")),
            resolved: Some(native(&root.join("one"))),
            project_relative: Some(native(Path::new("one"))),
            identity: None,
        };
        dependencies.include_path(
            &access,
            &Selector::new(&["**/input".to_owned()], &[]).unwrap(),
            Observed {
                operation: Operation::Mutation,
                open_mutates: false,
                native_result: 0,
                native_error: None,
            },
            Some(&root),
        );
        assert!(dependencies.self_written(Path::new("current")));
        let mut session = WatchSession::new(root.clone());
        session.target_generation = Some(0);
        session
            .tx
            .send(WatchEvent {
                generation: 0,
                result: Ok(Event::new(EventKind::Other).add_path(root.join("current"))),
            })
            .unwrap();
        assert_eq!(
            session.collect(
                &dependencies,
                Duration::from_millis(1),
                Duration::from_millis(10),
                &AtomicBool::new(false),
            ),
            Err(WatchFailure::UnsafeAmbiguity)
        );
    }

    #[cfg(unix)]
    #[test]
    fn access_only_notifications_do_not_trigger_a_rerun() {
        use notify::{event::AccessKind, EventKind};

        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().canonicalize().unwrap();
        let mut dependencies = Dependencies::default();
        dependencies.files.insert(PathBuf::from("input"));

        let mut session = WatchSession::new(root.clone());
        session.target_generation = Some(0);
        session
            .tx
            .send(WatchEvent {
                generation: 0,
                result: Ok(
                    Event::new(EventKind::Access(AccessKind::Read)).add_path(root.join("input"))
                ),
            })
            .unwrap();

        assert!(!session
            .collect(
                &dependencies,
                Duration::from_millis(1),
                Duration::from_millis(10),
                &AtomicBool::new(false),
            )
            .unwrap());
    }

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

    #[cfg(unix)]
    #[test]
    fn nofollow_external_link_entry_replacement_is_selected_exactly() {
        use std::{fs, os::unix::fs::symlink};

        use crate::unix_paths::{path_identity, resolve_final_component, FinalSymlink};
        let directory = tempfile::tempdir().unwrap();
        let base = directory.path().canonicalize().unwrap();
        let root = base.join("root");
        fs::create_dir(&root).unwrap();
        fs::write(base.join("outside"), b"outside").unwrap();
        let logical = root.join("link");
        symlink(base.join("outside"), &logical).unwrap();
        let resolved = resolve_final_component(&logical, FinalSymlink::NoFollow, |parent| {
            fs::canonicalize(parent).map(Some)
        })
        .unwrap()
        .unwrap();
        let access = AccessPath {
            class: PathClass::Project,
            logical: native(&logical),
            resolved: Some(native(&resolved)),
            project_relative: Some(native(resolved.strip_prefix(&root).unwrap())),
            identity: path_identity(&logical, FinalSymlink::NoFollow),
        };
        let selector = Selector::new(&["link".to_owned()], &[]).unwrap();
        let mut dependencies = Dependencies::default();
        dependencies.include_path(
            &access,
            &selector,
            Observed {
                operation: Operation::Metadata,
                open_mutates: false,
                native_result: 0,
                native_error: None,
            },
            Some(&root),
        );
        assert!(dependencies.relevant(Path::new("link")));
        assert!(!dependencies.relevant(Path::new("sibling")));
        assert_eq!(dependencies.files, BTreeSet::from([PathBuf::from("link")]));
        fs::write(root.join("replacement"), b"replacement").unwrap();
        fs::rename(root.join("replacement"), &logical).unwrap();
        assert_ne!(
            path_identity(&logical, FinalSymlink::NoFollow),
            access.identity
        );
        assert!(dependencies.relevant(Path::new("link")));
        let excluded = Selector::new(&["**".to_owned()], &["link".to_owned()]).unwrap();
        let mut dependencies = Dependencies::default();
        dependencies.include_path(
            &access,
            &excluded,
            Observed {
                operation: Operation::Metadata,
                open_mutates: false,
                native_result: 0,
                native_error: None,
            },
            Some(&root),
        );
        assert!(dependencies.is_empty());
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

        let mut session = WatchSession::new(PathBuf::from("/project"));
        session.target_generation = Some(0);
        session
            .tx
            .send(WatchEvent {
                generation: 0,
                result: Ok(Event::new(EventKind::Other).set_flag(Flag::Rescan)),
            })
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
    fn stale_target_events_are_ignored_after_replacement() {
        use notify::EventKind;

        let mut session = WatchSession::new(PathBuf::from("/project"));
        session.target_generation = Some(2);
        session
            .tx
            .send(WatchEvent {
                generation: 1,
                result: Ok(Event::new(EventKind::Other).add_path(PathBuf::from("/project/input"))),
            })
            .unwrap();
        assert_eq!(
            session.collect(
                &Dependencies::default(),
                Duration::from_millis(1),
                Duration::from_millis(5),
                &AtomicBool::new(false),
            ),
            Ok(false)
        );
    }

    #[test]
    fn long_debounce_observes_cancellation() {
        use notify::EventKind;

        let mut session = WatchSession::new(PathBuf::from("/project"));
        session.target_generation = Some(0);
        session
            .tx
            .send(WatchEvent {
                generation: 0,
                result: Ok(Event::new(EventKind::Other).add_path(PathBuf::from("/project/input"))),
            })
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
