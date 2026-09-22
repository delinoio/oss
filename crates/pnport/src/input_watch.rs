//! Cheap polling of immutable graph inputs and active archives.
use std::{
    collections::HashMap,
    fs,
    path::{Path, PathBuf},
    time::SystemTime,
};

use pnport::{
    cache::archive_identity,
    diagnostic::{Code, Error, Result},
    graph::Input,
};

#[derive(Debug, PartialEq, Eq)]
struct Identity {
    length: u64,
    modified: SystemTime,
    created: Option<SystemTime>,
    #[cfg(unix)]
    unix: (u64, u64, i64, i64),
}

impl Identity {
    fn read(path: &Path) -> Result<Self> {
        let metadata = fs::metadata(path).map_err(|_| changed())?;
        if !metadata.is_file() {
            return Err(changed());
        }
        #[cfg(unix)]
        use std::os::unix::fs::MetadataExt;
        Ok(Self {
            length: metadata.len(),
            modified: metadata.modified().map_err(|_| changed())?,
            created: metadata.created().ok(),
            // ctime catches same-size writes even if the writer restores mtime;
            // inode/device catch atomic replacements. Do not include atime.
            #[cfg(unix)]
            unix: (
                metadata.dev(),
                metadata.ino(),
                metadata.ctime(),
                metadata.ctime_nsec(),
            ),
        })
    }
}

fn changed() -> Error {
    Error::new(
        Code::PnportGraphChanged,
        "A PnP input or active package archive changed or disappeared; restart pnport.",
    )
}

struct Watched {
    sha256: String,
    identity: Option<Identity>,
}

#[derive(Default)]
pub struct InputWatch {
    inputs: HashMap<PathBuf, Watched>,
    #[cfg(test)]
    content_reads: usize,
}

impl InputWatch {
    pub fn register(&mut self, input: &Input) -> Result<()> {
        if let Some(previous) = self.inputs.get(&input.path) {
            if previous.sha256 != input.sha256 {
                return Err(changed());
            }
        } else {
            self.inputs.insert(
                input.path.clone(),
                Watched {
                    sha256: input.sha256.clone(),
                    identity: None,
                },
            );
        }
        Ok(())
    }

    pub fn poll(&mut self) -> Result<()> {
        for (path, input) in &mut self.inputs {
            let before = Identity::read(path)?;
            if input.identity.as_ref() == Some(&before) {
                continue;
            }
            #[cfg(test)]
            {
                self.content_reads += 1;
            }
            if archive_identity(path).map_err(|_| changed())? != input.sha256
                || Identity::read(path)? != before
            {
                return Err(changed());
            }
            tracing::debug!(action = "input_verified", path = %path.display(), "Verified changed input metadata");
            input.identity = Some(before);
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use pnport::graph::digest;

    use super::*;

    #[test]
    fn unchanged_inputs_do_not_reread_contents_and_restored_mtime_is_detected() {
        let root = tempfile::tempdir().unwrap();
        let path = root.path().join("input.zip");
        fs::write(&path, b"original").unwrap();
        let modified = fs::metadata(&path).unwrap().modified().unwrap();
        let mut watch = InputWatch::default();
        watch
            .register(&Input {
                path: path.clone(),
                sha256: digest(b"original"),
            })
            .unwrap();
        for _ in 0..100 {
            watch.poll().unwrap();
        }
        assert_eq!(watch.content_reads, 1);
        // No timing benchmark or filesystem cache assumption: count actual hash
        // reads, then change equal-length bytes and restore the original mtime.
        std::thread::sleep(std::time::Duration::from_millis(2));
        fs::write(&path, b"modified").unwrap();
        fs::File::open(&path)
            .unwrap()
            .set_modified(modified)
            .unwrap();
        #[cfg(unix)]
        assert_eq!(watch.poll().unwrap_err().code, Code::PnportGraphChanged);
    }

    #[test]
    fn replacement_deletion_and_conflicting_archive_registration_require_restart() {
        let root = tempfile::tempdir().unwrap();
        let path = root.path().join("input.zip");
        fs::write(&path, b"original").unwrap();
        let mut watch = InputWatch::default();
        let input = Input {
            path: path.clone(),
            sha256: digest(b"original"),
        };
        watch.register(&input).unwrap();
        watch.poll().unwrap();
        assert!(watch
            .register(&Input {
                path: path.clone(),
                sha256: digest(b"different")
            })
            .is_err());
        let replacement = root.path().join("replacement");
        fs::write(&replacement, b"original").unwrap();
        fs::rename(&replacement, &path).unwrap();
        watch.poll().unwrap();
        assert_eq!(watch.content_reads, 2);
        fs::remove_file(&path).unwrap();
        assert_eq!(watch.poll().unwrap_err().code, Code::PnportGraphChanged);
    }
}
