// SPDX-License-Identifier: Apache-2.0
//! Device-local browser destinations for external HTTP(S) links. Two
//! independent choices (general and local) persist as native browser
//! identities, never display names, executable paths or renderer-supplied
//! commands. The storage shape follows the revisioned shortcut preference
//! pattern.
use std::{
    fs::{self, File, OpenOptions},
    io::{Read, Write},
    path::{Path, PathBuf},
    sync::Mutex,
};

use serde::{Deserialize, Serialize};

use crate::{browser_discovery::valid_identity, external_url::UrlClass};

const DOCUMENT_LIMIT: u64 = 4096;
const FILE_NAME: &str = "browser_preferences.json";

#[derive(Clone, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(tag = "state", rename_all = "kebab-case", deny_unknown_fields)]
pub enum BrowserChoice {
    #[default]
    SystemDefault,
    Installed {
        identity: String,
    },
}

impl BrowserChoice {
    fn valid(&self) -> bool {
        match self {
            Self::SystemDefault => true,
            Self::Installed { identity } => valid_identity(identity),
        }
    }

    /// Stable log value: the native identity or `system-default`; never a path.
    pub fn log_identity(&self) -> &str {
        match self {
            Self::SystemDefault => "system-default",
            Self::Installed { identity } => identity,
        }
    }
}

#[derive(Clone, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct BrowserChoices {
    pub general: BrowserChoice,
    pub local: BrowserChoice,
}

impl BrowserChoices {
    fn valid(&self) -> bool {
        self.general.valid() && self.local.valid()
    }

    pub fn for_class(&self, class: UrlClass) -> &BrowserChoice {
        match class {
            UrlClass::General => &self.general,
            UrlClass::Local => &self.local,
        }
    }
}

#[derive(Clone, Copy, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum BrowserProblem {
    Unavailable,
    ReadFailed,
    InvalidDocument,
    UnsupportedVersion,
    WriteFailed,
    OutcomeUnknown,
    Changed,
}

impl BrowserProblem {
    /// Only unreadable-by-contract content can be replaced by an explicit
    /// reset.
    fn resettable(self) -> bool {
        matches!(self, Self::InvalidDocument | Self::UnsupportedVersion)
    }
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct BrowserSnapshot {
    pub revision: u32,
    pub choices: BrowserChoices,
    pub problem: Option<BrowserProblem>,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Document {
    version: u32,
    revision: u32,
    choices: BrowserChoices,
}

/// One process-wide device preference, independent of every server connection.
pub struct BrowserStore {
    path: Option<PathBuf>,
    state: Mutex<BrowserSnapshot>,
    presentation: Mutex<()>,
}

impl BrowserStore {
    pub fn new(config_dir: Option<PathBuf>) -> Self {
        Self {
            path: config_dir.map(|dir| dir.join(FILE_NAME)),
            state: Mutex::new(BrowserSnapshot {
                revision: 0,
                choices: BrowserChoices::default(),
                problem: Some(BrowserProblem::Unavailable),
            }),
            presentation: Mutex::new(()),
        }
    }

    pub fn presentation(&self) -> std::sync::MutexGuard<'_, ()> {
        self.presentation
            .lock()
            .unwrap_or_else(|error| error.into_inner())
    }

    pub fn current(&self) -> BrowserSnapshot {
        self.state
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .clone()
    }

    /// Reads the committed destination for a link class directly from storage.
    /// Dispatch never trusts retained renderer or window state, and unreadable
    /// storage fails closed instead of silently selecting the system default.
    pub fn resolve(&self, class: UrlClass) -> Result<BrowserChoice, crate::NativeFailure> {
        let path = self
            .path
            .as_deref()
            .ok_or(crate::NativeFailure::StorageUnavailable)?;
        match inspect(path) {
            Ok(document) => Ok(document.choices.for_class(class).clone()),
            Err(problem) => {
                tracing::warn!(operation = "browser_preferences_resolve", ?problem, url_class = ?class);
                Err(crate::NativeFailure::StorageUnavailable)
            }
        }
    }

    pub fn read(&self) -> BrowserSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        match self.path.as_deref().map(inspect) {
            Some(Ok(document)) => {
                let changed = state.revision == 0
                    || state.choices != document.choices
                    || state.problem.is_some();
                state.choices = document.choices;
                state.problem = None;
                if changed {
                    match state.revision.max(document.revision).checked_add(1) {
                        Some(revision) => state.revision = revision,
                        None => state.problem = Some(BrowserProblem::Unavailable),
                    };
                    if state.revision == u32::MAX {
                        state.problem = Some(BrowserProblem::Unavailable);
                    }
                }
            }
            Some(Err(problem)) => {
                state.problem = Some(problem);
                tracing::warn!(operation = "browser_preferences_read", ?problem);
            }
            None => state.problem = Some(BrowserProblem::Unavailable),
        }
        state.clone()
    }

    pub fn update(&self, choices: BrowserChoices, expected_revision: u32) -> BrowserSnapshot {
        self.update_with(choices, expected_revision, false, persist)
    }

    /// Replaces unreadable or unsupported storage with the two system defaults
    /// after an explicit user request. Healthy storage is never reset here.
    pub fn reset(&self, expected_revision: u32) -> BrowserSnapshot {
        self.update_with(BrowserChoices::default(), expected_revision, true, persist)
    }

    fn update_with(
        &self,
        choices: BrowserChoices,
        expected_revision: u32,
        reset: bool,
        write: impl FnOnce(&Path, BrowserChoices, u32) -> Result<(), BrowserProblem>,
    ) -> BrowserSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        if state.revision != expected_revision {
            let mut result = state.clone();
            result.problem = Some(BrowserProblem::Changed);
            return result;
        }
        let allowed = if reset {
            state.problem.is_some_and(BrowserProblem::resettable)
        } else {
            state.problem.is_none()
        };
        if !allowed || state.revision == u32::MAX {
            return state.clone();
        }
        if !choices.valid() {
            let mut result = state.clone();
            result.problem = Some(BrowserProblem::InvalidDocument);
            return result;
        }
        let revision = state.revision + 1;
        let result = self
            .path
            .as_deref()
            .ok_or(BrowserProblem::Unavailable)
            .and_then(|path| {
                // A reset deliberately overwrites the unreadable document it
                // was requested for; every ordinary update requires the
                // verified retained document to be unchanged.
                if !reset {
                    let document = inspect(path)?;
                    if document.choices != state.choices || document.revision > state.revision {
                        return Err(BrowserProblem::Changed);
                    }
                } else if inspect(path).is_ok() {
                    return Err(BrowserProblem::Changed);
                }
                write(path, choices.clone(), revision)
            });
        match result {
            Ok(()) => {
                state.choices = choices;
                state.problem = None;
                state.revision = revision;
                tracing::info!(
                    operation = if reset {
                        "browser_preferences_reset"
                    } else {
                        "browser_preferences_update"
                    },
                    revision,
                    state = "committed"
                );
            }
            Err(problem) => {
                state.problem = Some(problem);
                tracing::warn!(
                    operation = if reset {
                        "browser_preferences_reset"
                    } else {
                        "browser_preferences_update"
                    },
                    ?problem
                );
            }
        }
        state.clone()
    }
}

fn inspect(path: &Path) -> Result<Document, BrowserProblem> {
    let metadata = match fs::symlink_metadata(path) {
        Ok(value) => value,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            return Ok(Document {
                version: 1,
                revision: 0,
                choices: BrowserChoices::default(),
            });
        }
        Err(_) => return Err(BrowserProblem::ReadFailed),
    };
    if !metadata.is_file() || metadata.len() > DOCUMENT_LIMIT {
        return Err(BrowserProblem::InvalidDocument);
    }
    let mut bytes = Vec::new();
    File::open(path)
        .map_err(|_| BrowserProblem::ReadFailed)?
        .take(DOCUMENT_LIMIT + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| BrowserProblem::ReadFailed)?;
    if bytes.len() as u64 > DOCUMENT_LIMIT {
        return Err(BrowserProblem::InvalidDocument);
    }
    // Version is checked before the strict shape so a newer document reports
    // an unsupported version rather than a corrupt one.
    let probe: serde_json::Value =
        serde_json::from_slice(&bytes).map_err(|_| BrowserProblem::InvalidDocument)?;
    if probe.get("version").and_then(serde_json::Value::as_u64) != Some(1) {
        return Err(
            if probe
                .get("version")
                .and_then(serde_json::Value::as_u64)
                .is_some()
            {
                BrowserProblem::UnsupportedVersion
            } else {
                BrowserProblem::InvalidDocument
            },
        );
    }
    let document: Document =
        serde_json::from_value(probe).map_err(|_| BrowserProblem::InvalidDocument)?;
    if document.revision == u32::MAX || !document.choices.valid() {
        return Err(BrowserProblem::InvalidDocument);
    }
    Ok(document)
}

fn persist(path: &Path, choices: BrowserChoices, revision: u32) -> Result<(), BrowserProblem> {
    let parent = path.parent().ok_or(BrowserProblem::WriteFailed)?;
    fs::create_dir_all(parent).map_err(|_| BrowserProblem::WriteFailed)?;
    let temporary = parent.join(format!(".browser_preferences-{}.tmp", uuid::Uuid::now_v7()));
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    // Cleanup owns only the file that this attempt created exclusively.
    let mut file = options
        .open(&temporary)
        .map_err(|_| BrowserProblem::WriteFailed)?;
    let scratch = &temporary;
    let result = (move || {
        let bytes = serde_json::to_vec(&Document {
            version: 1,
            revision,
            choices,
        })
        .map_err(|_| BrowserProblem::WriteFailed)?;
        file.write_all(&bytes)
            .and_then(|_| file.sync_all())
            .map_err(|_| BrowserProblem::WriteFailed)?;
        drop(file);
        crate::appearance::replace(scratch, path).map_err(|_| BrowserProblem::WriteFailed)?;
        // Publication happened already. A failed directory sync is uncertain.
        #[cfg(unix)]
        File::open(parent)
            .and_then(|directory| directory.sync_all())
            .map_err(|_| BrowserProblem::OutcomeUnknown)?;
        Ok(())
    })();
    let _ = fs::remove_file(temporary);
    result
}

#[cfg(test)]
mod tests {
    use super::*;

    fn installed(identity: &str) -> BrowserChoice {
        BrowserChoice::Installed {
            identity: identity.into(),
        }
    }
    fn choices() -> BrowserChoices {
        BrowserChoices {
            general: installed("org.mozilla.firefox.desktop"),
            local: installed("com.google.Chrome"),
        }
    }
    fn store() -> (tempfile::TempDir, BrowserStore) {
        let dir = tempfile::tempdir().unwrap();
        let store = BrowserStore::new(Some(dir.path().into()));
        (dir, store)
    }

    #[test]
    fn defaults_are_system_default_and_choices_persist_independently_across_restart() {
        let (dir, store) = store();
        let initial = store.read();
        assert_eq!(initial.problem, None);
        assert_eq!(initial.choices, BrowserChoices::default());
        assert_eq!(
            store.resolve(UrlClass::General).unwrap(),
            BrowserChoice::SystemDefault
        );
        let saved = store.update(
            BrowserChoices {
                general: installed("firefox.desktop"),
                local: BrowserChoice::SystemDefault,
            },
            initial.revision,
        );
        assert_eq!(saved.problem, None);
        assert_eq!(saved.revision, initial.revision + 1);
        let restarted = BrowserStore::new(Some(dir.path().into()));
        assert_eq!(
            restarted.read().choices.general,
            installed("firefox.desktop")
        );
        assert_eq!(
            restarted.resolve(UrlClass::General).unwrap(),
            installed("firefox.desktop")
        );
        assert_eq!(
            restarted.resolve(UrlClass::Local).unwrap(),
            BrowserChoice::SystemDefault
        );
        let document = fs::read_to_string(dir.path().join(FILE_NAME)).unwrap();
        assert!(document.contains("firefox.desktop") && !document.contains('/'));
    }

    #[test]
    fn stale_revisions_report_changed_and_keep_the_committed_choices() {
        let (_dir, store) = store();
        let initial = store.read();
        let saved = store.update(choices(), initial.revision);
        let stale = store.update(BrowserChoices::default(), initial.revision);
        assert_eq!(stale.problem, Some(BrowserProblem::Changed));
        assert_eq!(stale.choices, saved.choices);
        assert_eq!(store.read().choices, choices());
    }

    #[test]
    fn invalid_identities_are_rejected_before_any_write() {
        let (dir, store) = store();
        let initial = store.read();
        for identity in ["", "/Applications/Evil.app", "C:\\evil.exe", "a;b", "../x"] {
            let rejected = store.update(
                BrowserChoices {
                    general: installed(identity),
                    local: BrowserChoice::SystemDefault,
                },
                initial.revision,
            );
            assert_eq!(
                rejected.problem,
                Some(BrowserProblem::InvalidDocument),
                "{identity:?}"
            );
            assert_eq!(rejected.choices, BrowserChoices::default());
        }
        assert!(!dir.path().join(FILE_NAME).exists());
    }

    #[test]
    fn corrupt_documents_are_visible_fail_closed_and_only_reset_explicitly() {
        for (bytes, problem) in [
            (b"broken".to_vec(), BrowserProblem::InvalidDocument),
            (
                br#"{"version":2,"revision":1,"choices":{}}"#.to_vec(),
                BrowserProblem::UnsupportedVersion,
            ),
            (
                br#"{"version":1,"revision":1,"choices":{"general":{"state":"installed","identity":"../x"},"local":{"state":"system-default"}}}"#.to_vec(),
                BrowserProblem::InvalidDocument,
            ),
            (vec![b' '; 4097], BrowserProblem::InvalidDocument),
        ] {
            let (dir, store) = store();
            let path = dir.path().join(FILE_NAME);
            fs::write(&path, &bytes).unwrap();
            let observed = store.read();
            assert_eq!(observed.problem, Some(problem));
            assert_eq!(store.resolve(UrlClass::General), Err(crate::NativeFailure::StorageUnavailable));
            // Ordinary updates never replace unreadable evidence.
            store.update(choices(), observed.revision);
            assert_eq!(fs::read(&path).unwrap(), bytes);
            let reset = store.reset(observed.revision);
            assert_eq!(reset.problem, None);
            assert_eq!(reset.choices, BrowserChoices::default());
            assert_eq!(BrowserStore::new(Some(dir.path().into())).read().problem, None);
            assert_eq!(store.resolve(UrlClass::Local).unwrap(), BrowserChoice::SystemDefault);
        }
    }

    #[test]
    fn healthy_storage_cannot_be_reset_through_the_corrupt_state_path() {
        let (_dir, store) = store();
        let initial = store.read();
        let saved = store.update(choices(), initial.revision);
        let attempted = store.reset(saved.revision);
        assert_eq!(attempted.choices, choices());
        assert_eq!(store.read().choices, choices());
    }

    #[test]
    fn failed_and_uncertain_publication_never_report_the_requested_choice() {
        let (_dir, store) = store();
        let initial = store.read();
        let failed = store.update_with(choices(), initial.revision, false, |_, _, _| {
            Err(BrowserProblem::WriteFailed)
        });
        assert_eq!(failed.problem, Some(BrowserProblem::WriteFailed));
        assert_eq!(failed.choices, BrowserChoices::default());
        assert_eq!(
            store.update(choices(), failed.revision).choices,
            BrowserChoices::default()
        );
        let inspected = store.read();
        let unknown = store.update_with(
            choices(),
            inspected.revision,
            false,
            |path, value, revision| {
                persist(path, value, revision)?;
                Err(BrowserProblem::OutcomeUnknown)
            },
        );
        assert_eq!(unknown.problem, Some(BrowserProblem::OutcomeUnknown));
        assert_eq!(unknown.choices, BrowserChoices::default());
        assert_eq!(store.read().choices, choices());
    }

    #[test]
    fn external_replacement_is_adopted_by_read_and_resolve_but_not_overwritten() {
        let (dir, store) = store();
        let initial = store.read();
        store.update(choices(), initial.revision);
        let other = BrowserStore::new(Some(dir.path().into()));
        let seen = other.read();
        let next = other.update(BrowserChoices::default(), seen.revision);
        assert_eq!(next.problem, None);
        // The first window still holds the older revision until it reads.
        assert_eq!(
            store.resolve(UrlClass::General).unwrap(),
            BrowserChoice::SystemDefault
        );
        let stale = store.update(choices(), store.current().revision);
        assert_eq!(stale.problem, Some(BrowserProblem::Changed));
        assert_eq!(store.read().choices, BrowserChoices::default());
    }

    #[test]
    fn unavailable_identities_remain_saved_and_resolve_unchanged() {
        let (_dir, store) = store();
        let initial = store.read();
        store.update(choices(), initial.revision);
        // Native resolution returns the saved identity even when the browser
        // was uninstalled; availability is decided only at dispatch time.
        assert_eq!(
            store.resolve(UrlClass::Local).unwrap(),
            installed("com.google.Chrome")
        );
    }

    #[cfg(unix)]
    #[test]
    fn symbolic_links_are_invalid_evidence_and_never_followed() {
        let dir = tempfile::tempdir().unwrap();
        let target = dir.path().join("external");
        fs::write(&target, b"original").unwrap();
        std::os::unix::fs::symlink(&target, dir.path().join(FILE_NAME)).unwrap();
        let store = BrowserStore::new(Some(dir.path().into()));
        let observed = store.read();
        assert_eq!(observed.problem, Some(BrowserProblem::InvalidDocument));
        store.update(choices(), observed.revision);
        assert_eq!(fs::read(&target).unwrap(), b"original");
    }

    #[test]
    fn missing_storage_is_unavailable_and_fails_closed() {
        let store = BrowserStore::new(None);
        assert_eq!(store.read().problem, Some(BrowserProblem::Unavailable));
        assert_eq!(
            store.resolve(UrlClass::General),
            Err(crate::NativeFailure::StorageUnavailable)
        );
    }
}
