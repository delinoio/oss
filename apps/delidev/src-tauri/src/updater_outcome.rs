// SPDX-License-Identifier: Apache-2.0
//! Private durable store for native update evidence that must outlive the
//! process. Each record is keyed by the original operation ID and is written
//! under the connector root's `desktop-updates` scope, beside the Go-owned
//! once-only journal. Records only carry the identities and the exact phase
//! needed to repeat an idempotent offline `native-outcome` write. They never
//! grant installation authority, and an unreadable or foreign record is kept
//! rather than deleted or trusted.
use super::{
    updater::{Phase, no_symlink_ancestors},
    *,
};

/// Directory (below the connector root) that owns every retained record.
pub(crate) const STORE_DIRECTORY: &str = "desktop-updates/native-outcomes";
const RECORD_LIMIT: u64 = 4096;
const MAX_ENTRIES: usize = 32;
const RECORD_VERSION: u32 = 1;

/// Closed kinds of retained evidence. Later contracts extend this enum rather
/// than inventing free-form markers.
#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum RecordKind {
    /// The exact terminal phase an accepted native installation observed but
    /// could not yet publish to the original Installing journal.
    Outcome,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct OutcomeRecord {
    version: u32,
    kind: RecordKind,
    pub server_id: String,
    pub operation_id: String,
    pub revision: u64,
    pub generation: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub saved_connection: Option<String>,
    pub phase: Phase,
}

impl OutcomeRecord {
    pub fn outcome(
        server_id: &str,
        operation_id: &str,
        revision: u64,
        generation: &str,
        saved_connection: Option<&str>,
        phase: Phase,
    ) -> Result<Self> {
        let record = Self {
            version: RECORD_VERSION,
            kind: RecordKind::Outcome,
            server_id: server_id.into(),
            operation_id: operation_id.into(),
            revision,
            generation: generation.into(),
            saved_connection: saved_connection.map(str::to_owned),
            phase,
        };
        record.validate()?;
        Ok(record)
    }

    pub(crate) fn validate(&self) -> Result<()> {
        for value in [&self.server_id, &self.operation_id, &self.generation] {
            canonical_id(value)?;
        }
        if let Some(saved) = &self.saved_connection {
            canonical_id(saved)?;
        }
        if self.version != RECORD_VERSION
            || self.kind != RecordKind::Outcome
            || self.revision == 0
            || self.revision >= 1 << 63
            || !matches!(
                self.phase,
                Phase::Installed | Phase::Failed | Phase::Uncertain
            )
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(())
    }
}

/// One scanned entry. An unreadable entry is reported, never deleted.
#[derive(Debug, PartialEq, Eq)]
pub enum StoredEntry {
    Record(OutcomeRecord),
    /// Corrupt, oversized, foreign-named or otherwise unparsable evidence.
    /// The optional name is logged only when it is itself a canonical ID.
    Unreadable(Option<String>),
}

pub struct OutcomeStore {
    dir: PathBuf,
}

impl OutcomeStore {
    pub fn new(root: &Path) -> Self {
        Self {
            dir: root.join(STORE_DIRECTORY),
        }
    }

    fn path(&self, operation_id: &str) -> PathBuf {
        self.dir.join(format!("{operation_id}.json"))
    }

    /// Returns false when the store directory does not exist. Existing
    /// directories must be owner-only regular directories without symlink
    /// ancestors; their permissions are never silently repaired.
    fn check_dir(&self, create: bool) -> Result<bool> {
        let parent = self.dir.parent().ok_or(NativeFailure::InvalidEvidence)?;
        // The Go-owned scope must already exist; the store never creates it.
        if !parent.is_dir() {
            return if create {
                Err(NativeFailure::StorageUnavailable)
            } else {
                Ok(false)
            };
        }
        let mut created = false;
        match fs::symlink_metadata(&self.dir) {
            Ok(_) => {}
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                if !create {
                    return Ok(false);
                }
                fs::create_dir(&self.dir).map_err(|_| NativeFailure::StorageUnavailable)?;
                created = true;
            }
            Err(_) => return Err(NativeFailure::StorageUnavailable),
        }
        if !no_symlink_ancestors(&self.dir) {
            return Err(NativeFailure::InvalidEvidence);
        }
        let metadata = fs::metadata(&self.dir).map_err(|_| NativeFailure::StorageUnavailable)?;
        if !metadata.is_dir() {
            return Err(NativeFailure::InvalidEvidence);
        }
        #[cfg(unix)]
        {
            use std::os::unix::fs::{MetadataExt, PermissionsExt};
            if created {
                fs::set_permissions(&self.dir, fs::Permissions::from_mode(0o700))
                    .map_err(|_| NativeFailure::StorageUnavailable)?;
            }
            let owner = fs::metadata(parent)
                .map_err(|_| NativeFailure::StorageUnavailable)?
                .uid();
            let current = fs::metadata(&self.dir).map_err(|_| NativeFailure::StorageUnavailable)?;
            if current.uid() != owner || current.permissions().mode() & 0o077 != 0 {
                return Err(NativeFailure::PermissionDenied);
            }
        }
        #[cfg(not(unix))]
        let _ = created;
        Ok(true)
    }

    /// Reads one record. `Ok(None)` means the file does not exist.
    fn read(&self, operation_id: &str) -> Result<Option<OutcomeRecord>> {
        let path = self.path(operation_id);
        let metadata = match fs::symlink_metadata(&path) {
            Ok(v) => v,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(None),
            Err(_) => return Err(NativeFailure::StorageUnavailable),
        };
        if !metadata.is_file() || metadata.file_type().is_symlink() || metadata.len() > RECORD_LIMIT
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        let mut bytes = Vec::new();
        File::open(&path)
            .map_err(|_| NativeFailure::StorageUnavailable)?
            .take(RECORD_LIMIT + 1)
            .read_to_end(&mut bytes)
            .map_err(|_| NativeFailure::StorageUnavailable)?;
        if bytes.len() as u64 > RECORD_LIMIT {
            return Err(NativeFailure::InvalidEvidence);
        }
        let record: OutcomeRecord =
            serde_json::from_slice(&bytes).map_err(|_| NativeFailure::InvalidEvidence)?;
        record.validate()?;
        if record.operation_id != operation_id {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(Some(record))
    }

    /// Durably stores the record before any settlement attempt. An identical
    /// retained record is accepted; a different record for the same operation
    /// is never overwritten because it is evidence of a conflicting outcome.
    pub fn persist(&self, record: &OutcomeRecord) -> Result<()> {
        record.validate()?;
        self.check_dir(true)?;
        match self.read(&record.operation_id)? {
            Some(existing) if existing == *record => return Ok(()),
            Some(_) => return Err(NativeFailure::InvalidEvidence),
            None => {}
        }
        let bytes = serde_json::to_vec(record).map_err(|_| NativeFailure::InvalidEvidence)?;
        let temporary = self.dir.join(format!(
            ".{}.{}.tmp",
            record.operation_id,
            uuid::Uuid::now_v7()
        ));
        let mut options = fs::OpenOptions::new();
        options.write(true).create_new(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let written = (|| -> std::io::Result<()> {
            let mut file = options.open(&temporary)?;
            file.write_all(&bytes)?;
            file.sync_all()?;
            drop(file);
            fs::rename(&temporary, self.path(&record.operation_id))?;
            #[cfg(unix)]
            File::open(&self.dir)?.sync_all()?;
            Ok(())
        })();
        if written.is_err() {
            let _ = fs::remove_file(&temporary);
            return Err(NativeFailure::StorageUnavailable);
        }
        Ok(())
    }

    /// Lists at most `MAX_ENTRIES` retained entries in stable order. Temporary
    /// files from an interrupted write are ignored and never trusted.
    pub fn scan(&self) -> Result<Vec<StoredEntry>> {
        if !self.check_dir(false)? {
            return Ok(Vec::new());
        }
        let mut names: Vec<String> = fs::read_dir(&self.dir)
            .map_err(|_| NativeFailure::StorageUnavailable)?
            .filter_map(|entry| entry.ok())
            .map(|entry| entry.file_name().to_string_lossy().into_owned())
            .filter(|name| !name.starts_with('.'))
            .collect();
        names.sort();
        names.truncate(MAX_ENTRIES);
        Ok(names
            .into_iter()
            .map(|name| {
                let stem = name.strip_suffix(".json").unwrap_or("");
                if canonical_id(stem).is_err() {
                    return StoredEntry::Unreadable(None);
                }
                match self.read(stem) {
                    Ok(Some(record)) => StoredEntry::Record(record),
                    _ => StoredEntry::Unreadable(Some(stem.to_owned())),
                }
            })
            .collect())
    }

    /// Removes the record only when the stored bytes still equal the exact
    /// record that was verified as settled. Anything else is retained.
    pub fn retire(&self, record: &OutcomeRecord) -> Result<()> {
        if !self.check_dir(false)? {
            return Ok(());
        }
        match self.read(&record.operation_id)? {
            None => Ok(()),
            Some(existing) if existing == *record => {
                fs::remove_file(self.path(&record.operation_id))
                    .map_err(|_| NativeFailure::StorageUnavailable)?;
                #[cfg(unix)]
                File::open(&self.dir)
                    .and_then(|d| d.sync_all())
                    .map_err(|_| NativeFailure::StorageUnavailable)?;
                Ok(())
            }
            Some(_) => Err(NativeFailure::InvalidEvidence),
        }
    }
}

#[cfg(all(test, unix))]
mod tests {
    use std::os::unix::fs::PermissionsExt;

    use super::*;

    fn record(phase: Phase) -> OutcomeRecord {
        OutcomeRecord::outcome(
            &uuid::Uuid::now_v7().to_string(),
            &uuid::Uuid::now_v7().to_string(),
            3,
            &uuid::Uuid::now_v7().to_string(),
            Some(&uuid::Uuid::now_v7().to_string()),
            phase,
        )
        .unwrap()
    }

    fn scope() -> (tempfile::TempDir, PathBuf) {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        fs::create_dir(root.join("desktop-updates")).unwrap();
        (temp, root)
    }

    #[test]
    fn persist_is_atomic_private_and_idempotent() {
        let (_temp, root) = scope();
        let store = OutcomeStore::new(&root);
        let record = record(Phase::Uncertain);
        store.persist(&record).unwrap();
        store.persist(&record).unwrap();
        let directory = root.join(STORE_DIRECTORY);
        assert_eq!(
            fs::metadata(&directory).unwrap().permissions().mode() & 0o777,
            0o700
        );
        let names: Vec<_> = fs::read_dir(&directory)
            .unwrap()
            .map(|e| e.unwrap().file_name().into_string().unwrap())
            .collect();
        assert_eq!(names, vec![format!("{}.json", record.operation_id)]);
        assert_eq!(store.scan().unwrap(), vec![StoredEntry::Record(record)]);
    }

    #[test]
    fn a_conflicting_phase_never_replaces_retained_evidence() {
        let (_temp, root) = scope();
        let store = OutcomeStore::new(&root);
        let first = record(Phase::Installed);
        store.persist(&first).unwrap();
        let mut second = first.clone();
        second.phase = Phase::Failed;
        assert!(matches!(
            store.persist(&second),
            Err(NativeFailure::InvalidEvidence)
        ));
        assert_eq!(store.scan().unwrap(), vec![StoredEntry::Record(first)]);
    }

    #[test]
    fn retire_removes_only_the_exact_record() {
        let (_temp, root) = scope();
        let store = OutcomeStore::new(&root);
        let stored = record(Phase::Installed);
        store.persist(&stored).unwrap();
        let mut other = stored.clone();
        other.phase = Phase::Failed;
        assert!(store.retire(&other).is_err());
        assert_eq!(store.scan().unwrap().len(), 1);
        store.retire(&stored).unwrap();
        assert!(store.scan().unwrap().is_empty());
        store.retire(&stored).unwrap();
    }

    #[test]
    fn missing_scope_or_directory_does_not_create_foreign_state() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let store = OutcomeStore::new(&root);
        assert!(store.scan().unwrap().is_empty());
        assert!(matches!(
            store.persist(&record(Phase::Failed)),
            Err(NativeFailure::StorageUnavailable)
        ));
        assert!(!root.join("desktop-updates").exists());
    }

    #[test]
    fn permissive_store_directory_is_refused_not_repaired() {
        let (_temp, root) = scope();
        let directory = root.join(STORE_DIRECTORY);
        fs::create_dir(&directory).unwrap();
        fs::set_permissions(&directory, fs::Permissions::from_mode(0o755)).unwrap();
        let store = OutcomeStore::new(&root);
        assert!(matches!(
            store.persist(&record(Phase::Failed)),
            Err(NativeFailure::PermissionDenied)
        ));
        assert!(store.scan().is_err());
        assert_eq!(
            fs::metadata(&directory).unwrap().permissions().mode() & 0o777,
            0o755
        );
    }

    #[test]
    fn symlinked_record_is_unreadable_evidence() {
        let (_temp, root) = scope();
        let store = OutcomeStore::new(&root);
        store.persist(&record(Phase::Failed)).unwrap();
        let outside = root.join("outside.json");
        let linked = uuid::Uuid::now_v7().to_string();
        fs::write(&outside, b"{}").unwrap();
        std::os::unix::fs::symlink(
            &outside,
            root.join(STORE_DIRECTORY).join(format!("{linked}.json")),
        )
        .unwrap();
        let entries = store.scan().unwrap();
        assert_eq!(entries.len(), 2);
        assert!(entries.contains(&StoredEntry::Unreadable(Some(linked))));
    }

    #[test]
    fn records_reject_invalid_identities_and_non_terminal_phases() {
        let id = uuid::Uuid::now_v7().to_string();
        assert!(OutcomeRecord::outcome(&id, &id, 1, &id, None, Phase::Installing).is_err());
        assert!(OutcomeRecord::outcome(&id, &id, 0, &id, None, Phase::Failed).is_err());
        assert!(OutcomeRecord::outcome(&id, &id, 1 << 63, &id, None, Phase::Failed).is_err());
        assert!(OutcomeRecord::outcome(&id, "x", 1, &id, None, Phase::Failed).is_err());
        assert!(OutcomeRecord::outcome(&id, &id, 1, &id, Some("x"), Phase::Failed).is_err());
    }
}
