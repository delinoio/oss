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
pub enum Theme {
    #[default]
    System,
    Light,
    Dark,
}

#[derive(Clone, Copy, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum AppearanceProblem {
    Unavailable,
    ReadFailed,
    InvalidDocument,
    UnsupportedVersion,
    WriteFailed,
    OutcomeUnknown,
    Changed,
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct AppearanceSnapshot {
    pub revision: u32,
    pub theme: Theme,
    pub problem: Option<AppearanceProblem>,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Document {
    version: u32,
    theme: Theme,
}

/// One process-wide device preference, independent of every server connection.
/// The lock covers inspection and atomic publication; revisions order IPC
/// replies and events even when their delivery order differs from their commit
/// order.
pub struct AppearanceStore {
    path: Option<PathBuf>,
    state: Mutex<AppearanceSnapshot>,
}

impl AppearanceStore {
    pub fn new(config_dir: Option<PathBuf>) -> Self {
        Self {
            path: config_dir.map(|directory| directory.join("appearance.json")),
            state: Mutex::new(AppearanceSnapshot {
                revision: 0,
                theme: Theme::System,
                problem: Some(AppearanceProblem::Unavailable),
            }),
        }
    }

    /// Retained presentation only; auxiliary views cannot inspect preference
    /// files.
    pub fn current(&self) -> AppearanceSnapshot {
        self.state
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .clone()
    }

    pub fn read(&self) -> AppearanceSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        let previous = state.clone();
        match self.path.as_deref().map(inspect) {
            Some(Ok(theme)) => {
                state.theme = theme;
                state.problem = None;
            }
            Some(Err(problem)) => {
                if matches!(
                    problem,
                    AppearanceProblem::InvalidDocument | AppearanceProblem::UnsupportedVersion
                ) {
                    state.theme = Theme::System;
                }
                state.problem = Some(problem);
                tracing::warn!(operation = "appearance_read", ?problem);
            }
            None => state.problem = Some(AppearanceProblem::Unavailable),
        }
        if state.revision == 0 || state.theme != previous.theme || state.problem != previous.problem
        {
            advance(&mut state);
        }
        state.clone()
    }

    pub fn update(&self, theme: Theme, expected_revision: u32) -> AppearanceSnapshot {
        self.update_with(theme, expected_revision, persist)
    }

    fn update_with(
        &self,
        theme: Theme,
        expected_revision: u32,
        write: impl FnOnce(&Path, Theme) -> Result<(), AppearanceProblem>,
    ) -> AppearanceSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        if state.revision != expected_revision {
            // Do not mutate the shared committed state for a stale caller. It
            // must inspect again before choosing against the current selection.
            let mut snapshot = state.clone();
            snapshot.problem = Some(AppearanceProblem::Changed);
            return snapshot;
        }
        if state.problem.is_some() || state.revision == u32::MAX {
            return state.clone();
        }
        let result = self
            .path
            .as_deref()
            .ok_or(AppearanceProblem::Unavailable)
            .and_then(|path| {
                // Reinspect before replacement, preserving external
                // invalid/newer documents instead of silently
                // upgrading or overwriting them.
                let current = inspect(path)?;
                if current != state.theme {
                    return Err(AppearanceProblem::Changed);
                }
                write(path, theme)
            });
        match result {
            Ok(()) => {
                state.theme = theme;
                tracing::info!(operation = "appearance_update", state = "committed");
            }
            Err(problem) => {
                state.problem = Some(problem);
                tracing::warn!(operation = "appearance_update", ?problem);
            }
        }
        advance(&mut state);
        state.clone()
    }
}

fn advance(state: &mut AppearanceSnapshot) {
    // Never wrap into an older revision. Exhaustion remains read-only until
    // restart instead of letting a delayed event replace newer state.
    if let Some(revision) = state.revision.checked_add(1) {
        state.revision = revision;
    } else {
        state.problem = Some(AppearanceProblem::Unavailable);
    }
}

fn inspect(path: &Path) -> Result<Theme, AppearanceProblem> {
    let metadata = match fs::symlink_metadata(path) {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(Theme::System),
        Err(_) => return Err(AppearanceProblem::ReadFailed),
    };
    if !metadata.is_file() || metadata.len() > DOCUMENT_LIMIT {
        return Err(AppearanceProblem::InvalidDocument);
    }
    let file = File::open(path).map_err(|_| AppearanceProblem::ReadFailed)?;
    let mut bytes = Vec::new();
    file.take(DOCUMENT_LIMIT + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| AppearanceProblem::ReadFailed)?;
    if bytes.len() as u64 > DOCUMENT_LIMIT {
        return Err(AppearanceProblem::InvalidDocument);
    }
    let document: Document =
        serde_json::from_slice(&bytes).map_err(|_| AppearanceProblem::InvalidDocument)?;
    if document.version != 1 {
        return Err(AppearanceProblem::UnsupportedVersion);
    }
    Ok(document.theme)
}

fn persist(path: &Path, theme: Theme) -> Result<(), AppearanceProblem> {
    let parent = path.parent().ok_or(AppearanceProblem::WriteFailed)?;
    fs::create_dir_all(parent).map_err(|_| AppearanceProblem::WriteFailed)?;
    let temporary = parent.join(format!(".appearance-{}.tmp", uuid::Uuid::now_v7()));
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
        .map_err(|_| AppearanceProblem::WriteFailed)?;
    let scratch = &temporary;
    let result = (move || {
        let bytes = serde_json::to_vec(&Document { version: 1, theme })
            .map_err(|_| AppearanceProblem::WriteFailed)?;
        file.write_all(&bytes)
            .and_then(|_| file.sync_all())
            .map_err(|_| AppearanceProblem::WriteFailed)?;
        drop(file);
        replace(scratch, path).map_err(|_| AppearanceProblem::WriteFailed)?;
        // Publication happened already. A failed directory sync is uncertain,
        // so retain the prior selection and require a fresh read before retry.
        #[cfg(unix)]
        File::open(parent)
            .and_then(|directory| directory.sync_all())
            .map_err(|_| AppearanceProblem::OutcomeUnknown)?;
        Ok(())
    })();
    let _ = fs::remove_file(temporary);
    result
}

#[cfg(not(windows))]
pub(crate) fn replace(source: &Path, target: &Path) -> std::io::Result<()> {
    fs::rename(source, target)
}

#[cfg(windows)]
pub(crate) fn replace(source: &Path, target: &Path) -> std::io::Result<()> {
    use std::os::windows::ffi::OsStrExt;
    #[link(name = "kernel32")]
    unsafe extern "system" {
        fn MoveFileExW(source: *const u16, target: *const u16, flags: u32) -> i32;
    }
    let source: Vec<u16> = source.as_os_str().encode_wide().chain(Some(0)).collect();
    let target: Vec<u16> = target.as_os_str().encode_wide().chain(Some(0)).collect();
    // Replace the same-directory target atomically and wait for publication.
    if unsafe { MoveFileExW(source.as_ptr(), target.as_ptr(), 0x1 | 0x8) } == 0 {
        Err(std::io::Error::last_os_error())
    } else {
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn failed_or_uncertain_publication_requires_a_fresh_read() {
        let directory = tempfile::tempdir().unwrap();
        let store = AppearanceStore::new(Some(directory.path().into()));
        let initial = store.read();
        let dark = store.update(Theme::Dark, initial.revision);
        let failure = store.update_with(Theme::Light, dark.revision, |_, _| {
            Err(AppearanceProblem::WriteFailed)
        });
        assert_eq!(failure.theme, Theme::Dark);
        assert_eq!(failure.problem, Some(AppearanceProblem::WriteFailed));
        assert_eq!(
            store.update(Theme::Light, failure.revision).theme,
            Theme::Dark
        );
        let inspected = store.read();
        let uncertain = store.update_with(Theme::Light, inspected.revision, |path, theme| {
            persist(path, theme)?;
            Err(AppearanceProblem::OutcomeUnknown)
        });
        assert_eq!(uncertain.theme, Theme::Dark);
        assert_eq!(uncertain.problem, Some(AppearanceProblem::OutcomeUnknown));
        assert_eq!(
            store.update(Theme::Dark, uncertain.revision).theme,
            Theme::Dark
        );
        let inspected = store.read();
        assert_eq!(inspected.theme, Theme::Light);
        assert_eq!(inspected.problem, None);
        assert_eq!(
            store.update(Theme::System, inspected.revision).problem,
            None
        );
    }

    #[test]
    fn unchanged_reads_keep_all_windows_on_the_same_revision() {
        let directory = tempfile::tempdir().unwrap();
        let store = AppearanceStore::new(Some(directory.path().into()));
        let initial = store.read();
        assert_eq!(store.read(), initial);
        assert_eq!(store.update(Theme::Dark, initial.revision).problem, None);
    }

    #[cfg(unix)]
    #[test]
    fn linked_storage_is_preserved_without_touching_its_target() {
        let directory = tempfile::tempdir().unwrap();
        let target = directory.path().join("external.json");
        fs::write(&target, br#"{"version":1,"theme":"dark"}"#).unwrap();
        let original = fs::read(&target).unwrap();
        std::os::unix::fs::symlink(&target, directory.path().join("appearance.json")).unwrap();
        let store = AppearanceStore::new(Some(directory.path().into()));
        let snapshot = store.read();
        assert_eq!(snapshot.problem, Some(AppearanceProblem::InvalidDocument));
        store.update(Theme::Light, snapshot.revision);
        assert_eq!(fs::read(target).unwrap(), original);
        assert!(
            fs::symlink_metadata(directory.path().join("appearance.json"))
                .unwrap()
                .is_symlink()
        );
    }

    #[test]
    fn default_restart_and_stale_selection() {
        let directory = tempfile::tempdir().unwrap();
        let store = AppearanceStore::new(Some(directory.path().into()));
        let initial = store.read();
        assert_eq!(initial.theme, Theme::System);
        assert_eq!(initial.problem, None);
        let dark = store.update(Theme::Dark, initial.revision);
        assert_eq!(dark.theme, Theme::Dark);
        assert_eq!(dark.problem, None);
        assert_eq!(
            store.update(Theme::Light, initial.revision).problem,
            Some(AppearanceProblem::Changed)
        );
        assert_eq!(store.read().theme, Theme::Dark);
        let restarted = AppearanceStore::new(Some(directory.path().into()));
        let observed = restarted.read();
        assert_eq!(observed.theme, Theme::Dark);
        assert_eq!(
            restarted.update(Theme::Light, observed.revision).theme,
            Theme::Light
        );
        assert_eq!(restarted.read().theme, Theme::Light);
    }

    #[test]
    fn invalid_and_newer_documents_remain_intact() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("appearance.json");
        for bytes in [
            b"broken".as_slice(),
            br#"{"version":2,"theme":"dark"}"#,
            br#"{"version":1,"theme":"other"}"#,
            br#"{"version":1,"theme":"dark","extra":true}"#,
        ] {
            fs::write(&path, bytes).unwrap();
            let store = AppearanceStore::new(Some(directory.path().into()));
            let observed = store.read();
            assert_eq!(observed.theme, Theme::System);
            assert!(observed.problem.is_some());
            assert_eq!(
                store.update(Theme::Light, observed.revision).theme,
                Theme::System
            );
            assert_eq!(fs::read(&path).unwrap(), bytes);
        }
    }

    #[test]
    fn external_change_requires_inspection_before_retry() {
        let directory = tempfile::tempdir().unwrap();
        let store = AppearanceStore::new(Some(directory.path().into()));
        let initial = store.read();
        fs::write(
            directory.path().join("appearance.json"),
            br#"{"version":1,"theme":"dark"}"#,
        )
        .unwrap();
        let rejected = store.update(Theme::Light, initial.revision);
        assert_eq!(rejected.theme, Theme::System);
        assert_eq!(rejected.problem, Some(AppearanceProblem::Changed));
        let observed = store.read();
        assert_eq!(observed.theme, Theme::Dark);
        assert_eq!(store.update(Theme::Light, observed.revision).problem, None);
    }

    #[test]
    fn write_failure_keeps_committed_selection_and_blocks_retry() {
        let directory = tempfile::tempdir().unwrap();
        let store = AppearanceStore::new(Some(directory.path().into()));
        let initial = store.read();
        let committed = store.update(Theme::Dark, initial.revision);
        fs::remove_file(directory.path().join("appearance.json")).unwrap();
        fs::remove_dir(directory.path()).unwrap();
        fs::write(directory.path(), b"not a directory").unwrap();
        let failed = store.update(Theme::Light, committed.revision);
        assert_eq!(failed.theme, Theme::Dark);
        assert!(failed.problem.is_some());
        assert_eq!(
            store.update(Theme::System, failed.revision).theme,
            Theme::Dark
        );
        fs::remove_file(directory.path()).unwrap();
    }
}
