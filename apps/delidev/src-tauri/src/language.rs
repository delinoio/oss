// SPDX-License-Identifier: Apache-2.0
use std::{
    fs::{self, File, OpenOptions},
    io::{Read, Write},
    path::{Path, PathBuf},
    sync::Mutex,
};

use serde::{Deserialize, Serialize};

const DOCUMENT_LIMIT: u64 = 4096;
static ACTIVE: std::sync::atomic::AtomicU8 = std::sync::atomic::AtomicU8::new(0);

pub fn active() -> SupportedLanguage {
    if ACTIVE.load(std::sync::atomic::Ordering::Acquire) == 1 {
        SupportedLanguage::Korean
    } else {
        SupportedLanguage::English
    }
}

/// Native presentation only; the process store remains preference authority.
pub fn activate(language: SupportedLanguage) {
    ACTIVE.store(
        if language == SupportedLanguage::Korean {
            1
        } else {
            0
        },
        std::sync::atomic::Ordering::Release,
    );
}

#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum LanguagePreference {
    #[default]
    System,
    #[serde(rename = "en")]
    English,
    #[serde(rename = "ko")]
    Korean,
}

#[derive(Clone, Copy, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum LanguageProblem {
    Unavailable,
    ReadFailed,
    InvalidDocument,
    UnsupportedVersion,
    WriteFailed,
    OutcomeUnknown,
    Changed,
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct LanguageSnapshot {
    pub revision: u32,
    pub language: LanguagePreference,
    pub resolved_language: SupportedLanguage,
    pub problem: Option<LanguageProblem>,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Document {
    version: u32,
    language: LanguagePreference,
}

/// One process-wide device preference, independent of every server connection.
/// The lock covers inspection and atomic publication; revisions order IPC
/// replies and events even when their delivery order differs from their commit
/// order.
pub struct LanguageStore {
    path: Option<PathBuf>,
    state: Mutex<LanguageSnapshot>,
    presentation: Mutex<()>,
}

impl LanguageStore {
    pub fn new(config_dir: Option<PathBuf>) -> Self {
        Self {
            path: config_dir.map(|directory| directory.join("language.json")),
            presentation: Mutex::new(()),
            state: Mutex::new(LanguageSnapshot {
                revision: 0,
                language: LanguagePreference::System,
                resolved_language: SupportedLanguage::English,
                problem: Some(LanguageProblem::Unavailable),
            }),
        }
    }

    pub fn current(&self) -> LanguageSnapshot {
        self.state
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .clone()
    }

    /// Serialize native reads/commits together with their presentation effects.
    pub fn presentation(&self) -> std::sync::MutexGuard<'_, ()> {
        self.presentation
            .lock()
            .unwrap_or_else(|error| error.into_inner())
    }

    pub fn read(&self) -> LanguageSnapshot {
        self.read_with(sys_locale::get_locales())
    }

    fn read_with(&self, languages: impl IntoIterator<Item = String>) -> LanguageSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        let previous = state.clone();
        match self.path.as_deref().map(inspect) {
            Some(Ok(language)) => {
                state.language = language;
                state.problem = None;
            }
            Some(Err(problem)) => {
                if matches!(
                    problem,
                    LanguageProblem::InvalidDocument | LanguageProblem::UnsupportedVersion
                ) {
                    state.language = LanguagePreference::System;
                }
                state.problem = Some(problem);
                tracing::warn!(operation = "language_read", ?problem);
            }
            None => state.problem = Some(LanguageProblem::Unavailable),
        }
        state.resolved_language = resolve(state.language, languages);
        if state.revision == 0
            || state.language != previous.language
            || state.resolved_language != previous.resolved_language
            || state.problem != previous.problem
        {
            advance(&mut state);
        }
        state.clone()
    }

    pub fn update(&self, language: LanguagePreference, expected_revision: u32) -> LanguageSnapshot {
        self.update_with(language, expected_revision, persist)
    }

    fn update_with(
        &self,
        language: LanguagePreference,
        expected_revision: u32,
        write: impl FnOnce(&Path, LanguagePreference) -> Result<(), LanguageProblem>,
    ) -> LanguageSnapshot {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        if state.revision != expected_revision {
            // Do not mutate the shared committed state for a stale caller. It
            // must inspect again before choosing against the current selection.
            let mut snapshot = state.clone();
            snapshot.problem = Some(LanguageProblem::Changed);
            return snapshot;
        }
        if state.problem.is_some() || state.revision == u32::MAX {
            return state.clone();
        }
        let result = self
            .path
            .as_deref()
            .ok_or(LanguageProblem::Unavailable)
            .and_then(|path| {
                // Reinspect before replacement, preserving external
                // invalid/newer documents instead of silently
                // upgrading or overwriting them.
                let current = inspect(path)?;
                if current != state.language {
                    return Err(LanguageProblem::Changed);
                }
                write(path, language)
            });
        match result {
            Ok(()) => {
                state.language = language;
                state.resolved_language = resolve(language, sys_locale::get_locales());
                tracing::info!(operation = "language_update", state = "committed");
            }
            Err(problem) => {
                state.problem = Some(problem);
                tracing::warn!(operation = "language_update", ?problem);
            }
        }
        advance(&mut state);
        state.clone()
    }
}

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
pub enum SupportedLanguage {
    #[serde(rename = "en")]
    English,
    #[serde(rename = "ko")]
    Korean,
}

pub fn resolve(
    preference: LanguagePreference,
    languages: impl IntoIterator<Item = String>,
) -> SupportedLanguage {
    match preference {
        LanguagePreference::English => return SupportedLanguage::English,
        LanguagePreference::Korean => return SupportedLanguage::Korean,
        LanguagePreference::System => {}
    }
    for locale in languages.into_iter().take(64) {
        if locale.len() > 128 {
            continue;
        }
        let language = locale.trim().split(['-', '_']).next().unwrap_or("");
        if language.eq_ignore_ascii_case("en") {
            return SupportedLanguage::English;
        }
        if language.eq_ignore_ascii_case("ko") {
            return SupportedLanguage::Korean;
        }
    }
    SupportedLanguage::English
}

fn advance(state: &mut LanguageSnapshot) {
    // Never wrap into an older revision. Exhaustion remains read-only until
    // restart instead of letting a delayed event replace newer state.
    if let Some(revision) = state.revision.checked_add(1) {
        state.revision = revision;
    } else {
        state.problem = Some(LanguageProblem::Unavailable);
    }
}

fn inspect(path: &Path) -> Result<LanguagePreference, LanguageProblem> {
    let metadata = match fs::symlink_metadata(path) {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            return Ok(LanguagePreference::System);
        }
        Err(_) => return Err(LanguageProblem::ReadFailed),
    };
    if !metadata.is_file() || metadata.len() > DOCUMENT_LIMIT {
        return Err(LanguageProblem::InvalidDocument);
    }
    let file = File::open(path).map_err(|_| LanguageProblem::ReadFailed)?;
    let mut bytes = Vec::new();
    file.take(DOCUMENT_LIMIT + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| LanguageProblem::ReadFailed)?;
    if bytes.len() as u64 > DOCUMENT_LIMIT {
        return Err(LanguageProblem::InvalidDocument);
    }
    let document: Document =
        serde_json::from_slice(&bytes).map_err(|_| LanguageProblem::InvalidDocument)?;
    if document.version != 1 {
        return Err(LanguageProblem::UnsupportedVersion);
    }
    Ok(document.language)
}

fn persist(path: &Path, language: LanguagePreference) -> Result<(), LanguageProblem> {
    let parent = path.parent().ok_or(LanguageProblem::WriteFailed)?;
    fs::create_dir_all(parent).map_err(|_| LanguageProblem::WriteFailed)?;
    let temporary = parent.join(format!(".language-{}.tmp", uuid::Uuid::now_v7()));
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
        .map_err(|_| LanguageProblem::WriteFailed)?;
    let scratch = &temporary;
    let result = (move || {
        let bytes = serde_json::to_vec(&Document {
            version: 1,
            language,
        })
        .map_err(|_| LanguageProblem::WriteFailed)?;
        file.write_all(&bytes)
            .and_then(|_| file.sync_all())
            .map_err(|_| LanguageProblem::WriteFailed)?;
        drop(file);
        replace(scratch, path).map_err(|_| LanguageProblem::WriteFailed)?;
        // Publication happened already. A failed directory sync is uncertain,
        // so retain the prior selection and require a fresh read before retry.
        #[cfg(unix)]
        File::open(parent)
            .and_then(|directory| directory.sync_all())
            .map_err(|_| LanguageProblem::OutcomeUnknown)?;
        Ok(())
    })();
    let _ = fs::remove_file(temporary);
    result
}

#[cfg(not(windows))]
fn replace(source: &Path, target: &Path) -> std::io::Result<()> {
    fs::rename(source, target)
}

#[cfg(windows)]
fn replace(source: &Path, target: &Path) -> std::io::Result<()> {
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
    fn system_language_uses_the_first_supported_preference() {
        for (locales, expected) in [
            (vec!["ja-JP", "ko_KR", "en-US"], SupportedLanguage::Korean),
            (vec!["EN-gb", "ko-KR"], SupportedLanguage::English),
            (vec!["fr-FR"], SupportedLanguage::English),
            (vec![], SupportedLanguage::English),
        ] {
            assert_eq!(
                resolve(
                    LanguagePreference::System,
                    locales.into_iter().map(str::to_owned)
                ),
                expected
            );
        }
        assert_eq!(
            resolve(LanguagePreference::Korean, ["en-US".into()]),
            SupportedLanguage::Korean
        );
        assert_eq!(
            resolve(LanguagePreference::English, ["ko-KR".into()]),
            SupportedLanguage::English
        );
    }

    #[test]
    fn system_change_advances_revision_without_writing_a_preference() {
        let directory = tempfile::tempdir().unwrap();
        let store = LanguageStore::new(Some(directory.path().into()));
        let english = store.read_with(["en-US".into()]);
        let korean = store.read_with(["ko-KR".into()]);
        assert!(korean.revision > english.revision);
        assert_eq!(korean.resolved_language, SupportedLanguage::Korean);
        assert!(!directory.path().join("language.json").exists());
    }

    #[test]
    fn failed_or_uncertain_publication_requires_a_fresh_read() {
        let directory = tempfile::tempdir().unwrap();
        let store = LanguageStore::new(Some(directory.path().into()));
        let initial = store.read();
        let dark = store.update(LanguagePreference::Korean, initial.revision);
        let failure = store.update_with(LanguagePreference::English, dark.revision, |_, _| {
            Err(LanguageProblem::WriteFailed)
        });
        assert_eq!(failure.language, LanguagePreference::Korean);
        assert_eq!(failure.problem, Some(LanguageProblem::WriteFailed));
        assert_eq!(
            store
                .update(LanguagePreference::English, failure.revision)
                .language,
            LanguagePreference::Korean
        );
        let inspected = store.read();
        let uncertain = store.update_with(
            LanguagePreference::English,
            inspected.revision,
            |path, language| {
                persist(path, language)?;
                Err(LanguageProblem::OutcomeUnknown)
            },
        );
        assert_eq!(uncertain.language, LanguagePreference::Korean);
        assert_eq!(uncertain.problem, Some(LanguageProblem::OutcomeUnknown));
        assert_eq!(
            store
                .update(LanguagePreference::Korean, uncertain.revision)
                .language,
            LanguagePreference::Korean
        );
        let inspected = store.read();
        assert_eq!(inspected.language, LanguagePreference::English);
        assert_eq!(inspected.problem, None);
        assert_eq!(
            store
                .update(LanguagePreference::System, inspected.revision)
                .problem,
            None
        );
    }

    #[test]
    fn unchanged_reads_keep_all_windows_on_the_same_revision() {
        let directory = tempfile::tempdir().unwrap();
        let store = LanguageStore::new(Some(directory.path().into()));
        let initial = store.read();
        assert_eq!(store.read(), initial);
        assert_eq!(
            store
                .update(LanguagePreference::Korean, initial.revision)
                .problem,
            None
        );
    }

    #[cfg(unix)]
    #[test]
    fn linked_storage_is_preserved_without_touching_its_target() {
        let directory = tempfile::tempdir().unwrap();
        let target = directory.path().join("external.json");
        fs::write(&target, br#"{"version":1,"language":"ko"}"#).unwrap();
        let original = fs::read(&target).unwrap();
        std::os::unix::fs::symlink(&target, directory.path().join("language.json")).unwrap();
        let store = LanguageStore::new(Some(directory.path().into()));
        let snapshot = store.read();
        assert_eq!(snapshot.problem, Some(LanguageProblem::InvalidDocument));
        store.update(LanguagePreference::English, snapshot.revision);
        assert_eq!(fs::read(target).unwrap(), original);
        assert!(
            fs::symlink_metadata(directory.path().join("language.json"))
                .unwrap()
                .is_symlink()
        );
    }

    #[test]
    fn default_restart_and_stale_selection() {
        let directory = tempfile::tempdir().unwrap();
        let store = LanguageStore::new(Some(directory.path().into()));
        let initial = store.read();
        assert_eq!(initial.language, LanguagePreference::System);
        assert_eq!(initial.problem, None);
        let dark = store.update(LanguagePreference::Korean, initial.revision);
        assert_eq!(dark.language, LanguagePreference::Korean);
        assert_eq!(dark.problem, None);
        assert_eq!(
            store
                .update(LanguagePreference::English, initial.revision)
                .problem,
            Some(LanguageProblem::Changed)
        );
        assert_eq!(store.read().language, LanguagePreference::Korean);
        let restarted = LanguageStore::new(Some(directory.path().into()));
        let observed = restarted.read();
        assert_eq!(observed.language, LanguagePreference::Korean);
        assert_eq!(
            restarted
                .update(LanguagePreference::English, observed.revision)
                .language,
            LanguagePreference::English
        );
        assert_eq!(restarted.read().language, LanguagePreference::English);
    }

    #[test]
    fn invalid_and_newer_documents_remain_intact() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("language.json");
        for bytes in [
            b"broken".as_slice(),
            br#"{"version":2,"language":"ko"}"#,
            br#"{"version":1,"language":"other"}"#,
            br#"{"version":1,"language":"ko","extra":true}"#,
        ] {
            fs::write(&path, bytes).unwrap();
            let store = LanguageStore::new(Some(directory.path().into()));
            let observed = store.read();
            assert_eq!(observed.language, LanguagePreference::System);
            assert!(observed.problem.is_some());
            assert_eq!(
                store
                    .update(LanguagePreference::English, observed.revision)
                    .language,
                LanguagePreference::System
            );
            assert_eq!(fs::read(&path).unwrap(), bytes);
        }
    }

    #[test]
    fn external_change_requires_inspection_before_retry() {
        let directory = tempfile::tempdir().unwrap();
        let store = LanguageStore::new(Some(directory.path().into()));
        let initial = store.read();
        fs::write(
            directory.path().join("language.json"),
            br#"{"version":1,"language":"ko"}"#,
        )
        .unwrap();
        let rejected = store.update(LanguagePreference::English, initial.revision);
        assert_eq!(rejected.language, LanguagePreference::System);
        assert_eq!(rejected.problem, Some(LanguageProblem::Changed));
        let observed = store.read();
        assert_eq!(observed.language, LanguagePreference::Korean);
        assert_eq!(
            store
                .update(LanguagePreference::English, observed.revision)
                .problem,
            None
        );
    }

    #[test]
    fn write_failure_keeps_committed_selection_and_blocks_retry() {
        let directory = tempfile::tempdir().unwrap();
        let store = LanguageStore::new(Some(directory.path().into()));
        let initial = store.read();
        let committed = store.update(LanguagePreference::Korean, initial.revision);
        fs::remove_file(directory.path().join("language.json")).unwrap();
        fs::remove_dir(directory.path()).unwrap();
        fs::write(directory.path(), b"not a directory").unwrap();
        let failed = store.update(LanguagePreference::English, committed.revision);
        assert_eq!(failed.language, LanguagePreference::Korean);
        assert!(failed.problem.is_some());
        assert_eq!(
            store
                .update(LanguagePreference::System, failed.revision)
                .language,
            LanguagePreference::Korean
        );
        fs::remove_file(directory.path()).unwrap();
    }
}
