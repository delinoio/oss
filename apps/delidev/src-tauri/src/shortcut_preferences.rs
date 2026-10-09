// SPDX-License-Identifier: Apache-2.0
use std::{
    collections::BTreeMap,
    fs::{self, File, OpenOptions},
    io::{Read, Write},
    path::{Path, PathBuf},
    sync::Mutex,
};

use serde::{Deserialize, Serialize};
const DOCUMENT_LIMIT: u64 = 4096;
#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq, PartialOrd, Ord)]
#[serde(rename_all = "kebab-case")]
pub enum ShortcutAction {
    Help,
    NewSession,
    SessionFocus,
    SessionSend,
    NewSessionFocus,
    NewSessionSend,
    SearchFocus,
}
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ShortcutChord {
    pub key: String,
    pub shift: bool,
}
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(tag = "state", rename_all = "kebab-case", deny_unknown_fields)]
pub enum ShortcutOverride {
    Disabled,
    Binding { chord: ShortcutChord },
}
pub type ShortcutOverrides = BTreeMap<ShortcutAction, ShortcutOverride>;
#[derive(Clone, Copy, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum ShortcutProblem {
    Unavailable,
    ReadFailed,
    InvalidDocument,
    UnsupportedVersion,
    WriteFailed,
    OutcomeUnknown,
    Changed,
}
#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct ShortcutSnapshot {
    pub revision: u32,
    pub overrides: ShortcutOverrides,
    pub problem: Option<ShortcutProblem>,
}
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Document {
    version: u32,
    revision: u32,
    overrides: ShortcutOverrides,
}
fn chord_valid(chord: &ShortcutChord) -> bool {
    if chord.key != "Enter"
        && !(chord.key.len() == 1 && chord.key.as_bytes()[0].is_ascii_lowercase()
            || chord.key.len() == 1 && chord.key.as_bytes()[0].is_ascii_digit())
    {
        return false;
    }
    // Fixed product window/palette/tab reservations and native editing survive
    // overrides.
    if chord.shift {
        !matches!(chord.key.as_str(), "v" | "z")
    } else {
        !matches!(
            chord.key.as_str(),
            "q" | "h"
                | "m"
                | "b"
                | "k"
                | "n"
                | "w"
                | "1"
                | "2"
                | "3"
                | "4"
                | "5"
                | "6"
                | "7"
                | "8"
                | "9"
                | "a"
                | "c"
                | "v"
                | "x"
                | "y"
                | "z"
        )
    }
}
fn valid(overrides: &ShortcutOverrides) -> bool {
    if overrides.len() > 7
        || overrides.values().any(
            |entry| matches!(entry, ShortcutOverride::Binding { chord } if !chord_valid(chord)),
        )
    {
        return false;
    }
    // Only these two actions share both priority and overlapping catalog
    // scopes.
    let help = match overrides.get(&ShortcutAction::Help) {
        Some(ShortcutOverride::Binding { chord }) => Some(chord.clone()),
        _ => None,
    };
    let new = match overrides.get(&ShortcutAction::NewSession) {
        Some(ShortcutOverride::Disabled) => None,
        Some(ShortcutOverride::Binding { chord }) => Some(chord.clone()),
        None => Some(ShortcutChord {
            key: "n".into(),
            shift: true,
        }),
    };
    help.is_none() || help != new
}
/// Independent device storage; publication and expected revisions never grant
/// business authority.
pub struct ShortcutStore {
    path: Option<PathBuf>,
    state: Mutex<ShortcutSnapshot>,
    presentation: Mutex<()>,
}
impl ShortcutStore {
    pub fn new(config_dir: Option<PathBuf>) -> Self {
        Self {
            path: config_dir.map(|dir| dir.join("shortcuts.json")),
            state: Mutex::new(ShortcutSnapshot {
                revision: 0,
                overrides: BTreeMap::new(),
                problem: Some(ShortcutProblem::Unavailable),
            }),
            presentation: Mutex::new(()),
        }
    }

    pub fn presentation(&self) -> std::sync::MutexGuard<'_, ()> {
        self.presentation
            .lock()
            .unwrap_or_else(|error| error.into_inner())
    }

    // Native capture admission uses only retained verified state on the UI
    // loop. Hold this guard through physical menu replacement so a concurrent
    // persistence commit cannot change its original preference revision.
    pub fn capture_revision(
        &self,
        expected: u32,
    ) -> Result<std::sync::MutexGuard<'_, ShortcutSnapshot>, crate::NativeFailure> {
        let state = self
            .state
            .try_lock()
            .map_err(|_| crate::NativeFailure::Busy)?;
        if state.problem.is_some() || state.revision != expected {
            return Err(crate::NativeFailure::InvalidEvidence);
        }
        Ok(state)
    }

    pub fn read(&self) -> ShortcutSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        match self.path.as_deref().map(inspect) {
            Some(Ok(document)) => {
                let changed = state.revision == 0
                    || state.overrides != document.overrides
                    || state.problem.is_some();
                state.overrides = document.overrides;
                state.problem = None;
                if changed {
                    match state.revision.max(document.revision).checked_add(1) {
                        Some(revision) => state.revision = revision,
                        None => state.problem = Some(ShortcutProblem::Unavailable),
                    };
                    if state.revision == u32::MAX {
                        state.problem = Some(ShortcutProblem::Unavailable);
                    }
                }
            }
            Some(Err(problem)) => {
                state.problem = Some(problem);
                tracing::warn!(operation = "shortcut_preferences_read", ?problem);
            }
            None => state.problem = Some(ShortcutProblem::Unavailable),
        }
        state.clone()
    }

    pub fn update(&self, overrides: ShortcutOverrides, expected_revision: u32) -> ShortcutSnapshot {
        self.update_with(overrides, expected_revision, persist)
    }

    fn update_with(
        &self,
        overrides: ShortcutOverrides,
        expected_revision: u32,
        write: impl FnOnce(&Path, ShortcutOverrides, u32) -> Result<(), ShortcutProblem>,
    ) -> ShortcutSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        if state.revision != expected_revision {
            let mut result = state.clone();
            result.problem = Some(ShortcutProblem::Changed);
            return result;
        }
        if state.problem.is_some() || state.revision == u32::MAX {
            return state.clone();
        }
        if !valid(&overrides) {
            let mut result = state.clone();
            result.problem = Some(ShortcutProblem::InvalidDocument);
            return result;
        }
        let revision = state.revision + 1;
        let result = self
            .path
            .as_deref()
            .ok_or(ShortcutProblem::Unavailable)
            .and_then(|path| {
                let document = inspect(path)?;
                if document.overrides != state.overrides || document.revision > state.revision {
                    return Err(ShortcutProblem::Changed);
                }
                write(path, overrides.clone(), revision)
            });
        match result {
            Ok(()) => {
                state.overrides = overrides;
                state.revision = revision;
                tracing::info!(
                    operation = "shortcut_preferences_update",
                    revision,
                    state = "committed"
                );
            }
            Err(problem) => {
                state.problem = Some(problem);
                tracing::warn!(operation = "shortcut_preferences_update", ?problem);
            }
        }
        state.clone()
    }
}
fn inspect(path: &Path) -> Result<Document, ShortcutProblem> {
    let metadata = match fs::symlink_metadata(path) {
        Ok(value) => value,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            return Ok(Document {
                version: 1,
                revision: 0,
                overrides: BTreeMap::new(),
            });
        }
        Err(_) => return Err(ShortcutProblem::ReadFailed),
    };
    if !metadata.is_file() || metadata.len() > DOCUMENT_LIMIT {
        return Err(ShortcutProblem::InvalidDocument);
    }
    let mut bytes = Vec::new();
    File::open(path)
        .map_err(|_| ShortcutProblem::ReadFailed)?
        .take(DOCUMENT_LIMIT + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| ShortcutProblem::ReadFailed)?;
    if bytes.len() as u64 > DOCUMENT_LIMIT {
        return Err(ShortcutProblem::InvalidDocument);
    }
    let document: Document =
        serde_json::from_slice(&bytes).map_err(|_| ShortcutProblem::InvalidDocument)?;
    if document.version != 1 {
        return Err(ShortcutProblem::UnsupportedVersion);
    }
    if document.revision == u32::MAX || !valid(&document.overrides) {
        return Err(ShortcutProblem::InvalidDocument);
    }
    Ok(document)
}
fn persist(
    path: &Path,
    overrides: ShortcutOverrides,
    revision: u32,
) -> Result<(), ShortcutProblem> {
    let parent = path.parent().ok_or(ShortcutProblem::WriteFailed)?;
    fs::create_dir_all(parent).map_err(|_| ShortcutProblem::WriteFailed)?;
    let temporary = parent.join(format!(
        ".shortcut_preferences-{}.tmp",
        uuid::Uuid::now_v7()
    ));
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
        .map_err(|_| ShortcutProblem::WriteFailed)?;
    let scratch = &temporary;
    let result = (move || {
        let bytes = serde_json::to_vec(&Document {
            version: 1,
            overrides,
            revision,
        })
        .map_err(|_| ShortcutProblem::WriteFailed)?;
        file.write_all(&bytes)
            .and_then(|_| file.sync_all())
            .map_err(|_| ShortcutProblem::WriteFailed)?;
        drop(file);
        crate::appearance::replace(scratch, path).map_err(|_| ShortcutProblem::WriteFailed)?;
        // Publication happened already. A failed directory sync is uncertain,
        // so retain the prior selection and require a fresh read before retry.
        #[cfg(unix)]
        File::open(parent)
            .and_then(|directory| directory.sync_all())
            .map_err(|_| ShortcutProblem::OutcomeUnknown)?;
        Ok(())
    })();
    let _ = fs::remove_file(temporary);
    result
}

#[cfg(test)]
mod tests {
    use super::*;
    fn binding(key: &str, shift: bool) -> ShortcutOverride {
        ShortcutOverride::Binding {
            chord: ShortcutChord {
                key: key.into(),
                shift,
            },
        }
    }
    fn choice() -> ShortcutOverrides {
        BTreeMap::from([(ShortcutAction::NewSession, binding("j", true))])
    }
    #[test]
    fn capture_admission_rechecks_retained_revision_without_disk_io() {
        let dir = tempfile::tempdir().unwrap();
        let store = ShortcutStore::new(Some(dir.path().into()));
        let initial = store.read();
        let guard = store.capture_revision(initial.revision).unwrap();
        assert!(matches!(
            store.capture_revision(initial.revision),
            Err(crate::NativeFailure::Busy)
        ));
        drop(guard);
        let next = store.update(choice(), initial.revision);
        assert!(store.capture_revision(initial.revision).is_err());
        assert!(store.capture_revision(next.revision).is_ok());
    }
    #[test]
    fn native_menu_chords_are_rejected_for_every_editable_action() {
        for action in [
            ShortcutAction::Help,
            ShortcutAction::NewSession,
            ShortcutAction::SessionFocus,
            ShortcutAction::SessionSend,
            ShortcutAction::NewSessionFocus,
            ShortcutAction::NewSessionSend,
            ShortcutAction::SearchFocus,
        ] {
            for key in ["q", "h", "m", "n", "w"] {
                assert!(!valid(&BTreeMap::from([(action, binding(key, false))])));
                assert!(chord_valid(&ShortcutChord {
                    key: key.into(),
                    shift: true
                }));
            }
        }
    }
    #[test]
    fn retired_new_window_chord_is_editable_and_persists_without_admitting_n() {
        for action in [
            ShortcutAction::Help,
            ShortcutAction::NewSession,
            ShortcutAction::SessionFocus,
            ShortcutAction::SessionSend,
            ShortcutAction::NewSessionFocus,
            ShortcutAction::NewSessionSend,
            ShortcutAction::SearchFocus,
        ] {
            let dir = tempfile::tempdir().unwrap();
            let store = ShortcutStore::new(Some(dir.path().into()));
            let initial = store.read();
            let overrides = BTreeMap::from([(action, binding("t", false))]);
            let saved = store.update(overrides.clone(), initial.revision);
            assert_eq!(saved.problem, None);
            assert_eq!(saved.overrides, overrides);
            assert_eq!(
                ShortcutStore::new(Some(dir.path().into())).read().overrides,
                overrides
            );
            assert!(!valid(&BTreeMap::from([(action, binding("n", false))])));
        }
    }
    #[test]
    fn defaults_restart_and_stale_revision_preserve_original_map() {
        let dir = tempfile::tempdir().unwrap();
        let store = ShortcutStore::new(Some(dir.path().into()));
        let initial = store.read();
        assert!(initial.overrides.is_empty());
        assert_eq!(initial.problem, None);
        let saved = store.update(choice(), initial.revision);
        assert_eq!(saved.overrides, choice());
        assert_eq!(saved.problem, None);
        let document: Document =
            serde_json::from_slice(&fs::read(dir.path().join("shortcuts.json")).unwrap()).unwrap();
        assert_eq!(document.revision, saved.revision);
        assert_eq!(
            store.update(BTreeMap::new(), initial.revision).problem,
            Some(ShortcutProblem::Changed)
        );
        let restarted = ShortcutStore::new(Some(dir.path().into()));
        assert_eq!(restarted.read().overrides, choice());
        assert_eq!(store.read(), saved);
    }
    #[test]
    fn invalid_newer_and_oversized_documents_are_never_overwritten() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("shortcuts.json");
        for bytes in [
            b"broken".to_vec(),
            br#"{"version":2,"revision":1,"overrides":{}}"#.to_vec(),
            br#"{"version":1,"revision":1,"overrides":{"unknown":{"state":"disabled"}}}"#.to_vec(),
            vec![b' '; 4097],
        ] {
            fs::write(&path, &bytes).unwrap();
            let store = ShortcutStore::new(Some(dir.path().into()));
            let state = store.read();
            assert!(state.problem.is_some());
            store.update(choice(), state.revision);
            assert_eq!(fs::read(&path).unwrap(), bytes);
        }
    }
    #[test]
    fn reserved_chords_and_overlapping_actions_are_rejected() {
        for key in ["b", "k", "n", "w", "1", "9", "c", "Escape", "é"] {
            assert!(!valid(&BTreeMap::from([(
                ShortcutAction::Help,
                binding(key, false)
            )])));
        }
        for key in ["v", "z"] {
            assert!(!valid(&BTreeMap::from([(
                ShortcutAction::Help,
                binding(key, true)
            )])));
        }
        assert!(!valid(&BTreeMap::from([
            (ShortcutAction::Help, binding("j", true)),
            (ShortcutAction::NewSession, binding("j", true))
        ])));
        assert!(!valid(&BTreeMap::from([(
            ShortcutAction::Help,
            binding("n", true)
        )])));
        assert!(valid(&BTreeMap::from([
            (ShortcutAction::SessionFocus, binding("j", true)),
            (ShortcutAction::SearchFocus, binding("j", true))
        ])));
    }
    #[test]
    fn failed_and_uncertain_publication_require_explicit_read() {
        let dir = tempfile::tempdir().unwrap();
        let store = ShortcutStore::new(Some(dir.path().into()));
        let initial = store.read();
        let failed = store.update_with(choice(), initial.revision, |_, _, _| {
            Err(ShortcutProblem::WriteFailed)
        });
        assert!(failed.overrides.is_empty());
        assert_eq!(failed.problem, Some(ShortcutProblem::WriteFailed));
        assert!(store.update(choice(), failed.revision).overrides.is_empty());
        let inspected = store.read();
        let unknown = store.update_with(choice(), inspected.revision, |path, value, revision| {
            persist(path, value, revision)?;
            Err(ShortcutProblem::OutcomeUnknown)
        });
        assert!(unknown.overrides.is_empty());
        assert_eq!(unknown.problem, Some(ShortcutProblem::OutcomeUnknown));
        assert!(
            store
                .update(choice(), unknown.revision)
                .overrides
                .is_empty()
        );
        assert_eq!(store.read().overrides, choice());
    }
    #[test]
    fn invalid_external_file_preserves_last_verified_effective_bindings() {
        let dir = tempfile::tempdir().unwrap();
        let store = ShortcutStore::new(Some(dir.path().into()));
        let initial = store.read();
        let saved = store.update(choice(), initial.revision);
        fs::write(dir.path().join("shortcuts.json"), b"broken").unwrap();
        let observed = store.read();
        assert_eq!(observed.overrides, saved.overrides);
        assert_eq!(observed.problem, Some(ShortcutProblem::InvalidDocument));
        assert_eq!(
            store.update(BTreeMap::new(), observed.revision).overrides,
            choice()
        );
    }
    #[cfg(unix)]
    #[test]
    fn symbolic_links_are_preserved_without_touching_their_target() {
        let dir = tempfile::tempdir().unwrap();
        let target = dir.path().join("external");
        fs::write(&target, b"original").unwrap();
        std::os::unix::fs::symlink(&target, dir.path().join("shortcuts.json")).unwrap();
        let store = ShortcutStore::new(Some(dir.path().into()));
        let observed = store.read();
        store.update(choice(), observed.revision);
        assert_eq!(fs::read(target).unwrap(), b"original");
        assert!(
            fs::symlink_metadata(dir.path().join("shortcuts.json"))
                .unwrap()
                .is_symlink()
        );
    }
}
