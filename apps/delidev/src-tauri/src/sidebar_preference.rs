// SPDX-License-Identifier: Apache-2.0
use std::{
    fs::{self, File, OpenOptions},
    io::{Read, Write},
    path::{Path, PathBuf},
    sync::Mutex,
};

use serde::{Deserialize, Serialize};

const DOCUMENT_LIMIT: u64 = 4096;

#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum SidebarPreference {
    #[default]
    Expanded,
    Collapsed,
}

#[derive(Clone, Copy, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum SidebarProblem {
    Unavailable,
    ReadFailed,
    InvalidDocument,
    UnsupportedVersion,
    WriteFailed,
    OutcomeUnknown,
    Changed,
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct SidebarSnapshot {
    pub revision: u32,
    pub sidebar_preference: SidebarPreference,
    pub problem: Option<SidebarProblem>,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Document {
    version: u32,
    sidebar_preference: SidebarPreference,
}

/// One process-wide device preference, independent of every server connection.
/// The lock covers inspection and atomic publication; revisions order IPC
/// replies and events even when their delivery order differs from their commit
/// order.
pub struct SidebarStore {
    path: Option<PathBuf>,
    state: Mutex<SidebarSnapshot>,
    presentation: Mutex<()>,
}

impl SidebarStore {
    pub fn new(config_dir: Option<PathBuf>) -> Self {
        Self {
            path: config_dir.map(|directory| directory.join("sidebar_preference.json")),
            presentation: Mutex::new(()),
            state: Mutex::new(SidebarSnapshot {
                revision: 0,
                sidebar_preference: SidebarPreference::Expanded,
                problem: Some(SidebarProblem::Unavailable),
            }),
        }
    }

    pub fn presentation(&self) -> std::sync::MutexGuard<'_, ()> {
        self.presentation
            .lock()
            .unwrap_or_else(|error| error.into_inner())
    }

    /// Retained presentation only; auxiliary views cannot inspect preference
    /// files.
    pub fn current(&self) -> SidebarSnapshot {
        self.state
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .clone()
    }

    pub fn read(&self) -> SidebarSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        let previous = state.clone();
        match self.path.as_deref().map(inspect) {
            Some(Ok(sidebar_preference)) => {
                state.sidebar_preference = sidebar_preference;
                state.problem = None;
            }
            Some(Err(problem)) => {
                // Invalid external storage cannot replace the last committed
                // layout.
                state.problem = Some(problem);
                tracing::warn!(operation = "sidebar_preference_read", ?problem);
            }
            None => state.problem = Some(SidebarProblem::Unavailable),
        }
        if state.revision == 0
            || state.sidebar_preference != previous.sidebar_preference
            || state.problem != previous.problem
        {
            advance(&mut state);
        }
        state.clone()
    }

    pub fn update(
        &self,
        sidebar_preference: SidebarPreference,
        expected_revision: u32,
    ) -> SidebarSnapshot {
        self.update_with(sidebar_preference, expected_revision, persist)
    }

    fn update_with(
        &self,
        sidebar_preference: SidebarPreference,
        expected_revision: u32,
        write: impl FnOnce(&Path, SidebarPreference) -> Result<(), SidebarProblem>,
    ) -> SidebarSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        if state.revision != expected_revision {
            // Do not mutate the shared committed state for a stale caller. It
            // must inspect again before choosing against the current selection.
            let mut snapshot = state.clone();
            snapshot.problem = Some(SidebarProblem::Changed);
            return snapshot;
        }
        if state.problem.is_some() || state.revision == u32::MAX {
            return state.clone();
        }
        let result = self
            .path
            .as_deref()
            .ok_or(SidebarProblem::Unavailable)
            .and_then(|path| {
                // Reinspect before replacement, preserving external
                // invalid/newer documents instead of silently
                // upgrading or overwriting them.
                let current = inspect(path)?;
                if current != state.sidebar_preference {
                    return Err(SidebarProblem::Changed);
                }
                write(path, sidebar_preference)
            });
        match result {
            Ok(()) => {
                state.sidebar_preference = sidebar_preference;
                tracing::info!(operation = "sidebar_preference_update", state = "committed");
            }
            Err(problem) => {
                state.problem = Some(problem);
                tracing::warn!(operation = "sidebar_preference_update", ?problem);
            }
        }
        advance(&mut state);
        state.clone()
    }
}

fn advance(state: &mut SidebarSnapshot) {
    // Never wrap into an older revision. Exhaustion remains read-only until
    // restart instead of letting a delayed event replace newer state.
    if let Some(revision) = state.revision.checked_add(1) {
        state.revision = revision;
    } else {
        state.problem = Some(SidebarProblem::Unavailable);
    }
}

fn inspect(path: &Path) -> Result<SidebarPreference, SidebarProblem> {
    let metadata = match fs::symlink_metadata(path) {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            return Ok(SidebarPreference::Expanded);
        }
        Err(_) => return Err(SidebarProblem::ReadFailed),
    };
    if !metadata.is_file() || metadata.len() > DOCUMENT_LIMIT {
        return Err(SidebarProblem::InvalidDocument);
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        if metadata.nlink() != 1 {
            return Err(SidebarProblem::InvalidDocument);
        }
    }
    let mut options = OpenOptions::new();
    options.read(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.custom_flags(libc::O_NOFOLLOW);
    }
    let file = options.open(path).map_err(|_| SidebarProblem::ReadFailed)?;
    let mut bytes = Vec::new();
    file.take(DOCUMENT_LIMIT + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| SidebarProblem::ReadFailed)?;
    if bytes.len() as u64 > DOCUMENT_LIMIT {
        return Err(SidebarProblem::InvalidDocument);
    }
    let document: Document =
        serde_json::from_slice(&bytes).map_err(|_| SidebarProblem::InvalidDocument)?;
    if document.version != 1 {
        return Err(SidebarProblem::UnsupportedVersion);
    }
    Ok(document.sidebar_preference)
}

fn persist(path: &Path, sidebar_preference: SidebarPreference) -> Result<(), SidebarProblem> {
    let parent = path.parent().ok_or(SidebarProblem::WriteFailed)?;
    fs::create_dir_all(parent).map_err(|_| SidebarProblem::WriteFailed)?;
    let temporary = parent.join(format!(".sidebar_preference-{}.tmp", uuid::Uuid::now_v7()));
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    // Cleanup owns only the file that this attempt created exclusively. A
    // creation failure cannot authorize deleting a pre-existing scratch file.
    let mut file = options
        .open(&temporary)
        .map_err(|_| SidebarProblem::WriteFailed)?;
    let scratch = &temporary;
    let result = (move || {
        let bytes = serde_json::to_vec(&Document {
            version: 1,
            sidebar_preference,
        })
        .map_err(|_| SidebarProblem::WriteFailed)?;
        file.write_all(&bytes)
            .and_then(|_| file.sync_all())
            .map_err(|_| SidebarProblem::WriteFailed)?;
        drop(file);
        crate::appearance::replace(scratch, path).map_err(|_| SidebarProblem::WriteFailed)?;
        // Publication happened already. A failed directory sync is uncertain,
        // so retain the prior selection and require a fresh read before retry.
        #[cfg(unix)]
        File::open(parent)
            .and_then(|directory| directory.sync_all())
            .map_err(|_| SidebarProblem::OutcomeUnknown)?;
        Ok(())
    })();
    let _ = fs::remove_file(temporary);
    result
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn invalid_external_document_retains_last_committed_layout() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("sidebar_preference.json");
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        store.update(SidebarPreference::Collapsed, initial.revision);
        fs::write(&path, b"invalid original").unwrap();
        let observed = store.read();
        assert_eq!(observed.sidebar_preference, SidebarPreference::Collapsed);
        assert_eq!(observed.problem, Some(SidebarProblem::InvalidDocument));
        store.update(SidebarPreference::Expanded, observed.revision);
        assert_eq!(fs::read(&path).unwrap(), b"invalid original");
    }

    #[test]
    fn oversized_document_and_revision_exhaustion_preserve_original_bytes() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("sidebar_preference.json");
        let original = vec![b' '; DOCUMENT_LIMIT as usize + 1];
        fs::write(&path, &original).unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let snapshot = store.read();
        assert_eq!(snapshot.problem, Some(SidebarProblem::InvalidDocument));
        store.update(SidebarPreference::Collapsed, snapshot.revision);
        assert_eq!(fs::read(&path).unwrap(), original);
        fs::remove_file(&path).unwrap();
        store.read();
        store.state.lock().unwrap().revision = u32::MAX;
        store.update(SidebarPreference::Collapsed, u32::MAX);
        assert!(!path.exists());
        assert_eq!(
            store.current().sidebar_preference,
            SidebarPreference::Expanded
        );
    }

    #[cfg(unix)]
    #[test]
    fn hard_linked_document_is_not_replaced() {
        let directory = tempfile::tempdir().unwrap();
        let source = directory.path().join("original.json");
        let original = br#"{"version":1,"sidebar_preference":"collapsed"}"#;
        fs::write(&source, original).unwrap();
        let path = directory.path().join("sidebar_preference.json");
        fs::hard_link(&source, &path).unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let snapshot = store.read();
        assert_eq!(snapshot.problem, Some(SidebarProblem::InvalidDocument));
        store.update(SidebarPreference::Expanded, snapshot.revision);
        assert_eq!(fs::read(&path).unwrap(), original);
        assert_eq!(fs::read(&source).unwrap(), original);
    }

    #[test]
    fn failed_or_uncertain_publication_requires_a_fresh_read() {
        let directory = tempfile::tempdir().unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        let dark = store.update(SidebarPreference::Collapsed, initial.revision);
        let failure = store.update_with(SidebarPreference::Expanded, dark.revision, |_, _| {
            Err(SidebarProblem::WriteFailed)
        });
        assert_eq!(failure.sidebar_preference, SidebarPreference::Collapsed);
        assert_eq!(failure.problem, Some(SidebarProblem::WriteFailed));
        assert_eq!(
            store
                .update(SidebarPreference::Expanded, failure.revision)
                .sidebar_preference,
            SidebarPreference::Collapsed
        );
        let inspected = store.read();
        let uncertain = store.update_with(
            SidebarPreference::Expanded,
            inspected.revision,
            |path, sidebar_preference| {
                persist(path, sidebar_preference)?;
                Err(SidebarProblem::OutcomeUnknown)
            },
        );
        assert_eq!(uncertain.sidebar_preference, SidebarPreference::Collapsed);
        assert_eq!(uncertain.problem, Some(SidebarProblem::OutcomeUnknown));
        assert_eq!(
            store
                .update(SidebarPreference::Collapsed, uncertain.revision)
                .sidebar_preference,
            SidebarPreference::Collapsed
        );
        let inspected = store.read();
        assert_eq!(inspected.sidebar_preference, SidebarPreference::Expanded);
        assert_eq!(inspected.problem, None);
        assert_eq!(
            store
                .update(SidebarPreference::Expanded, inspected.revision)
                .problem,
            None
        );
    }

    #[test]
    fn unchanged_reads_keep_all_windows_on_the_same_revision() {
        let directory = tempfile::tempdir().unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        assert_eq!(store.read(), initial);
        assert_eq!(
            store
                .update(SidebarPreference::Collapsed, initial.revision)
                .problem,
            None
        );
    }

    #[cfg(unix)]
    #[test]
    fn linked_storage_is_preserved_without_touching_its_target() {
        let directory = tempfile::tempdir().unwrap();
        let target = directory.path().join("external.json");
        fs::write(
            &target,
            br#"{"version":1,"sidebar_preference":"collapsed"}"#,
        )
        .unwrap();
        let original = fs::read(&target).unwrap();
        std::os::unix::fs::symlink(&target, directory.path().join("sidebar_preference.json"))
            .unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let snapshot = store.read();
        assert_eq!(snapshot.problem, Some(SidebarProblem::InvalidDocument));
        store.update(SidebarPreference::Expanded, snapshot.revision);
        assert_eq!(fs::read(target).unwrap(), original);
        assert!(
            fs::symlink_metadata(directory.path().join("sidebar_preference.json"))
                .unwrap()
                .is_symlink()
        );
    }

    #[test]
    fn default_restart_and_stale_selection() {
        let directory = tempfile::tempdir().unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        assert_eq!(initial.sidebar_preference, SidebarPreference::Expanded);
        assert_eq!(initial.problem, None);
        let dark = store.update(SidebarPreference::Collapsed, initial.revision);
        assert_eq!(dark.sidebar_preference, SidebarPreference::Collapsed);
        assert_eq!(dark.problem, None);
        assert_eq!(
            store
                .update(SidebarPreference::Expanded, initial.revision)
                .problem,
            Some(SidebarProblem::Changed)
        );
        assert_eq!(
            store.read().sidebar_preference,
            SidebarPreference::Collapsed
        );
        let restarted = SidebarStore::new(Some(directory.path().into()));
        let observed = restarted.read();
        assert_eq!(observed.sidebar_preference, SidebarPreference::Collapsed);
        assert_eq!(
            restarted
                .update(SidebarPreference::Expanded, observed.revision)
                .sidebar_preference,
            SidebarPreference::Expanded
        );
        assert_eq!(
            restarted.read().sidebar_preference,
            SidebarPreference::Expanded
        );
    }

    #[test]
    fn invalid_and_newer_documents_remain_intact() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("sidebar_preference.json");
        for bytes in [
            b"broken".as_slice(),
            br#"{"version":2,"sidebar_preference":"collapsed"}"#,
            br#"{"version":1,"sidebar_preference":"other"}"#,
            br#"{"version":1,"sidebar_preference":"collapsed","extra":true}"#,
        ] {
            fs::write(&path, bytes).unwrap();
            let store = SidebarStore::new(Some(directory.path().into()));
            let observed = store.read();
            assert_eq!(observed.sidebar_preference, SidebarPreference::Expanded);
            assert!(observed.problem.is_some());
            assert_eq!(
                store
                    .update(SidebarPreference::Expanded, observed.revision)
                    .sidebar_preference,
                SidebarPreference::Expanded
            );
            assert_eq!(fs::read(&path).unwrap(), bytes);
        }
    }

    #[test]
    fn external_change_requires_inspection_before_retry() {
        let directory = tempfile::tempdir().unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        fs::write(
            directory.path().join("sidebar_preference.json"),
            br#"{"version":1,"sidebar_preference":"collapsed"}"#,
        )
        .unwrap();
        let rejected = store.update(SidebarPreference::Expanded, initial.revision);
        assert_eq!(rejected.sidebar_preference, SidebarPreference::Expanded);
        assert_eq!(rejected.problem, Some(SidebarProblem::Changed));
        let observed = store.read();
        assert_eq!(observed.sidebar_preference, SidebarPreference::Collapsed);
        assert_eq!(
            store
                .update(SidebarPreference::Expanded, observed.revision)
                .problem,
            None
        );
    }

    #[test]
    fn write_failure_keeps_committed_selection_and_blocks_retry() {
        let directory = tempfile::tempdir().unwrap();
        let store = SidebarStore::new(Some(directory.path().into()));
        let initial = store.read();
        let committed = store.update(SidebarPreference::Collapsed, initial.revision);
        fs::remove_file(directory.path().join("sidebar_preference.json")).unwrap();
        fs::remove_dir(directory.path()).unwrap();
        fs::write(directory.path(), b"not a directory").unwrap();
        let failed = store.update(SidebarPreference::Expanded, committed.revision);
        assert_eq!(failed.sidebar_preference, SidebarPreference::Collapsed);
        assert!(failed.problem.is_some());
        assert_eq!(
            store
                .update(SidebarPreference::Expanded, failed.revision)
                .sidebar_preference,
            SidebarPreference::Collapsed
        );
        fs::remove_file(directory.path()).unwrap();
    }
}
