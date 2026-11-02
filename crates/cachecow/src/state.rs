// SPDX-License-Identifier: Apache-2.0
//! Versioned, current-user-private CacheCow records (Go `internal/state`).

use std::{
    fs,
    io::{self, Write},
};

use anyhow::{Result, anyhow};
use serde::{Deserialize, Serialize, de::DeserializeOwned};

use crate::{lease, paths};

/// Root of current-user-private CacheCow storage.
#[derive(Debug, Clone)]
pub struct Store {
    pub root: String,
}

/// Per-worktree settings record `settings-<key>.json`.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Settings {
    #[serde(rename = "schemaVersion", default = "one")]
    pub schema_version: i64,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub source: String,
}

const fn one() -> i64 {
    1
}

impl Store {
    /// Opens (creating privately) the state root. An empty `root` selects
    /// `user_config_dir()/cachecow`, or `user_cache_dir()/cachecow` on Windows.
    pub fn open(root: &str) -> Result<Self> {
        let root = if root.is_empty() {
            let base = if cfg!(windows) {
                paths::user_cache_dir()?
            } else {
                paths::user_config_dir()?
            };
            paths::join2(&base, "cachecow")
        } else {
            root.to_string()
        };
        let root = paths::abs(&root)?;
        lease::ensure_private_directory(&root)?;
        let root = paths::eval_symlinks(&root)?;
        tracing::debug!(root = %root, "state store opened");
        Ok(Self { root })
    }

    /// Ensures the private subdirectory `name` exists and returns its path.
    pub fn directory(&self, name: &str) -> Result<String> {
        let p = paths::join2(&self.root, name);
        lease::ensure_private_directory(&p)?;
        Ok(p)
    }

    fn settings_path(&self, worktree: &str) -> String {
        paths::join2(&self.root, &format!("settings-{}.json", key(worktree)))
    }

    /// Reads the saved settings for a worktree ID; absent records yield
    /// defaults.
    pub fn settings(&self, worktree: &str) -> Result<Settings> {
        let v = match read::<Settings>(&self.settings_path(worktree)) {
            Ok(v) => v,
            Err(e) if crate::error::is_not_found(&e) => {
                return Ok(Settings {
                    schema_version: 1,
                    source: String::new(),
                });
            }
            Err(e) => return Err(e),
        };
        if v.schema_version != 1 {
            return Err(anyhow!(
                "unsupported settings schemaVersion; use a compatible ccow without removing the \
                 record"
            ));
        }
        Ok(v)
    }

    /// Saves (or clears with an empty string) the source selection.
    pub fn set_source(&self, worktree: &str, source: &str) -> Result<()> {
        let mut v = self.settings(worktree)?;
        v.source = source.to_string();
        write(&self.settings_path(worktree), &v)
    }
}

/// SHA-256 hex digest used for record names.
pub fn key(value: &str) -> String {
    crate::jsonx::sha256_hex(value.as_bytes())
}

/// Validates privacy, then strictly decodes one record. A missing file keeps
/// its I/O `NotFound` cause (see [`crate::error::is_not_found`]).
pub fn read<T: DeserializeOwned>(path: &str) -> Result<T> {
    let bytes = lease::read_private_file(path)?;
    if bytes.is_empty() {
        // Older versions could leave an empty record behind. Nothing repairs
        // it, so name the file for the user.
        return Err(anyhow!(
            "state record {path} is empty; delete the file and retry"
        ));
    }
    let mut de = serde_json::Deserializer::from_slice(&bytes);
    let value = T::deserialize(&mut de)?;
    de.end()
        .map_err(|_| anyhow!("trailing data in state record"))?;
    Ok(value)
}

/// Atomically replaces a private record (temporary file, fsync, rename, and
/// parent directory sync on Unix).
pub fn write<T: Serialize>(path: &str, value: &T) -> Result<()> {
    match lease::validate_private_file(path) {
        Ok(()) => {}
        Err(e) if crate::error::is_not_found(&e) => {}
        Err(e) => return Err(e),
    }
    let bytes = serde_json::to_vec(value)?;
    let dir = paths::dir(path);
    let (temp, mut file) = create_temp(&dir, ".ccow-state-")?;
    let result = (|| -> Result<()> {
        file.write_all(&bytes)?;
        file.sync_all()?;
        drop(file);
        fs::rename(&temp, path)?;
        sync_dir(&dir)
    })();
    if result.is_err() {
        let _ignored = fs::remove_file(&temp);
    }
    result
}

/// Creates a new private temporary file in `dir`, like Go's `os.CreateTemp`.
fn create_temp(dir: &str, prefix: &str) -> Result<(String, fs::File)> {
    loop {
        let name = paths::join2(dir, &format!("{prefix}{}", uuid::Uuid::now_v7().simple()));
        let mut options = fs::OpenOptions::new();
        options.write(true).create_new(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        match options.open(&name) {
            Ok(f) => return Ok((name, f)),
            Err(e) if e.kind() == io::ErrorKind::AlreadyExists => {}
            Err(e) => return Err(e.into()),
        }
    }
}

#[cfg(unix)]
fn sync_dir(dir: &str) -> Result<()> {
    fs::File::open(dir)?.sync_all()?;
    Ok(())
}

/// Windows does not expose a portable directory fsync.
#[cfg(windows)]
fn sync_dir(_dir: &str) -> Result<()> {
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn store() -> (tempfile::TempDir, Store) {
        let temp = tempfile::tempdir().unwrap();
        let s = Store::open(&paths::join2(temp.path().to_str().unwrap(), "private")).unwrap();
        (temp, s)
    }

    #[test]
    fn settings_version_and_isolation() {
        let (_temp, s) = store();
        s.set_source("one", "/source").unwrap();
        assert_eq!(s.settings("one").unwrap().source, "/source");
        assert_eq!(s.settings("two").unwrap().source, "");
        let p = s.settings_path("one");
        write(
            &p,
            &Settings {
                schema_version: 2,
                source: "keep".into(),
            },
        )
        .unwrap();
        let before = fs::read(&p).unwrap();
        assert!(s.set_source("one", "").is_err(), "rewrote unknown schema");
        assert_eq!(
            before,
            fs::read(&p).unwrap(),
            "modified unsupported settings"
        );
    }

    #[test]
    fn read_rejects_trailing_and_unknown_and_reports_missing() {
        let (_temp, s) = store();
        let p = paths::join2(&s.root, "record.json");
        let missing = read::<Settings>(&p).unwrap_err();
        assert!(crate::error::is_not_found(&missing));
        write(
            &p,
            &Settings {
                schema_version: 1,
                source: String::new(),
            },
        )
        .unwrap();
        assert_eq!(fs::read_to_string(&p).unwrap(), "{\"schemaVersion\":1}");
        fs::write(&p, "{\"schemaVersion\":1}{}").unwrap();
        assert!(
            read::<Settings>(&p)
                .unwrap_err()
                .to_string()
                .contains("trailing data")
        );
        fs::write(&p, "{\"schemaVersion\":1,\"other\":1}").unwrap();
        assert!(read::<Settings>(&p).is_err());
    }

    #[cfg(unix)]
    #[test]
    fn rejects_public_records() {
        use std::os::unix::fs::PermissionsExt;
        let (_temp, s) = store();
        let p = paths::join2(&s.root, "record.json");
        write(
            &p,
            &Settings {
                schema_version: 1,
                source: String::new(),
            },
        )
        .unwrap();
        fs::set_permissions(&p, fs::Permissions::from_mode(0o644)).unwrap();
        assert!(read::<Settings>(&p).is_err());
        assert!(
            write(
                &p,
                &Settings {
                    schema_version: 1,
                    source: String::new()
                }
            )
            .is_err()
        );
    }

    #[test]
    fn empty_record_error_names_the_file() {
        let (_temp, s) = store();
        let p = paths::join2(&s.root, "cache-empty.json");
        fs::write(&p, "").unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(&p, fs::Permissions::from_mode(0o600)).unwrap();
        }
        let err = read::<Settings>(&p).unwrap_err();
        let text = err.to_string();
        assert!(text.contains(&p) && text.contains("delete"), "{text}");
        assert!(!crate::error::is_not_found(&err));
    }

    #[cfg(unix)]
    #[test]
    fn missing_record_is_not_created_by_read_or_write_checks() {
        let (_temp, s) = store();
        let p = paths::join2(&s.root, "record.json");
        assert!(crate::error::is_not_found(
            &read::<Settings>(&p).unwrap_err()
        ));
        assert!(fs::symlink_metadata(&p).is_err());
    }

    /// Runs `writer` against a record while a reader loops for about two
    /// seconds, then checks that every read result is a valid record or
    /// `NotFound` and that no zero-length record ever existed.
    #[cfg(unix)]
    fn race(remove: bool) {
        use std::{
            sync::atomic::{AtomicBool, Ordering},
            time::{Duration, Instant},
        };
        let (_temp, s) = store();
        let p = paths::join2(&s.root, "cache-race.json");
        let stop = AtomicBool::new(false);
        // Stops the writer even when a reader assertion panics, otherwise the
        // thread scope would wait forever.
        struct Stop<'a>(&'a AtomicBool);
        impl Drop for Stop<'_> {
            fn drop(&mut self) {
                self.0.store(true, Ordering::Relaxed);
            }
        }
        let value = Settings {
            schema_version: 1,
            source: "/source".into(),
        };
        // Without removal the record always exists, so reads must never fail.
        write(&p, &value).unwrap();
        let (mut reads, mut found, mut empty_seen) = (0u64, 0u64, 0u64);
        std::thread::scope(|scope| {
            scope.spawn(|| {
                while !stop.load(Ordering::Relaxed) {
                    write(&p, &value).unwrap();
                    if remove {
                        let _ = fs::remove_file(&p);
                    }
                }
            });
            let _stop = Stop(&stop);
            let deadline = Instant::now() + Duration::from_secs(2);
            while Instant::now() < deadline {
                match read::<Settings>(&p) {
                    Ok(v) => {
                        assert_eq!(v, value);
                        found += 1;
                    }
                    Err(e) => assert!(
                        remove && crate::error::is_not_found(&e),
                        "unexpected read error: {e:#}"
                    ),
                }
                if fs::metadata(&p).is_ok_and(|m| m.len() == 0) {
                    empty_seen += 1;
                }
                reads += 1;
            }
        });
        assert_eq!(empty_seen, 0, "zero-length record appeared");
        assert!(found > 0 && reads >= found);
    }

    #[cfg(unix)]
    #[test]
    fn concurrent_write_and_remove_never_leave_an_empty_record() {
        race(true);
    }

    #[cfg(unix)]
    #[test]
    fn concurrent_write_never_rejects_a_replaced_record() {
        race(false);
    }
}
