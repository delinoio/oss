// SPDX-License-Identifier: Apache-2.0
//! Private durable store for native update evidence that must outlive the
//! process. Each record is keyed by the original operation ID and is written
//! under the connector root's `desktop-updates` scope, beside the Go-owned
//! once-only journal. Records only carry the identities and the exact phase
//! needed to repeat an idempotent offline `native-outcome` write. They never
//! grant installation authority, and an unreadable or foreign record is kept
//! rather than deleted or trusted.
use std::ops::ControlFlow;

use super::{
    updater::{Phase, no_symlink_ancestors},
    *,
};

/// Directory (below the connector root) that owns every retained record.
pub(crate) const STORE_DIRECTORY: &str = "desktop-updates/native-outcomes";
const RECORD_LIMIT: u64 = 4096;
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
        // Validate it and every ancestor without following links before any
        // creation, so a linked scope cannot redirect the store elsewhere.
        match fs::symlink_metadata(parent) {
            Ok(m) if m.file_type().is_symlink() => return Err(NativeFailure::InvalidEvidence),
            Ok(m) if m.is_dir() => {}
            Ok(_) => return Err(NativeFailure::InvalidEvidence),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                return if create {
                    Err(NativeFailure::StorageUnavailable)
                } else {
                    Ok(false)
                };
            }
            Err(_) => return Err(NativeFailure::StorageUnavailable),
        }
        if !no_symlink_ancestors(parent) {
            return Err(NativeFailure::InvalidEvidence);
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

    /// Reads the bounded raw bytes of one regular file. `Ok(None)` means the
    /// file does not exist.
    fn read_bytes(path: &Path) -> Result<Option<Vec<u8>>> {
        let metadata = match fs::symlink_metadata(path) {
            Ok(v) => v,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(None),
            Err(_) => return Err(NativeFailure::StorageUnavailable),
        };
        if !metadata.is_file() || metadata.file_type().is_symlink() || metadata.len() > RECORD_LIMIT
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        let mut bytes = Vec::new();
        File::open(path)
            .map_err(|_| NativeFailure::StorageUnavailable)?
            .take(RECORD_LIMIT + 1)
            .read_to_end(&mut bytes)
            .map_err(|_| NativeFailure::StorageUnavailable)?;
        if bytes.len() as u64 > RECORD_LIMIT {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(Some(bytes))
    }

    /// Reads one record. `Ok(None)` means the file does not exist.
    fn read(&self, operation_id: &str) -> Result<Option<OutcomeRecord>> {
        let Some(bytes) = Self::read_bytes(&self.path(operation_id))? else {
            return Ok(None);
        };
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
        // A new store directory is only a directory entry in its parent until
        // that parent is synced. Sync it on every persistence so a crash after
        // the directory was created but before this sync cannot lose it.
        #[cfg(unix)]
        if let Some(parent) = self.dir.parent() {
            File::open(parent)
                .and_then(|d| d.sync_all())
                .map_err(|_| NativeFailure::StorageUnavailable)?;
        }
        match self.read(&record.operation_id)? {
            Some(existing) if existing == *record => return Ok(()),
            Some(_) => return Err(NativeFailure::InvalidEvidence),
            None => {}
        }
        self.publish(record)
    }

    /// Publishes a fully written, fsynced temporary file under the final name
    /// with a no-replace primitive. A hard link fails atomically when the
    /// destination exists, so a record created after the preceding read is
    /// never overwritten; it is reread and accepted only when identical.
    fn publish(&self, record: &OutcomeRecord) -> Result<()> {
        let bytes = serde_json::to_vec(record).map_err(|_| NativeFailure::InvalidEvidence)?;
        let destination = self.path(&record.operation_id);
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
            fs::hard_link(&temporary, &destination)
        })();
        let _ = fs::remove_file(&temporary);
        match written {
            Ok(()) => {
                #[cfg(unix)]
                File::open(&self.dir)
                    .and_then(|d| d.sync_all())
                    .map_err(|_| NativeFailure::StorageUnavailable)?;
                Ok(())
            }
            Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => {
                match self.read(&record.operation_id)? {
                    Some(existing) if existing == *record => Ok(()),
                    _ => Err(NativeFailure::InvalidEvidence),
                }
            }
            Err(_) => Err(NativeFailure::StorageUnavailable),
        }
    }

    /// Loads the one entry keyed by `operation_id` without enumerating the
    /// directory, so unrelated or unreadable evidence can never hide it.
    /// `Ok(None)` means no file exists; an unreadable record is reported as
    /// such and stays on disk.
    pub fn load(&self, operation_id: &str) -> Result<Option<StoredEntry>> {
        canonical_id(operation_id)?;
        if !self.check_dir(false)? {
            return Ok(None);
        }
        Ok(match self.read(operation_id) {
            Ok(None) => None,
            Ok(Some(record)) => Some(StoredEntry::Record(record)),
            Err(_) => Some(StoredEntry::Unreadable(Some(operation_id.to_owned()))),
        })
    }

    /// Collects every retained entry. This buffers the whole listing, so it is
    /// only for tests and diagnostics; production passes stream through
    /// [`OutcomeStore::for_each`].
    pub fn scan(&self) -> Result<Vec<StoredEntry>> {
        let mut entries = Vec::new();
        self.for_each(|entry| {
            entries.push(entry);
            ControlFlow::Continue(())
        })?;
        Ok(entries)
    }

    /// Streams every retained entry to `visit` in a single directory pass
    /// until `visit` breaks. Directory entries are read lazily, so memory
    /// stays constant and the work is linear in the number of entries no
    /// matter how many corrupt, misnamed or otherwise unreadable entries the
    /// store holds. Those are reported to `visit` and never deleted. Because
    /// `visit` runs between directory reads, a caller can stop the walk (for
    /// example on Quit) before the rest of a very large store is listed.
    /// Retained records that stay unresolved never hide later ones: every
    /// entry that exists for the whole walk is visited exactly once, and an
    /// entry removed by `visit` itself is not revisited. Temporary and claimed
    /// files from an interrupted write or retirement are dot-prefixed and are
    /// ignored and never trusted.
    pub fn for_each(&self, mut visit: impl FnMut(StoredEntry) -> ControlFlow<()>) -> Result<()> {
        if !self.check_dir(false)? {
            return Ok(());
        }
        let entries = fs::read_dir(&self.dir).map_err(|_| NativeFailure::StorageUnavailable)?;
        for entry in entries {
            let Ok(entry) = entry else { continue };
            let name = entry.file_name().to_string_lossy().into_owned();
            if name.starts_with('.') {
                continue;
            }
            let stem = name.strip_suffix(".json").unwrap_or("");
            let entry = if canonical_id(stem).is_err() {
                StoredEntry::Unreadable(None)
            } else {
                match self.read(stem) {
                    Ok(Some(record)) => StoredEntry::Record(record),
                    // A file that disappeared after listing is not evidence.
                    Ok(None) => continue,
                    Err(_) => StoredEntry::Unreadable(Some(stem.to_owned())),
                }
            };
            if visit(entry).is_break() {
                break;
            }
        }
        Ok(())
    }

    /// Removes the record only when the stored bytes are exactly the bytes
    /// written for the record that was verified as settled. A differently
    /// encoded or replaced file is retained, even when it parses to an equal
    /// record.
    pub fn retire(&self, record: &OutcomeRecord) -> Result<()> {
        self.retire_with(record, || {})
    }

    /// `window` runs between the early comparison and the removal so tests can
    /// model a writer racing the retirement.
    fn retire_with(&self, record: &OutcomeRecord, window: impl FnOnce()) -> Result<()> {
        if !self.check_dir(false)? {
            return Ok(());
        }
        let expected = serde_json::to_vec(record).map_err(|_| NativeFailure::InvalidEvidence)?;
        let path = self.path(&record.operation_id);
        match Self::read_bytes(&path)? {
            None => return Ok(()),
            Some(stored) if stored == expected => {}
            Some(_) => return Err(NativeFailure::InvalidEvidence),
        }
        window();
        // Take exclusive ownership of whatever now holds the name with an
        // atomic rename, then verify and delete only that object. A file that
        // replaced the verified one in the meantime is restored, never
        // unlinked.
        let claimed = self.dir.join(format!(
            ".{}.{}.retire",
            record.operation_id,
            uuid::Uuid::now_v7()
        ));
        match fs::rename(&path, &claimed) {
            Ok(()) => {}
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(()),
            Err(_) => return Err(NativeFailure::StorageUnavailable),
        }
        if matches!(Self::read_bytes(&claimed), Ok(Some(stored)) if stored == expected) {
            fs::remove_file(&claimed).map_err(|_| NativeFailure::StorageUnavailable)?;
            #[cfg(unix)]
            File::open(&self.dir)
                .and_then(|d| d.sync_all())
                .map_err(|_| NativeFailure::StorageUnavailable)?;
            return Ok(());
        }
        self.restore_claimed(&claimed, &path, &record.operation_id);
        Err(NativeFailure::InvalidEvidence)
    }

    /// Returns evidence that was claimed but did not match the verified bytes
    /// to its canonical name. The restore never replaces: when another writer
    /// already published that name, both records are evidence of a conflicting
    /// outcome. The claimed one is then renamed to a visible, non-canonical
    /// quarantine name, because dot-prefixed files are never listed and so
    /// would be neither replayed nor reported on later launches. A listing
    /// reports the quarantined file as unreadable evidence and keeps it.
    fn restore_claimed(&self, claimed: &Path, path: &Path, operation_id: &str) {
        match fs::hard_link(claimed, path) {
            Ok(()) => {
                let _ = fs::remove_file(claimed);
                return;
            }
            Err(e) => tracing::warn!(
                operation = "desktop_update_outcome_restore",
                state = "canonical-name-unavailable",
                id = operation_id,
                kind = ?e.kind()
            ),
        }
        let quarantine = self.dir.join(format!(
            "{operation_id}.{}.conflict.json",
            uuid::Uuid::now_v7()
        ));
        match fs::rename(claimed, &quarantine) {
            Ok(()) => tracing::warn!(
                operation = "desktop_update_outcome_restore",
                state = "claimed-evidence-quarantined",
                id = operation_id
            ),
            // The claimed file stays on disk under its hidden name.
            Err(e) => tracing::error!(
                operation = "desktop_update_outcome_restore",
                state = "claimed-evidence-stranded",
                id = operation_id,
                kind = ?e.kind()
            ),
        }
    }
}

#[cfg(all(test, unix))]
mod tests {
    use std::os::unix::fs::PermissionsExt;

    use super::*;

    /// Larger than any small constant a walk could silently rely on.
    const BULK: usize = 80;

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
    fn publication_never_replaces_a_record_created_after_the_read() {
        let (_temp, root) = scope();
        let store = OutcomeStore::new(&root);
        let first = record(Phase::Installed);
        store.persist(&first).unwrap();
        store.persist(&record(Phase::Failed)).unwrap();
        let path = root
            .join(STORE_DIRECTORY)
            .join(format!("{}.json", first.operation_id));
        let before = fs::read(&path).unwrap();
        // Skipping the preceding read models a writer that won the race.
        let mut conflicting = first.clone();
        conflicting.phase = Phase::Failed;
        assert!(matches!(
            store.publish(&conflicting),
            Err(NativeFailure::InvalidEvidence)
        ));
        assert_eq!(fs::read(&path).unwrap(), before);
        store.publish(&first).unwrap();
        assert_eq!(fs::read(&path).unwrap(), before);
        let leftovers = fs::read_dir(root.join(STORE_DIRECTORY))
            .unwrap()
            .filter(|e| {
                e.as_ref()
                    .unwrap()
                    .file_name()
                    .to_string_lossy()
                    .starts_with('.')
            })
            .count();
        assert_eq!(leftovers, 0, "temporary files are always removed");
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
    fn retire_keeps_semantically_equal_records_with_different_bytes() {
        let (_temp, root) = scope();
        let store = OutcomeStore::new(&root);
        let stored = record(Phase::Installed);
        store.persist(&stored).unwrap();
        let path = root
            .join(STORE_DIRECTORY)
            .join(format!("{}.json", stored.operation_id));
        let pretty = serde_json::to_vec_pretty(&stored).unwrap();
        fs::write(&path, &pretty).unwrap();
        assert!(store.load(&stored.operation_id).unwrap().is_some());
        assert!(matches!(
            store.retire(&stored),
            Err(NativeFailure::InvalidEvidence)
        ));
        assert_eq!(fs::read(&path).unwrap(), pretty);
        fs::write(&path, serde_json::to_vec(&stored).unwrap()).unwrap();
        store.retire(&stored).unwrap();
        assert!(!path.exists());
    }

    #[test]
    fn retire_never_unlinks_a_record_replaced_after_verification() {
        let (_temp, root) = scope();
        let store = OutcomeStore::new(&root);
        let stored = record(Phase::Installed);
        store.persist(&stored).unwrap();
        let directory = root.join(STORE_DIRECTORY);
        let path = directory.join(format!("{}.json", stored.operation_id));
        let mut replacement = stored.clone();
        replacement.phase = Phase::Failed;
        let replacement_bytes = serde_json::to_vec(&replacement).unwrap();
        let result = store.retire_with(&stored, || {
            let temporary = directory.join("replacement.tmp");
            fs::write(&temporary, &replacement_bytes).unwrap();
            fs::rename(&temporary, &path).unwrap();
        });
        assert!(matches!(result, Err(NativeFailure::InvalidEvidence)));
        assert_eq!(fs::read(&path).unwrap(), replacement_bytes);
        let hidden = fs::read_dir(&directory)
            .unwrap()
            .filter(|e| {
                e.as_ref()
                    .unwrap()
                    .file_name()
                    .to_string_lossy()
                    .starts_with('.')
            })
            .count();
        assert_eq!(hidden, 0, "the claimed file is restored, not left behind");
        // A second replacement is restored the same way.
        let result = store.retire_with(&replacement, || {
            let temporary = directory.join("replacement.tmp");
            fs::write(&temporary, serde_json::to_vec(&stored).unwrap()).unwrap();
            fs::rename(&temporary, &path).unwrap();
        });
        assert!(result.is_err());
        assert_eq!(
            fs::read(&path).unwrap(),
            serde_json::to_vec(&stored).unwrap()
        );
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
    fn persistence_fails_closed_when_the_parent_cannot_be_synced() {
        let (_temp, root) = scope();
        let parent = root.join("desktop-updates");
        // Write and search without read permission: the directory can gain
        // the store entry but cannot be opened to fsync it.
        fs::set_permissions(&parent, fs::Permissions::from_mode(0o300)).unwrap();
        let unreadable = File::open(&parent).is_err();
        let store = OutcomeStore::new(&root);
        let stored = record(Phase::Failed);
        let result = store.persist(&stored);
        fs::set_permissions(&parent, fs::Permissions::from_mode(0o700)).unwrap();
        if !unreadable {
            // Privileged runners bypass directory permissions.
            return;
        }
        assert!(matches!(result, Err(NativeFailure::StorageUnavailable)));
        assert!(
            !root
                .join(STORE_DIRECTORY)
                .join(format!("{}.json", stored.operation_id))
                .exists(),
            "an outcome is never reported retained before the parent is durable"
        );
        // Once the parent can be synced the same record persists.
        store.persist(&stored).unwrap();
        assert_eq!(store.scan().unwrap(), vec![StoredEntry::Record(stored)]);
    }

    #[test]
    fn linked_scope_is_refused_before_creating_anything_outside_the_root() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let external = root.join("external");
        fs::create_dir(&external).unwrap();
        let connector = root.join("connector");
        fs::create_dir(&connector).unwrap();
        std::os::unix::fs::symlink(&external, connector.join("desktop-updates")).unwrap();
        let store = OutcomeStore::new(&connector);
        assert!(matches!(
            store.persist(&record(Phase::Failed)),
            Err(NativeFailure::InvalidEvidence)
        ));
        assert!(matches!(store.scan(), Err(NativeFailure::InvalidEvidence)));
        assert_eq!(fs::read_dir(&external).unwrap().count(), 0);
    }

    #[test]
    fn linked_ancestor_of_the_scope_is_refused_before_creation() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let external = root.join("external");
        fs::create_dir_all(external.join("desktop-updates")).unwrap();
        let linked = root.join("linked-root");
        std::os::unix::fs::symlink(&external, &linked).unwrap();
        let store = OutcomeStore::new(&linked);
        assert!(matches!(
            store.persist(&record(Phase::Failed)),
            Err(NativeFailure::InvalidEvidence)
        ));
        assert_eq!(
            fs::read_dir(external.join("desktop-updates"))
                .unwrap()
                .count(),
            0
        );
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
    fn walk_visits_every_entry_exactly_once_behind_retained_prefixes() {
        let (_temp, root) = scope();
        let store = OutcomeStore::new(&root);
        let directory = root.join(STORE_DIRECTORY);
        // Unreadable names interleave with many readable records.
        let mut expected = Vec::new();
        for index in 0..BULK {
            let id = format!("00000000-0000-7000-8000-{index:012}");
            let stored = OutcomeRecord::outcome(&id, &id, 3, &id, None, Phase::Failed).unwrap();
            store.persist(&stored).unwrap();
            expected.push(stored);
            if index % 7 == 0 {
                fs::write(directory.join(format!("{id}-corrupt.json")), b"{").unwrap();
            }
        }
        // Dot-prefixed temporary and claimed files are never listed.
        fs::write(directory.join(".stale.tmp"), b"{").unwrap();
        let mut seen = Vec::new();
        let mut unreadable = 0;
        store
            .for_each(|entry| {
                match entry {
                    StoredEntry::Record(record) => seen.push(record),
                    StoredEntry::Unreadable(_) => unreadable += 1,
                }
                ControlFlow::Continue(())
            })
            .unwrap();
        seen.sort_by(|a, b| a.operation_id.cmp(&b.operation_id));
        assert_eq!(seen, expected, "every record, once");
        assert_eq!(unreadable, (0..BULK).step_by(7).count());
        assert_eq!(store.scan().unwrap().len(), BULK + unreadable);
    }

    #[test]
    fn walk_stops_between_entries_when_the_visitor_breaks() {
        let (_temp, root) = scope();
        let store = OutcomeStore::new(&root);
        store.persist(&record(Phase::Failed)).unwrap();
        let directory = root.join(STORE_DIRECTORY);
        for index in 0..BULK {
            fs::write(directory.join(format!("0-corrupt-{index:02}.json")), b"{").unwrap();
        }
        let mut visited = 0;
        store
            .for_each(|_| {
                visited += 1;
                ControlFlow::Break(())
            })
            .unwrap();
        assert_eq!(visited, 1, "a stop request ends the walk mid-directory");
    }

    #[test]
    fn restore_never_leaves_claimed_evidence_hidden_when_the_name_is_taken() {
        let (_temp, root) = scope();
        let store = OutcomeStore::new(&root);
        let claimed_record = record(Phase::Installed);
        store.persist(&claimed_record).unwrap();
        let directory = root.join(STORE_DIRECTORY);
        let path = directory.join(format!("{}.json", claimed_record.operation_id));
        // Model a claim whose canonical name another writer then published.
        let claimed = directory.join(format!(".{}.claim.retire", claimed_record.operation_id));
        fs::rename(&path, &claimed).unwrap();
        let mut rival = claimed_record.clone();
        rival.phase = Phase::Failed;
        store.persist(&rival).unwrap();
        let rival_bytes = fs::read(&path).unwrap();
        store.restore_claimed(&claimed, &path, &claimed_record.operation_id);
        assert_eq!(
            fs::read(&path).unwrap(),
            rival_bytes,
            "the published record is never replaced"
        );
        assert!(!claimed.exists(), "the claimed file is no longer hidden");
        let hidden = fs::read_dir(&directory)
            .unwrap()
            .filter(|e| {
                e.as_ref()
                    .unwrap()
                    .file_name()
                    .to_string_lossy()
                    .starts_with('.')
            })
            .count();
        assert_eq!(hidden, 0);
        // The quarantined evidence is reported on every later listing and kept.
        let entries = store.scan().unwrap();
        assert_eq!(entries.len(), 2);
        assert!(entries.contains(&StoredEntry::Record(rival)));
        assert!(entries.contains(&StoredEntry::Unreadable(None)));
        let quarantined = fs::read_dir(&directory)
            .unwrap()
            .map(|e| e.unwrap().path())
            .find(|p| p.to_string_lossy().ends_with(".conflict.json"))
            .unwrap();
        assert_eq!(
            fs::read(quarantined).unwrap(),
            serde_json::to_vec(&claimed_record).unwrap()
        );
    }

    #[test]
    fn load_reads_one_operation_without_enumerating_the_store() {
        let (_temp, root) = scope();
        let store = OutcomeStore::new(&root);
        let missing = uuid::Uuid::now_v7().to_string();
        assert_eq!(store.load(&missing).unwrap(), None);
        let stored = record(Phase::Uncertain);
        store.persist(&stored).unwrap();
        let directory = root.join(STORE_DIRECTORY);
        for index in 0..(BULK) {
            fs::write(directory.join(format!("0-corrupt-{index:02}.json")), b"{").unwrap();
        }
        assert_eq!(
            store.load(&stored.operation_id).unwrap(),
            Some(StoredEntry::Record(stored.clone()))
        );
        fs::write(directory.join(format!("{missing}.json")), b"{").unwrap();
        assert_eq!(
            store.load(&missing).unwrap(),
            Some(StoredEntry::Unreadable(Some(missing.clone())))
        );
        assert!(matches!(
            store.load("../escape"),
            Err(NativeFailure::InvalidEvidence)
        ));
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
