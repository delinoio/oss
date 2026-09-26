//! Dependency extraction and loss-aware native file watching.

use std::{
    collections::BTreeSet,
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

impl Dependencies {
    fn include_path(
        &mut self,
        path: &AccessPath,
        selector: &Selector,
        operation: Operation,
        native_result: i64,
        native_error: Option<i32>,
    ) {
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
            }
            Operation::Directory => {
                self.directories.insert(relative);
            }
            Operation::Close
            | Operation::Write
            | Operation::PositionalWrite
            | Operation::Mutation => {}
        }
    }

    pub fn from_record(record: &CompleteRecord, selector: &Selector) -> Self {
        let mut dependencies = Self::default();
        for pair in &record.operations {
            for path in &pair.start.paths {
                dependencies.include_path(
                    path,
                    selector,
                    pair.start.operation,
                    pair.completion.native_result,
                    pair.completion.native_error,
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
        self.files.contains(path)
            || self
                .directories
                .iter()
                .any(|directory| path.starts_with(directory) || directory.starts_with(path))
            || self
                .absent
                .iter()
                .any(|missing| path.starts_with(missing) || missing.starts_with(path))
    }

    fn self_written(&self, path: &Path) -> bool {
        self.writes.iter().any(|written| {
            written == path || written.starts_with(path) || path.starts_with(written)
        })
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
        loop {
            match self.receive_until(until, cancelled)? {
                Some(event) => events.push(event),
                None => break,
            }
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
        dependencies.include_path(&path, &selector, Operation::Exec, 0, None);
        assert!(dependencies.files.contains(Path::new("tool")));
        assert!(!dependencies.is_empty());

        let mut missing = Dependencies::default();
        #[cfg(unix)]
        let missing_error = libc::ENOENT;
        #[cfg(windows)]
        let missing_error = 0xc0000034_u32 as i32;
        missing.include_path(&path, &selector, Operation::Exec, -1, Some(missing_error));
        assert!(missing.absent.contains(Path::new("tool")));
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
