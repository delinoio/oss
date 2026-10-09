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
pub enum DateFormatPreference {
    #[default]
    System,
    Ymd,
    Mdy,
    Dmy,
}

#[derive(Clone, Copy, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum DateFormatProblem {
    Unavailable,
    ReadFailed,
    InvalidDocument,
    UnsupportedVersion,
    WriteFailed,
    OutcomeUnknown,
    Changed,
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct DateFormatSnapshot {
    pub revision: u32,
    pub date_format: DateFormatPreference,
    pub problem: Option<DateFormatProblem>,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Document {
    version: u32,
    date_format: DateFormatPreference,
}

/// One process-wide device preference, independent of every server connection.
/// The lock covers inspection and atomic publication; revisions order IPC
/// replies and events even when their delivery order differs from their commit
/// order.
pub struct DateFormatStore {
    path: Option<PathBuf>,
    state: Mutex<DateFormatSnapshot>,
    presentation: Mutex<()>,
}

impl DateFormatStore {
    pub fn new(config_dir: Option<PathBuf>) -> Self {
        Self {
            path: config_dir.map(|directory| directory.join("date_format.json")),
            presentation: Mutex::new(()),
            state: Mutex::new(DateFormatSnapshot {
                revision: 0,
                date_format: DateFormatPreference::System,
                problem: Some(DateFormatProblem::Unavailable),
            }),
        }
    }

    pub fn presentation(&self) -> std::sync::MutexGuard<'_, ()> {
        self.presentation
            .lock()
            .unwrap_or_else(|error| error.into_inner())
    }

    pub fn read(&self) -> DateFormatSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        let previous = state.clone();
        match self.path.as_deref().map(inspect) {
            Some(Ok(date_format)) => {
                state.date_format = date_format;
                state.problem = None;
            }
            Some(Err(problem)) => {
                if matches!(
                    problem,
                    DateFormatProblem::InvalidDocument | DateFormatProblem::UnsupportedVersion
                ) {
                    state.date_format = DateFormatPreference::System;
                }
                state.problem = Some(problem);
                tracing::warn!(operation = "date_format_read", ?problem);
            }
            None => state.problem = Some(DateFormatProblem::Unavailable),
        }
        if state.revision == 0
            || state.date_format != previous.date_format
            || state.problem != previous.problem
        {
            advance(&mut state);
        }
        state.clone()
    }

    pub fn update(
        &self,
        date_format: DateFormatPreference,
        expected_revision: u32,
    ) -> DateFormatSnapshot {
        self.update_with(date_format, expected_revision, persist)
    }

    fn update_with(
        &self,
        date_format: DateFormatPreference,
        expected_revision: u32,
        write: impl FnOnce(&Path, DateFormatPreference) -> Result<(), DateFormatProblem>,
    ) -> DateFormatSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        if state.revision != expected_revision {
            // Do not mutate the shared committed state for a stale caller. It
            // must inspect again before choosing against the current selection.
            let mut snapshot = state.clone();
            snapshot.problem = Some(DateFormatProblem::Changed);
            return snapshot;
        }
        if state.problem.is_some() || state.revision == u32::MAX {
            return state.clone();
        }
        let result = self
            .path
            .as_deref()
            .ok_or(DateFormatProblem::Unavailable)
            .and_then(|path| {
                // Reinspect before replacement, preserving external
                // invalid/newer documents instead of silently
                // upgrading or overwriting them.
                let current = inspect(path)?;
                if current != state.date_format {
                    return Err(DateFormatProblem::Changed);
                }
                write(path, date_format)
            });
        match result {
            Ok(()) => {
                state.date_format = date_format;
                tracing::info!(operation = "date_format_update", state = "committed");
            }
            Err(problem) => {
                state.problem = Some(problem);
                tracing::warn!(operation = "date_format_update", ?problem);
            }
        }
        advance(&mut state);
        state.clone()
    }
}

fn advance(state: &mut DateFormatSnapshot) {
    // Never wrap into an older revision. Exhaustion remains read-only until
    // restart instead of letting a delayed event replace newer state.
    if let Some(revision) = state.revision.checked_add(1) {
        state.revision = revision;
    } else {
        state.problem = Some(DateFormatProblem::Unavailable);
    }
}

fn inspect(path: &Path) -> Result<DateFormatPreference, DateFormatProblem> {
    let metadata = match fs::symlink_metadata(path) {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            return Ok(DateFormatPreference::System);
        }
        Err(_) => return Err(DateFormatProblem::ReadFailed),
    };
    if !metadata.is_file() || metadata.len() > DOCUMENT_LIMIT {
        return Err(DateFormatProblem::InvalidDocument);
    }
    let file = File::open(path).map_err(|_| DateFormatProblem::ReadFailed)?;
    let mut bytes = Vec::new();
    file.take(DOCUMENT_LIMIT + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| DateFormatProblem::ReadFailed)?;
    if bytes.len() as u64 > DOCUMENT_LIMIT {
        return Err(DateFormatProblem::InvalidDocument);
    }
    let document: Document =
        serde_json::from_slice(&bytes).map_err(|_| DateFormatProblem::InvalidDocument)?;
    if document.version != 1 {
        return Err(DateFormatProblem::UnsupportedVersion);
    }
    Ok(document.date_format)
}

fn persist(path: &Path, date_format: DateFormatPreference) -> Result<(), DateFormatProblem> {
    let parent = path.parent().ok_or(DateFormatProblem::WriteFailed)?;
    fs::create_dir_all(parent).map_err(|_| DateFormatProblem::WriteFailed)?;
    let temporary = parent.join(format!(".date_format-{}.tmp", uuid::Uuid::now_v7()));
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
        .map_err(|_| DateFormatProblem::WriteFailed)?;
    let scratch = &temporary;
    let result = (move || {
        let bytes = serde_json::to_vec(&Document {
            version: 1,
            date_format,
        })
        .map_err(|_| DateFormatProblem::WriteFailed)?;
        file.write_all(&bytes)
            .and_then(|_| file.sync_all())
            .map_err(|_| DateFormatProblem::WriteFailed)?;
        drop(file);
        crate::appearance::replace(scratch, path).map_err(|_| DateFormatProblem::WriteFailed)?;
        // Publication happened already. A failed directory sync is uncertain,
        // so retain the prior selection and require a fresh read before retry.
        #[cfg(unix)]
        File::open(parent)
            .and_then(|directory| directory.sync_all())
            .map_err(|_| DateFormatProblem::OutcomeUnknown)?;
        Ok(())
    })();
    let _ = fs::remove_file(temporary);
    result
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn failed_or_uncertain_publication_requires_a_fresh_read() {
        let directory = tempfile::tempdir().unwrap();
        let store = DateFormatStore::new(Some(directory.path().into()));
        let initial = store.read();
        let dark = store.update(DateFormatPreference::Ymd, initial.revision);
        let failure = store.update_with(DateFormatPreference::Mdy, dark.revision, |_, _| {
            Err(DateFormatProblem::WriteFailed)
        });
        assert_eq!(failure.date_format, DateFormatPreference::Ymd);
        assert_eq!(failure.problem, Some(DateFormatProblem::WriteFailed));
        assert_eq!(
            store
                .update(DateFormatPreference::Mdy, failure.revision)
                .date_format,
            DateFormatPreference::Ymd
        );
        let inspected = store.read();
        let uncertain = store.update_with(
            DateFormatPreference::Mdy,
            inspected.revision,
            |path, date_format| {
                persist(path, date_format)?;
                Err(DateFormatProblem::OutcomeUnknown)
            },
        );
        assert_eq!(uncertain.date_format, DateFormatPreference::Ymd);
        assert_eq!(uncertain.problem, Some(DateFormatProblem::OutcomeUnknown));
        assert_eq!(
            store
                .update(DateFormatPreference::Ymd, uncertain.revision)
                .date_format,
            DateFormatPreference::Ymd
        );
        let inspected = store.read();
        assert_eq!(inspected.date_format, DateFormatPreference::Mdy);
        assert_eq!(inspected.problem, None);
        assert_eq!(
            store
                .update(DateFormatPreference::System, inspected.revision)
                .problem,
            None
        );
    }

    #[test]
    fn unchanged_reads_keep_all_windows_on_the_same_revision() {
        let directory = tempfile::tempdir().unwrap();
        let store = DateFormatStore::new(Some(directory.path().into()));
        let initial = store.read();
        assert_eq!(store.read(), initial);
        assert_eq!(
            store
                .update(DateFormatPreference::Ymd, initial.revision)
                .problem,
            None
        );
    }

    #[cfg(unix)]
    #[test]
    fn linked_storage_is_preserved_without_touching_its_target() {
        let directory = tempfile::tempdir().unwrap();
        let target = directory.path().join("external.json");
        fs::write(&target, br#"{"version":1,"date_format":"ymd"}"#).unwrap();
        let original = fs::read(&target).unwrap();
        std::os::unix::fs::symlink(&target, directory.path().join("date_format.json")).unwrap();
        let store = DateFormatStore::new(Some(directory.path().into()));
        let snapshot = store.read();
        assert_eq!(snapshot.problem, Some(DateFormatProblem::InvalidDocument));
        store.update(DateFormatPreference::Mdy, snapshot.revision);
        assert_eq!(fs::read(target).unwrap(), original);
        assert!(
            fs::symlink_metadata(directory.path().join("date_format.json"))
                .unwrap()
                .is_symlink()
        );
    }

    #[test]
    fn default_restart_and_stale_selection() {
        let directory = tempfile::tempdir().unwrap();
        let store = DateFormatStore::new(Some(directory.path().into()));
        let initial = store.read();
        assert_eq!(initial.date_format, DateFormatPreference::System);
        assert_eq!(initial.problem, None);
        let dark = store.update(DateFormatPreference::Ymd, initial.revision);
        assert_eq!(dark.date_format, DateFormatPreference::Ymd);
        assert_eq!(dark.problem, None);
        assert_eq!(
            store
                .update(DateFormatPreference::Mdy, initial.revision)
                .problem,
            Some(DateFormatProblem::Changed)
        );
        assert_eq!(store.read().date_format, DateFormatPreference::Ymd);
        let restarted = DateFormatStore::new(Some(directory.path().into()));
        let observed = restarted.read();
        assert_eq!(observed.date_format, DateFormatPreference::Ymd);
        assert_eq!(
            restarted
                .update(DateFormatPreference::Mdy, observed.revision)
                .date_format,
            DateFormatPreference::Mdy
        );
        assert_eq!(restarted.read().date_format, DateFormatPreference::Mdy);
    }

    #[test]
    fn invalid_and_newer_documents_remain_intact() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("date_format.json");
        for bytes in [
            b"broken".as_slice(),
            br#"{"version":2,"date_format":"ymd"}"#,
            br#"{"version":1,"date_format":"other"}"#,
            br#"{"version":1,"date_format":"ymd","extra":true}"#,
        ] {
            fs::write(&path, bytes).unwrap();
            let store = DateFormatStore::new(Some(directory.path().into()));
            let observed = store.read();
            assert_eq!(observed.date_format, DateFormatPreference::System);
            assert!(observed.problem.is_some());
            assert_eq!(
                store
                    .update(DateFormatPreference::Mdy, observed.revision)
                    .date_format,
                DateFormatPreference::System
            );
            assert_eq!(fs::read(&path).unwrap(), bytes);
        }
    }

    #[test]
    fn external_change_requires_inspection_before_retry() {
        let directory = tempfile::tempdir().unwrap();
        let store = DateFormatStore::new(Some(directory.path().into()));
        let initial = store.read();
        fs::write(
            directory.path().join("date_format.json"),
            br#"{"version":1,"date_format":"ymd"}"#,
        )
        .unwrap();
        let rejected = store.update(DateFormatPreference::Mdy, initial.revision);
        assert_eq!(rejected.date_format, DateFormatPreference::System);
        assert_eq!(rejected.problem, Some(DateFormatProblem::Changed));
        let observed = store.read();
        assert_eq!(observed.date_format, DateFormatPreference::Ymd);
        assert_eq!(
            store
                .update(DateFormatPreference::Mdy, observed.revision)
                .problem,
            None
        );
    }

    #[test]
    fn write_failure_keeps_committed_selection_and_blocks_retry() {
        let directory = tempfile::tempdir().unwrap();
        let store = DateFormatStore::new(Some(directory.path().into()));
        let initial = store.read();
        let committed = store.update(DateFormatPreference::Ymd, initial.revision);
        fs::remove_file(directory.path().join("date_format.json")).unwrap();
        fs::remove_dir(directory.path()).unwrap();
        fs::write(directory.path(), b"not a directory").unwrap();
        let failed = store.update(DateFormatPreference::Mdy, committed.revision);
        assert_eq!(failed.date_format, DateFormatPreference::Ymd);
        assert!(failed.problem.is_some());
        assert_eq!(
            store
                .update(DateFormatPreference::System, failed.revision)
                .date_format,
            DateFormatPreference::Ymd
        );
        fs::remove_file(directory.path()).unwrap();
    }
}
