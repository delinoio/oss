// SPDX-License-Identifier: Apache-2.0
use std::{
    collections::BTreeSet,
    fs::{self, File, OpenOptions},
    io::{Read, Write},
    path::{Path, PathBuf},
    sync::Mutex,
};

use serde::{Deserialize, Serialize};

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq, PartialOrd, Ord)]
#[serde(rename_all = "kebab-case")]
pub enum CreationKind {
    Session,
    GeneralChat,
}
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq, PartialOrd, Ord)]
#[serde(deny_unknown_fields)]
pub struct Scope {
    pub server_id: String,
    pub device_id: String,
}
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Pair {
    pub agent_id: String,
    pub machine_id: String,
}
#[derive(Clone, Copy, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum Problem {
    Unavailable,
    ReadFailed,
    InvalidDocument,
    UnsupportedVersion,
    WriteFailed,
    OutcomeUnknown,
    Changed,
    Capacity,
}
#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct Snapshot {
    pub revision: u32,
    pub scope: Scope,
    pub pair: Option<Pair>,
    pub problem: Option<Problem>,
}
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
struct Record {
    scope: Scope,
    kind: CreationKind,
    pair: Pair,
}
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Document {
    version: u32,
    records: Vec<Record>,
}
struct State {
    revision: u32,
    selected: Option<(Scope, CreationKind)>,
    records: Vec<Record>,
    problem: Option<Problem>,
}
pub struct Store {
    path: Option<PathBuf>,
    state: Mutex<State>,
}
fn valid_id(id: &str) -> bool {
    uuid::Uuid::parse_str(id)
        .is_ok_and(|value| value.hyphenated().to_string() == id && !value.is_nil())
}
impl Scope {
    pub fn valid(&self) -> bool {
        valid_id(&self.server_id) && valid_id(&self.device_id)
    }
}
impl Pair {
    fn valid(&self) -> bool {
        valid_id(&self.agent_id) && valid_id(&self.machine_id)
    }
}
impl State {
    fn advance(&mut self) {
        if let Some(next) = self.revision.checked_add(1) {
            self.revision = next;
        } else {
            self.problem = Some(Problem::Unavailable);
        }
    }

    fn snapshot(&self, scope: &Scope, kind: CreationKind) -> Snapshot {
        Snapshot {
            revision: self.revision,
            scope: scope.clone(),
            pair: self
                .records
                .iter()
                .find(|r| r.scope == *scope && r.kind == kind)
                .map(|r| r.pair.clone()),
            problem: self.problem,
        }
    }
}
impl Store {
    pub fn new(directory: Option<PathBuf>) -> Self {
        Self {
            path: directory.map(|p| p.join("session-creation-preferences.json")),
            state: Mutex::new(State {
                revision: 0,
                selected: None,
                records: vec![],
                problem: Some(Problem::Unavailable),
            }),
        }
    }

    pub fn read(&self, scope: Scope, kind: CreationKind) -> Snapshot {
        let mut state = self.state.lock().unwrap_or_else(|e| e.into_inner());
        let observed = self
            .path
            .as_deref()
            .ok_or(Problem::Unavailable)
            .and_then(inspect);
        let previous = (state.records.clone(), state.problem, state.selected.clone());
        match observed {
            Ok(records) if scope.valid() => {
                state.records = records;
                state.problem = None;
            }
            Ok(_) => state.problem = Some(Problem::InvalidDocument),
            Err(problem) => {
                state.problem = Some(problem);
                tracing::warn!(operation = "creation_preferences_read", ?problem);
            }
        }
        state.selected = Some((scope.clone(), kind));
        if state.revision == 0
            || previous != (state.records.clone(), state.problem, state.selected.clone())
        {
            state.advance();
        }
        state.snapshot(&scope, kind)
    }

    pub fn update(
        &self,
        scope: Scope,
        kind: CreationKind,
        pair: Pair,
        expected_revision: u32,
    ) -> Snapshot {
        self.update_with(scope, kind, pair, expected_revision, persist)
    }

    fn update_with(
        &self,
        scope: Scope,
        kind: CreationKind,
        pair: Pair,
        expected_revision: u32,
        write: impl FnOnce(&Path, &[Record]) -> Result<(), Problem>,
    ) -> Snapshot {
        let mut state = self.state.lock().unwrap_or_else(|e| e.into_inner());
        if state.revision != expected_revision
            || state.selected.as_ref() != Some(&(scope.clone(), kind))
        {
            let mut result = state.snapshot(&scope, kind);
            result.problem = Some(Problem::Changed);
            return result;
        }
        if state.problem.is_some() || state.revision == u32::MAX {
            return state.snapshot(&scope, kind);
        }
        let result = (|| {
            if !scope.valid() || !pair.valid() {
                return Err(Problem::InvalidDocument);
            }
            let path = self.path.as_deref().ok_or(Problem::Unavailable)?;
            if inspect(path)? != state.records {
                return Err(Problem::Changed);
            }
            let mut records = state.records.clone();
            if let Some(record) = records
                .iter_mut()
                .find(|r| r.scope == scope && r.kind == kind)
            {
                record.pair = pair;
            } else {
                if records.len() >= 128 {
                    return Err(Problem::Capacity);
                }
                records.push(Record {
                    scope: scope.clone(),
                    kind,
                    pair,
                });
            }
            write(path, &records)?;
            Ok(records)
        })();
        match result {
            Ok(records) => {
                state.records = records;
                tracing::info!(
                    operation = "creation_preferences_update",
                    outcome = "committed"
                );
            }
            Err(problem) => {
                state.problem = Some(problem);
                tracing::warn!(operation = "creation_preferences_update", ?problem);
            }
        }
        state.advance();
        state.snapshot(&scope, kind)
    }
}
fn inspect(path: &Path) -> Result<Vec<Record>, Problem> {
    let metadata = match fs::symlink_metadata(path) {
        Ok(m) => m,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(vec![]),
        Err(_) => return Err(Problem::ReadFailed),
    };
    if !metadata.is_file() || metadata.len() > 64 << 10 {
        return Err(Problem::InvalidDocument);
    }
    let file = File::open(path).map_err(|_| Problem::ReadFailed)?;
    let mut bytes = vec![];
    file.take((64 << 10) + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| Problem::ReadFailed)?;
    if bytes.len() > 64 << 10 {
        return Err(Problem::InvalidDocument);
    }
    let value: Document = serde_json::from_slice(&bytes).map_err(|_| Problem::InvalidDocument)?;
    if value.version != 1 {
        return Err(Problem::UnsupportedVersion);
    }
    let mut keys = BTreeSet::new();
    if value.records.len() > 128
        || value
            .records
            .iter()
            .any(|r| !r.scope.valid() || !r.pair.valid() || !keys.insert((r.scope.clone(), r.kind)))
    {
        return Err(Problem::InvalidDocument);
    }
    Ok(value.records)
}
fn persist(path: &Path, records: &[Record]) -> Result<(), Problem> {
    let parent = path.parent().ok_or(Problem::WriteFailed)?;
    fs::create_dir_all(parent).map_err(|_| Problem::WriteFailed)?;
    let scratch = parent.join(format!(".session-creation-{}.tmp", uuid::Uuid::now_v7()));
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut file = options.open(&scratch).map_err(|_| Problem::WriteFailed)?;
    let result = (|| {
        let bytes = serde_json::to_vec(&Document {
            version: 1,
            records: records.to_vec(),
        })
        .map_err(|_| Problem::WriteFailed)?;
        if bytes.len() > 64 << 10 {
            return Err(Problem::Capacity);
        }
        file.write_all(&bytes)
            .and_then(|_| file.sync_all())
            .map_err(|_| Problem::WriteFailed)?;
        drop(file);
        crate::appearance::replace(&scratch, path).map_err(|_| Problem::WriteFailed)?;
        #[cfg(unix)]
        File::open(parent)
            .and_then(|d| d.sync_all())
            .map_err(|_| Problem::OutcomeUnknown)?;
        Ok(())
    })();
    let _ = fs::remove_file(scratch);
    result
}

#[cfg(test)]
mod tests {
    use super::*;
    fn scope() -> Scope {
        Scope {
            server_id: uuid::Uuid::now_v7().to_string(),
            device_id: uuid::Uuid::now_v7().to_string(),
        }
    }
    fn pair() -> Pair {
        Pair {
            agent_id: uuid::Uuid::now_v7().to_string(),
            machine_id: uuid::Uuid::now_v7().to_string(),
        }
    }
    #[test]
    fn restart_kind_scope_and_revision_isolation() {
        let temp = tempfile::tempdir().unwrap();
        let store = Store::new(Some(temp.path().into()));
        let scope = scope();
        let session = pair();
        let chat = pair();
        let read = store.read(scope.clone(), CreationKind::Session);
        assert_eq!(read.pair, None);
        assert_eq!(
            store
                .update(
                    scope.clone(),
                    CreationKind::Session,
                    session.clone(),
                    read.revision
                )
                .pair,
            Some(session.clone())
        );
        let read = store.read(scope.clone(), CreationKind::GeneralChat);
        assert_eq!(read.pair, None);
        let saved = store.update(
            scope.clone(),
            CreationKind::GeneralChat,
            chat.clone(),
            read.revision,
        );
        assert!(saved.problem.is_none());
        assert_eq!(
            store
                .update(scope.clone(), CreationKind::Session, pair(), read.revision)
                .problem,
            Some(Problem::Changed)
        );
        let restarted = Store::new(Some(temp.path().into()));
        assert_eq!(
            restarted.read(scope.clone(), CreationKind::Session).pair,
            Some(session)
        );
        assert_eq!(
            restarted
                .read(scope.clone(), CreationKind::GeneralChat)
                .pair,
            Some(chat)
        );
        let foreign = Scope {
            server_id: scope.server_id,
            device_id: uuid::Uuid::now_v7().to_string(),
        };
        assert_eq!(restarted.read(foreign, CreationKind::Session).pair, None);
    }
    #[test]
    fn invalid_documents_are_preserved() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("session-creation-preferences.json");
        let selected = scope();
        let record = Record {
            scope: selected.clone(),
            kind: CreationKind::Session,
            pair: pair(),
        };
        let duplicate = serde_json::to_string(&Document {
            version: 1,
            records: vec![record.clone(), record],
        })
        .unwrap();
        for raw in [
            "{\"version\":1,\"version\":1,\"records\":[]}".to_owned(),
            "{\"version\":2,\"records\":[]}".into(),
            "{\"version\":1,\"records\":[],\"extra\":true}".into(),
            duplicate,
            "x".repeat((64 << 10) + 1),
        ] {
            fs::write(&path, &raw).unwrap();
            let store = Store::new(Some(temp.path().into()));
            let read = store.read(selected.clone(), CreationKind::Session);
            assert!(read.problem.is_some());
            let result = store.update(
                selected.clone(),
                CreationKind::Session,
                pair(),
                read.revision,
            );
            assert!(result.problem.is_some());
            assert_eq!(fs::read_to_string(&path).unwrap(), raw);
        }
    }
    #[test]
    fn capacity_failed_and_uncertain_writes_preserve_history_until_read() {
        let temp = tempfile::tempdir().unwrap();
        let store = Store::new(Some(temp.path().into()));
        let selected = scope();
        let original = pair();
        let read = store.read(selected.clone(), CreationKind::Session);
        store.update(
            selected.clone(),
            CreationKind::Session,
            original.clone(),
            read.revision,
        );
        let read = store.read(selected.clone(), CreationKind::Session);
        let failed = store.update_with(
            selected.clone(),
            CreationKind::Session,
            pair(),
            read.revision,
            |_, _| Err(Problem::WriteFailed),
        );
        assert_eq!(failed.pair, Some(original.clone()));
        assert_eq!(failed.problem, Some(Problem::WriteFailed));
        let read = store.read(selected.clone(), CreationKind::Session);
        let next = pair();
        let uncertain = store.update_with(
            selected.clone(),
            CreationKind::Session,
            next.clone(),
            read.revision,
            |path, rows| {
                persist(path, rows)?;
                Err(Problem::OutcomeUnknown)
            },
        );
        assert_eq!(uncertain.pair, Some(original));
        assert_eq!(uncertain.problem, Some(Problem::OutcomeUnknown));
        assert_eq!(store.read(selected, CreationKind::Session).pair, Some(next));
        let records: Vec<Record> = (0..128)
            .map(|_| Record {
                scope: scope(),
                kind: CreationKind::Session,
                pair: pair(),
            })
            .collect();
        persist(
            &temp.path().join("session-creation-preferences.json"),
            &records,
        )
        .unwrap();
        let selected = scope();
        let read = store.read(selected.clone(), CreationKind::Session);
        assert_eq!(
            store
                .update(selected, CreationKind::Session, pair(), read.revision)
                .problem,
            Some(Problem::Capacity)
        );
    }
    #[cfg(unix)]
    #[test]
    fn linked_file_is_not_read_or_replaced() {
        let temp = tempfile::tempdir().unwrap();
        let target = temp.path().join("other");
        fs::write(&target, "{}").unwrap();
        std::os::unix::fs::symlink(
            &target,
            temp.path().join("session-creation-preferences.json"),
        )
        .unwrap();
        let store = Store::new(Some(temp.path().into()));
        assert_eq!(
            store.read(scope(), CreationKind::Session).problem,
            Some(Problem::InvalidDocument)
        );
        assert_eq!(fs::read_to_string(target).unwrap(), "{}");
    }
}
