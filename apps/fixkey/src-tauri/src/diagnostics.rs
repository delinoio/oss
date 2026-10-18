//! Bounded local diagnostic storage. Only the `FixKey` tracing targets are
//! subscribed; correction text, candidates and settings are never logged.

use std::{
    fs::{self, OpenOptions},
    io::{self, Write},
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use tracing_subscriber::{layer::SubscriberExt, util::SubscriberInitExt};

/// Total maximum retained bytes.
pub const MAX_BYTES: u64 = 10 * 1024 * 1024;
/// Maximum age for a diagnostic file.
pub const MAX_AGE: Duration = Duration::from_secs(7 * 24 * 60 * 60);
const FILE_BYTES: u64 = 1024 * 1024;
const MAX_RECORD_BYTES: usize = 64 * 1024;

/// Shared log writer for the current process.
#[derive(Clone)]
pub struct Logs(Arc<Mutex<LogStore>>);

struct LogStore {
    directory: PathBuf,
    sequence: u64,
    active: Option<PathBuf>,
    write_failed: bool,
}

impl Logs {
    /// Creates the log folder and removes expired or oversized history.
    ///
    /// # Errors
    ///
    /// Returns an I/O error without logging its path or contents.
    pub fn open(directory: PathBuf) -> io::Result<Self> {
        fs::create_dir_all(&directory)?;
        let logs = Self(Arc::new(Mutex::new(LogStore {
            directory,
            sequence: 0,
            active: None,
            write_failed: false,
        })));
        logs.prune(SystemTime::now())?;
        Ok(logs)
    }

    /// Applies the retention rules, including during idle periods.
    ///
    /// # Errors
    ///
    /// Returns a filesystem or lock failure.
    pub fn prune(&self, now: SystemTime) -> io::Result<()> {
        let mut store = self
            .0
            .lock()
            .map_err(|_| io::Error::other("log-lock-failed"))?;
        store.prune(now, 0)
    }

    /// Whether diagnostic publication is currently healthy. The shell can
    /// expose a stable logging failure without including raw event contents.
    #[must_use]
    pub fn is_healthy(&self) -> bool {
        self.0.lock().is_ok_and(|store| !store.write_failed)
    }

    /// Deletes owned logs. User files in the same folder remain untouched.
    ///
    /// # Errors
    ///
    /// Returns a filesystem or lock failure.
    pub fn delete(&self) -> io::Result<()> {
        let mut store = self
            .0
            .lock()
            .map_err(|_| io::Error::other("log-lock-failed"))?;
        for (path, _, _) in inventory(&store.directory)? {
            fs::remove_file(path)?;
        }
        store.active = None;
        store.write_failed = false;
        drop(store);
        Ok(())
    }

    /// Installs redacted structured tracing and colored operator output.
    /// `NO_COLOR` opts out of console color; persisted logs never use ANSI.
    ///
    /// # Errors
    ///
    /// Returns an error if another global subscriber was already installed.
    pub fn install(&self) -> Result<(), tracing_subscriber::util::TryInitError> {
        let filter = tracing_subscriber::EnvFilter::new("off,fixkey=info,fixkey_core=info");
        tracing_subscriber::registry()
            .with(filter)
            .with(
                tracing_subscriber::fmt::layer()
                    .json()
                    .with_ansi(false)
                    .with_writer(self.clone()),
            )
            .with(
                tracing_subscriber::fmt::layer()
                    .with_writer(io::stderr)
                    .with_ansi(std::env::var_os("NO_COLOR").is_none()),
            )
            .try_init()
    }

    fn append(&self, record: &[u8], now: SystemTime) -> io::Result<()> {
        if record.len() > MAX_RECORD_BYTES {
            return Err(io::Error::other("log-record-too-large"));
        }
        let event: serde_json::Value =
            serde_json::from_slice(record).map_err(|_| io::Error::other("log-record-invalid"))?;
        let envelope = serde_json::json!({
            "version": env!("CARGO_PKG_VERSION"),
            "platform": platform(),
            "event": event,
        });
        let mut bytes = serde_json::to_vec(&envelope)?;
        bytes.push(b'\n');
        let size =
            u64::try_from(bytes.len()).map_err(|_| io::Error::other("log-record-too-large"))?;
        let mut store = self
            .0
            .lock()
            .map_err(|_| io::Error::other("log-lock-failed"))?;
        store.prune(now, size)?;
        let rotate = store
            .active
            .as_ref()
            .and_then(|path| fs::metadata(path).ok())
            .is_none_or(|metadata| metadata.len() + size > FILE_BYTES)
            || store
                .active
                .as_ref()
                .and_then(|path| opened_at(path))
                .is_some_and(|opened| {
                    now.duration_since(opened)
                        .is_ok_and(|age| age >= Duration::from_secs(24 * 60 * 60))
                });
        if rotate {
            let timestamp = now
                .duration_since(UNIX_EPOCH)
                .map_err(|_| io::Error::other("log-clock-invalid"))?
                .as_nanos();
            let path = store
                .directory
                .join(format!("fixkey-{timestamp}-{}.jsonl", store.sequence));
            store.sequence += 1;
            OpenOptions::new()
                .write(true)
                .create_new(true)
                .open(&path)?;
            store.active = Some(path);
        }
        let path = store
            .active
            .as_ref()
            .ok_or_else(|| io::Error::other("log-file-unavailable"))?;
        let result = OpenOptions::new()
            .append(true)
            .open(path)?
            .write_all(&bytes);
        drop(store);
        result
    }
}

impl LogStore {
    fn prune(&mut self, now: SystemTime, reserve: u64) -> io::Result<()> {
        let mut files = inventory(&self.directory)?;
        files.sort_by_key(|(_, modified, _)| *modified);
        let mut bytes = files.iter().map(|(_, _, size)| *size).sum::<u64>();
        for (path, modified, size) in files {
            if now.duration_since(modified).is_ok_and(|age| age >= MAX_AGE)
                || bytes.saturating_add(reserve) > MAX_BYTES
            {
                fs::remove_file(&path)?;
                bytes -= size;
                if self.active.as_ref() == Some(&path) {
                    self.active = None;
                }
            }
        }
        Ok(())
    }
}

fn inventory(directory: &Path) -> io::Result<Vec<(PathBuf, SystemTime, u64)>> {
    fs::read_dir(directory)?
        .filter_map(|entry| {
            let entry = match entry {
                Ok(entry) => entry,
                Err(error) => return Some(Err(error)),
            };
            let opened = opened_at(&entry.path())?;
            let metadata = match entry.metadata() {
                Ok(metadata) => metadata,
                Err(error) => return Some(Err(error)),
            };
            if !metadata.is_file() || entry.file_type().is_ok_and(|kind| kind.is_symlink()) {
                return None;
            }
            // Appending refreshes mtime. Age the whole file from its first
            // record instead so sparse logs cannot retain old events forever.
            Some(Ok((entry.path(), opened, metadata.len())))
        })
        .collect()
}

fn opened_at(path: &Path) -> Option<SystemTime> {
    let name = path.file_name()?.to_str()?;
    let (timestamp, sequence) = name
        .strip_prefix("fixkey-")?
        .strip_suffix(".jsonl")?
        .split_once('-')?;
    if timestamp.is_empty()
        || sequence.is_empty()
        || !timestamp.bytes().all(|byte| byte.is_ascii_digit())
        || !sequence.bytes().all(|byte| byte.is_ascii_digit())
    {
        return None;
    }
    let _: u64 = sequence.parse().ok()?;
    let timestamp: u128 = timestamp.parse().ok()?;
    let seconds = u64::try_from(timestamp / 1_000_000_000).ok()?;
    let nanos = u32::try_from(timestamp % 1_000_000_000).ok()?;
    UNIX_EPOCH.checked_add(Duration::new(seconds, nanos))
}

/// Stable platform identifier used by all diagnostic events.
#[must_use]
pub const fn platform() -> &'static str {
    if cfg!(all(target_os = "macos", target_arch = "aarch64")) {
        "macos-arm64"
    } else if cfg!(all(target_os = "windows", target_arch = "x86_64")) {
        "windows-x64"
    } else if cfg!(all(target_os = "linux", target_arch = "x86_64")) {
        "linux-x11-x64"
    } else {
        "unsupported"
    }
}

/// Buffers one formatter event until it can be published as a complete record.
pub struct RecordWriter {
    logs: Logs,
    bytes: Vec<u8>,
}

impl<'a> tracing_subscriber::fmt::MakeWriter<'a> for Logs {
    type Writer = RecordWriter;

    fn make_writer(&'a self) -> Self::Writer {
        RecordWriter {
            logs: self.clone(),
            bytes: Vec::new(),
        }
    }
}

impl Write for RecordWriter {
    fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
        if self.bytes.len().saturating_add(bytes.len()) > MAX_RECORD_BYTES {
            self.bytes.clear();
            return Err(io::Error::other("log-record-too-large"));
        }
        self.bytes.extend_from_slice(bytes);
        Ok(bytes.len())
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

impl Drop for RecordWriter {
    fn drop(&mut self) {
        // A logging failure must never include the rejected event in a
        // fallback log or interfere with the host application's input.
        if self.logs.append(&self.bytes, SystemTime::now()).is_err()
            && let Ok(mut store) = self.logs.0.lock()
        {
            store.write_failed = true;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn retention_uses_first_record_time_even_after_recent_file_writes() {
        let temp = tempfile::tempdir().unwrap();
        let logs = Logs::open(temp.path().to_path_buf()).unwrap();
        let now = SystemTime::now();
        let old = now - MAX_AGE - Duration::from_secs(1);
        let timestamp = old.duration_since(UNIX_EPOCH).unwrap().as_nanos();
        let path = temp.path().join(format!("fixkey-{timestamp}-0.jsonl"));
        fs::write(&path, "{\"reason\":\"fixture\"}\n").unwrap();
        assert!(fs::metadata(&path).unwrap().modified().unwrap() > old);
        logs.prune(now).unwrap();
        assert!(!path.exists());
    }

    #[test]
    fn sparse_logs_rotate_daily_and_failed_publication_is_visible() {
        use tracing_subscriber::fmt::MakeWriter;
        let temp = tempfile::tempdir().unwrap();
        let logs = Logs::open(temp.path().to_path_buf()).unwrap();
        let now = SystemTime::now();
        logs.append(b"{\"reason\":\"fixture\"}", now).unwrap();
        logs.append(
            b"{\"reason\":\"fixture\"}",
            now + Duration::from_secs(24 * 60 * 60),
        )
        .unwrap();
        assert_eq!(inventory(temp.path()).unwrap().len(), 2);
        assert!(logs.is_healthy());
        {
            let mut writer = logs.make_writer();
            writer.write_all(b"invalid-private-event").unwrap();
        }
        assert!(!logs.is_healthy());
        logs.delete().unwrap();
        assert!(logs.is_healthy());
    }

    #[test]
    fn rotation_retention_and_deletion_preserve_unowned_files() {
        let temp = tempfile::tempdir().unwrap();
        let logs = Logs::open(temp.path().to_path_buf()).unwrap();
        fs::write(temp.path().join("notes.txt"), "keep").unwrap();
        fs::write(temp.path().join("fixkey-notes.jsonl"), "keep").unwrap();
        let payload = serde_json::to_vec(
            &serde_json::json!({"reason":"fixture","padding":"x".repeat(60_000)}),
        )
        .unwrap();
        for _ in 0..200 {
            logs.append(&payload, SystemTime::now()).unwrap();
        }
        let files = inventory(temp.path()).unwrap();
        assert!(files.len() > 1);
        assert!(files.iter().map(|(_, _, size)| size).sum::<u64>() <= MAX_BYTES);
        assert!(files.iter().all(|(_, _, size)| *size <= FILE_BYTES));
        logs.prune(SystemTime::now() + MAX_AGE + Duration::from_secs(1))
            .unwrap();
        assert_eq!(inventory(temp.path()).unwrap(), Vec::new());
        logs.append(b"{\"reason\":\"fixture\"}", SystemTime::now())
            .unwrap();
        logs.delete().unwrap();
        assert_eq!(inventory(temp.path()).unwrap(), Vec::new());
        assert_eq!(
            fs::read_to_string(temp.path().join("notes.txt")).unwrap(),
            "keep"
        );
        assert_eq!(
            fs::read_to_string(temp.path().join("fixkey-notes.jsonl")).unwrap(),
            "keep"
        );
    }

    #[test]
    fn tracing_filter_redacts_unrelated_targets_and_envelopes_records() {
        let temp = tempfile::tempdir().unwrap();
        let logs = Logs::open(temp.path().to_path_buf()).unwrap();
        let subscriber = tracing_subscriber::registry()
            .with(tracing_subscriber::EnvFilter::new("off,fixkey_core=info"))
            .with(
                tracing_subscriber::fmt::layer()
                    .json()
                    .with_ansi(false)
                    .with_writer(logs),
            );
        tracing::subscriber::with_default(subscriber, || {
            tracing::info!(target: "third_party", text = "PRIVATE_CANARY", "unrelated");
            tracing::info!(target: "fixkey_core", reason = "permission-missing", "fixkey status changed");
        });
        let content = inventory(temp.path())
            .unwrap()
            .into_iter()
            .map(|(path, _, _)| fs::read_to_string(path).unwrap())
            .collect::<String>();
        assert!(!content.contains("PRIVATE_CANARY"));
        let value: serde_json::Value = serde_json::from_str(&content).unwrap();
        assert_eq!(value["platform"], platform());
        assert_eq!(value["version"], env!("CARGO_PKG_VERSION"));
        assert_eq!(value["event"]["fields"]["reason"], "permission-missing");
    }
}
