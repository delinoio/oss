use std::{
    fs::{self, File, OpenOptions},
    io::{self, Write},
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::{Duration, SystemTime, UNIX_EPOCH},
};

const LOG_FILE_PREFIX: &str = "devhud-";
const LOG_FILE_SUFFIX: &str = ".jsonl";
const LOG_RETENTION: Duration = Duration::from_secs(7 * 24 * 60 * 60);
const MAX_LOG_BYTES: u64 = 20 * 1024 * 1024;
const MAX_LOG_FILE_BYTES: u64 = 2 * 1024 * 1024;

#[derive(Clone)]
pub(crate) struct LocalLogWriter {
    state: Arc<Mutex<LocalLogState>>,
}

impl LocalLogWriter {
    pub(crate) fn new(application_id: &str) -> io::Result<Self> {
        let directory = log_directory(application_id)
            .ok_or_else(|| io::Error::other("local log directory is unavailable"))?;
        Ok(Self {
            state: Arc::new(Mutex::new(LocalLogState::new(directory)?)),
        })
    }

    pub(crate) fn clear(&self) -> io::Result<()> {
        self.state
            .lock()
            .map_err(|_| io::Error::other("local log state is unavailable"))?
            .clear()
    }
}

impl Write for LocalLogWriter {
    fn write(&mut self, buffer: &[u8]) -> io::Result<usize> {
        self.state
            .lock()
            .map_err(|_| io::Error::other("local log state is unavailable"))?
            .write(buffer)
    }

    fn flush(&mut self) -> io::Result<()> {
        self.state
            .lock()
            .map_err(|_| io::Error::other("local log state is unavailable"))?
            .flush()
    }
}

struct LocalLogState {
    directory: PathBuf,
    file: Option<File>,
    file_bytes: u64,
    sequence: u64,
}

impl LocalLogState {
    fn new(directory: PathBuf) -> io::Result<Self> {
        fs::create_dir_all(&directory)?;
        prune_logs(
            &directory,
            SystemTime::now(),
            MAX_LOG_BYTES - MAX_LOG_FILE_BYTES,
        )?;
        let mut state = Self {
            directory,
            file: None,
            file_bytes: 0,
            sequence: 0,
        };
        state.open_file()?;
        Ok(state)
    }

    fn write(&mut self, buffer: &[u8]) -> io::Result<usize> {
        let buffer_bytes = u64::try_from(buffer.len()).unwrap_or(u64::MAX);
        if buffer_bytes > MAX_LOG_FILE_BYTES {
            return Ok(buffer.len());
        }
        if self.file_bytes.saturating_add(buffer_bytes) > MAX_LOG_FILE_BYTES {
            self.rotate()?;
        }
        let file = self
            .file
            .as_mut()
            .ok_or_else(|| io::Error::other("local log file is unavailable"))?;
        let written = file.write(buffer)?;
        self.file_bytes = self
            .file_bytes
            .saturating_add(u64::try_from(written).unwrap_or(u64::MAX));
        Ok(written)
    }

    fn flush(&mut self) -> io::Result<()> {
        self.file
            .as_mut()
            .ok_or_else(|| io::Error::other("local log file is unavailable"))?
            .flush()
    }

    fn rotate(&mut self) -> io::Result<()> {
        self.file.take();
        prune_logs(
            &self.directory,
            SystemTime::now(),
            MAX_LOG_BYTES - MAX_LOG_FILE_BYTES,
        )?;
        self.open_file()
    }

    fn clear(&mut self) -> io::Result<()> {
        self.file.take();
        let removal = remove_managed_logs(&self.directory);
        let reopen = self.open_file();
        removal.and(reopen)
    }

    fn open_file(&mut self) -> io::Result<()> {
        let timestamp = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .as_millis();
        loop {
            let path = self.directory.join(format!(
                "{LOG_FILE_PREFIX}{timestamp}-{}-{}{LOG_FILE_SUFFIX}",
                std::process::id(),
                self.sequence
            ));
            self.sequence = self.sequence.saturating_add(1);
            match OpenOptions::new().create_new(true).append(true).open(path) {
                Ok(file) => {
                    self.file = Some(file);
                    self.file_bytes = 0;
                    return Ok(());
                }
                Err(error) if error.kind() == io::ErrorKind::AlreadyExists => {}
                Err(error) => return Err(error),
            }
        }
    }
}

fn log_directory(application_id: &str) -> Option<PathBuf> {
    #[cfg(target_os = "macos")]
    {
        dirs::home_dir()
            .map(|directory| directory.join("Library").join("Logs").join(application_id))
    }
    #[cfg(not(target_os = "macos"))]
    {
        dirs::data_local_dir().map(|directory| directory.join(application_id).join("logs"))
    }
}

fn is_managed_log(path: &Path) -> bool {
    path.file_name()
        .and_then(|name| name.to_str())
        .is_some_and(|name| name.starts_with(LOG_FILE_PREFIX) && name.ends_with(LOG_FILE_SUFFIX))
}

fn managed_logs(directory: &Path) -> io::Result<Vec<(PathBuf, SystemTime, u64)>> {
    let mut logs = Vec::new();
    for entry in fs::read_dir(directory)? {
        let entry = entry?;
        if !entry.file_type()?.is_file() || !is_managed_log(&entry.path()) {
            continue;
        }
        let metadata = entry.metadata()?;
        logs.push((
            entry.path(),
            metadata.modified().unwrap_or(UNIX_EPOCH),
            metadata.len(),
        ));
    }
    Ok(logs)
}

fn prune_logs(directory: &Path, now: SystemTime, retained_bytes: u64) -> io::Result<()> {
    let mut logs = managed_logs(directory)?;
    for (path, modified, _) in &logs {
        if now.duration_since(*modified).unwrap_or_default() > LOG_RETENTION {
            fs::remove_file(path)?;
        }
    }

    logs = managed_logs(directory)?;
    logs.sort_by_key(|(_, modified, _)| *modified);
    let mut total_bytes = logs
        .iter()
        .fold(0_u64, |total, (_, _, bytes)| total.saturating_add(*bytes));
    for (path, _, bytes) in logs {
        if total_bytes <= retained_bytes {
            break;
        }
        fs::remove_file(path)?;
        total_bytes = total_bytes.saturating_sub(bytes);
    }
    Ok(())
}

fn remove_managed_logs(directory: &Path) -> io::Result<()> {
    for (path, _, _) in managed_logs(directory)? {
        fs::remove_file(path)?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn temporary_directory(name: &str) -> PathBuf {
        std::env::temp_dir().join(format!(
            "devhud-local-log-{name}-{}-{:?}",
            std::process::id(),
            std::thread::current().id()
        ))
    }

    #[test]
    fn production_constructor_resolves_the_platform_log_directory() {
        let constructor: fn(&str) -> io::Result<LocalLogWriter> = LocalLogWriter::new;
        let _ = constructor;
        assert!(log_directory("dev.deli.devhud").is_some());
    }

    #[test]
    fn pruning_keeps_only_managed_logs_within_the_byte_budget() {
        let directory = temporary_directory("prune");
        fs::create_dir_all(&directory).unwrap();
        fs::write(directory.join("devhud-1.jsonl"), [0_u8; 8]).unwrap();
        fs::write(directory.join("devhud-2.jsonl"), [0_u8; 8]).unwrap();
        fs::write(directory.join("user-export.jsonl"), [0_u8; 8]).unwrap();

        prune_logs(&directory, SystemTime::now(), 8).unwrap();

        assert_eq!(managed_logs(&directory).unwrap().len(), 1);
        assert!(directory.join("user-export.jsonl").exists());
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn clearing_logs_reopens_the_bounded_sink() {
        let directory = temporary_directory("clear");
        let mut writer = LocalLogWriter {
            state: Arc::new(Mutex::new(LocalLogState::new(directory.clone()).unwrap())),
        };
        writer.write_all(b"{\"event\":\"before-reset\"}\n").unwrap();

        writer.clear().unwrap();
        writer.write_all(b"{\"event\":\"after-reset\"}\n").unwrap();
        writer.flush().unwrap();

        let logs = managed_logs(&directory).unwrap();
        assert_eq!(logs.len(), 1);
        assert_eq!(
            fs::read_to_string(&logs[0].0).unwrap(),
            "{\"event\":\"after-reset\"}\n"
        );
        fs::remove_dir_all(directory).unwrap();
    }
}
