//! Bounded metadata maps. Large collections spill to a private, disposable
//! SQLite index; serialization and analyses traverse pages instead of
//! rebuilding a map.
use std::{
    collections::{BTreeMap, VecDeque},
    marker::PhantomData,
    sync::{
        Arc, Mutex,
        atomic::{AtomicUsize, Ordering},
    },
};

use rusqlite::{Connection, params};
use serde::{
    Deserialize, Serialize,
    de::{DeserializeOwned, MapAccess, Visitor},
    ser::SerializeMap,
};
use tempfile::NamedTempFile;

use crate::error::{Error, Result};

pub const MAX_RECORD_BYTES: usize = 64 * 1024;
pub const MAX_RECORDS: usize = 1_000_000;
pub const DEFAULT_MEMORY_BYTES: usize = 256 * 1024 * 1024;

static MEMORY_USED: AtomicUsize = AtomicUsize::new(0);
static MEMORY_LIMIT: AtomicUsize = AtomicUsize::new(DEFAULT_MEMORY_BYTES);

pub fn set_memory_limit(bytes: usize) {
    MEMORY_LIMIT.store(bytes, Ordering::Relaxed);
}
fn reserve(previous: usize, next: usize) -> bool {
    MEMORY_USED
        .try_update(Ordering::Relaxed, Ordering::Relaxed, |used| {
            used.checked_sub(previous)?
                .checked_add(next)
                .filter(|total| *total <= MEMORY_LIMIT.load(Ordering::Relaxed))
        })
        .is_ok()
}
struct Disk {
    connection: Connection,
    _file: NamedTempFile,
}
enum Storage {
    Memory(BTreeMap<String, String>),
    Disk(Disk),
}
struct State {
    storage: Storage,
    memory_limit: usize,
    reserved: usize,
    bytes: usize,
    count: usize,
}
#[derive(Clone)]
pub struct Entries<T> {
    state: Arc<Mutex<State>>,
    marker: PhantomData<T>,
}
impl<T> std::fmt::Debug for Entries<T> {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("Entries")
            .field("count", &self.len())
            .finish()
    }
}
impl<T> Default for Entries<T> {
    fn default() -> Self {
        Self::new(DEFAULT_MEMORY_BYTES / 8)
    }
}
impl<T> Entries<T> {
    pub fn new(memory_limit: usize) -> Self {
        Self {
            state: Arc::new(Mutex::new(State {
                storage: Storage::Memory(BTreeMap::new()),
                memory_limit,
                reserved: 0,
                bytes: 0,
                count: 0,
            })),
            marker: PhantomData,
        }
    }

    pub fn len(&self) -> usize {
        self.state.lock().map(|s| s.count).unwrap_or(MAX_RECORDS)
    }

    pub fn is_empty(&self) -> bool {
        self.len() == 0
    }

    pub fn spilled(&self) -> bool {
        self.state
            .lock()
            .is_ok_and(|s| matches!(s.storage, Storage::Disk(_)))
    }

    pub fn iter(&self) -> EntryIter<T> {
        EntryIter {
            state: Arc::clone(&self.state),
            page: VecDeque::new(),
            after: None,
            ended: false,
            marker: PhantomData,
        }
    }
}
impl<T: Serialize + DeserializeOwned> Entries<T> {
    pub fn insert(&mut self, key: String, value: T) -> Result<bool> {
        let encoded = serde_json::to_string(&value).map_err(|_| Error::storage())?;
        if key.is_empty() || key.len() > MAX_RECORD_BYTES || encoded.len() > MAX_RECORD_BYTES {
            return Err(Error::input("metadata record is oversized"));
        }
        let mut state = self.state.lock().map_err(|_| Error::storage())?;
        let old_len = match &state.storage {
            Storage::Memory(map) => map.get(&key).map(String::len),
            Storage::Disk(disk) => {
                use rusqlite::OptionalExtension;
                disk.connection
                    .query_row(
                        "SELECT length(CAST(value AS BLOB)) FROM entries WHERE key=?1",
                        [&key],
                        |r| r.get::<_, i64>(0).map(|n| n as usize),
                    )
                    .optional()
                    .map_err(|_| Error::storage())?
            }
        };
        if old_len.is_none() && state.count >= MAX_RECORDS {
            return Err(Error::input("too many metadata records"));
        }
        let next_bytes = state.bytes.saturating_sub(old_len.unwrap_or(0))
            + encoded.len()
            + if old_len.is_none() {
                key.len() + 128
            } else {
                0
            };
        let memory_storage = matches!(state.storage, Storage::Memory(_));
        let fits_memory = memory_storage
            && next_bytes <= state.memory_limit
            && reserve(state.reserved, next_bytes);
        if fits_memory {
            state.reserved = next_bytes;
        }
        if memory_storage && !fits_memory {
            let file = NamedTempFile::new().map_err(|_| Error::storage())?;
            let mut connection = Connection::open(file.path()).map_err(|_| Error::storage())?;
            connection
                .execute_batch(
                    "PRAGMA journal_mode=OFF; PRAGMA synchronous=OFF; PRAGMA temp_store=FILE; \
                     PRAGMA cache_size=-512; CREATE TABLE entries(key TEXT PRIMARY KEY COLLATE \
                     BINARY, value TEXT NOT NULL) WITHOUT ROWID;",
                )
                .map_err(|_| Error::storage())?;
            let transaction = connection.transaction().map_err(|_| Error::storage())?;
            if let Storage::Memory(map) = &state.storage {
                let mut statement = transaction
                    .prepare("INSERT INTO entries VALUES (?1,?2)")
                    .map_err(|_| Error::storage())?;
                for (k, v) in map {
                    statement
                        .execute(params![k, v])
                        .map_err(|_| Error::storage())?;
                }
            }
            transaction.commit().map_err(|_| Error::storage())?;
            MEMORY_USED.fetch_sub(state.reserved, Ordering::Relaxed);
            state.reserved = 0;
            state.storage = Storage::Disk(Disk {
                connection,
                _file: file,
            });
        }
        match &mut state.storage {
            Storage::Memory(map) => {
                map.insert(key, encoded);
            }
            Storage::Disk(disk) => {
                disk.connection
                    .execute(
                        "INSERT OR REPLACE INTO entries VALUES (?1,?2)",
                        params![key, encoded],
                    )
                    .map_err(|_| Error::storage())?;
            }
        }
        state.bytes = next_bytes;
        state.count += usize::from(old_len.is_none());
        Ok(old_len.is_none())
    }

    pub fn get(&self, key: &str) -> Result<Option<T>> {
        use rusqlite::OptionalExtension;
        let state = self.state.lock().map_err(|_| Error::storage())?;
        let value = match &state.storage {
            Storage::Memory(map) => map.get(key).cloned(),
            Storage::Disk(disk) => disk
                .connection
                .query_row("SELECT value FROM entries WHERE key=?1", [key], |r| {
                    r.get::<_, String>(0)
                })
                .optional()
                .map_err(|_| Error::storage())?,
        };
        value
            .map(|v| serde_json::from_str(&v).map_err(|_| Error::storage()))
            .transpose()
    }
}
pub struct EntryIter<T> {
    state: Arc<Mutex<State>>,
    page: VecDeque<(String, String)>,
    after: Option<String>,
    ended: bool,
    marker: PhantomData<T>,
}
impl<T: DeserializeOwned> Iterator for EntryIter<T> {
    type Item = Result<(String, T)>;

    fn next(&mut self) -> Option<Self::Item> {
        if self.ended {
            return None;
        }
        if self.page.is_empty() {
            let loaded = (|| -> Result<()> {
                let state = self.state.lock().map_err(|_| Error::storage())?;
                match &state.storage {
                    Storage::Memory(map) => {
                        use std::ops::Bound::{Excluded, Unbounded};
                        let lower = self.after.as_ref().map_or(Unbounded, Excluded);
                        self.page.extend(
                            map.range::<String, _>((lower, Unbounded))
                                .take(64)
                                .map(|(k, v)| (k.clone(), v.clone())),
                        );
                    }
                    Storage::Disk(disk) => {
                        let mut statement = disk
                            .connection
                            .prepare(
                                "SELECT key,value FROM entries WHERE key>?1 ORDER BY key LIMIT 64",
                            )
                            .map_err(|_| Error::storage())?;
                        let rows = statement
                            .query_map([self.after.as_deref().unwrap_or("")], |r| {
                                Ok((r.get(0)?, r.get(1)?))
                            })
                            .map_err(|_| Error::storage())?;
                        for row in rows {
                            self.page.push_back(row.map_err(|_| Error::storage())?);
                        }
                    }
                }
                Ok(())
            })();
            if let Err(error) = loaded {
                self.ended = true;
                return Some(Err(error));
            }
        }
        match self.page.pop_front() {
            Some((key, encoded)) => {
                self.after = Some(key.clone());
                Some(
                    serde_json::from_str(&encoded)
                        .map(|value| (key, value))
                        .map_err(|_| Error::storage()),
                )
            }
            None => {
                self.ended = true;
                None
            }
        }
    }
}
impl<T: Serialize + DeserializeOwned> Serialize for Entries<T> {
    fn serialize<S: serde::Serializer>(
        &self,
        serializer: S,
    ) -> std::result::Result<S::Ok, S::Error> {
        let mut map = serializer.serialize_map(Some(self.len()))?;
        for item in self.iter() {
            let (key, value) = item.map_err(serde::ser::Error::custom)?;
            map.serialize_entry(&key, &value)?;
        }
        map.end()
    }
}
impl<'de, T: Serialize + DeserializeOwned> Deserialize<'de> for Entries<T> {
    fn deserialize<D: serde::Deserializer<'de>>(
        deserializer: D,
    ) -> std::result::Result<Self, D::Error> {
        struct MapVisitor<T>(PhantomData<T>);
        impl<'de, T: Serialize + DeserializeOwned> Visitor<'de> for MapVisitor<T> {
            type Value = Entries<T>;

            fn expecting(&self, f: &mut std::fmt::Formatter) -> std::fmt::Result {
                f.write_str("a bounded metadata object with unique keys")
            }

            fn visit_map<M: MapAccess<'de>>(
                self,
                mut map: M,
            ) -> std::result::Result<Self::Value, M::Error> {
                let mut entries = Entries::new(0);
                while let Some((key, value)) = map.next_entry::<String, T>()? {
                    if !entries
                        .insert(key, value)
                        .map_err(serde::de::Error::custom)?
                    {
                        return Err(serde::de::Error::custom("duplicate metadata key"));
                    }
                }
                Ok(entries)
            }
        }
        deserializer.deserialize_map(MapVisitor(PhantomData))
    }
}

impl<T: schemars::JsonSchema> schemars::JsonSchema for Entries<T> {
    fn schema_name() -> std::borrow::Cow<'static, str> {
        format!("Entries_{}", T::schema_name()).into()
    }

    fn json_schema(generator: &mut schemars::SchemaGenerator) -> schemars::Schema {
        <std::collections::BTreeMap<String, T>>::json_schema(generator)
    }
}

impl Drop for State {
    fn drop(&mut self) {
        MEMORY_USED.fetch_sub(self.reserved, Ordering::Relaxed);
        if let Storage::Disk(Disk { connection, _file }) =
            std::mem::replace(&mut self.storage, Storage::Memory(BTreeMap::new()))
        {
            drop(connection);
            if _file.close().is_err() {
                crate::temporary::record_cleanup_failure();
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn retained_maps_share_one_memory_threshold() {
        set_memory_limit(256);
        let mut first = Entries::new(1024 * 1024);
        let mut second = Entries::new(1024 * 1024);
        first.insert("first".into(), "a".repeat(150)).unwrap();
        second.insert("second".into(), "b".repeat(150)).unwrap();
        assert!(first.spilled() && second.spilled());
        set_memory_limit(DEFAULT_MEMORY_BYTES);
    }
    #[cfg(unix)]
    #[test]
    fn index_cleanup_failure_is_observable() {
        let mut values = Entries::new(0);
        values.insert("path".into(), true).unwrap();
        let path = {
            let state = values.state.lock().unwrap();
            match &state.storage {
                Storage::Disk(disk) => disk._file.path().to_owned(),
                _ => panic!("expected a disk index"),
            }
        };
        std::fs::remove_file(&path).unwrap();
        std::fs::create_dir(&path).unwrap();
        drop(values);
        assert!(crate::temporary::cleanup_failed());
        std::fs::remove_dir(path).unwrap();
    }
}
