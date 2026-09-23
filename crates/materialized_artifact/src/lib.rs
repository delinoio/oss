//! Materialize a compile-time–embedded file to disk on demand.
//!
//! Some APIs need a file on disk — `LoadLibrary` and `LD_PRELOAD` take a
//! path, and helper binaries have to exist as actual files to be spawned —
//! but we want to ship a single executable. `materialized_artifact` embeds
//! the file content as a `&'static [u8]` at compile time via the
//! [`artifact!`] macro (same as `include_bytes!`), and [`Materialize::at`]
//! writes it out to disk when first needed — that materialization step is
//! the value-add over a bare `include_bytes!`.
//!
//! Materialized files are named `{name}_{hash}{suffix}` in the caller-chosen
//! directory. The hash (computed at macro-expansion time by [`artifact!`])
//! gives three properties without any coordination between processes:
//!
//! - **No repeated writes.** [`Materialize::at`] verifies an existing file's
//!   bytes before reusing it; repeated calls and re-runs skip writes.
//! - **Correctness.** Two binaries with different embedded content produce
//!   different filenames, so a stale file from an older build is never mistaken
//!   for the current one.
//! - **Coexistence.** Multiple versions of a materialized artifact (e.g. from
//!   different builds of the host program on the same machine) share `dir`
//!   without overwriting each other.

use std::{
    fs,
    io::{self, Read, Write},
    path::{Path, PathBuf},
};

/// A file embedded into the executable at compile time.
///
/// Construct with [`artifact!`]; materialize to disk via
/// [`Artifact::materialize`] + [`Materialize::at`]. See the [crate docs] for
/// the design rationale.
///
/// [crate docs]: crate
#[derive(Clone, Copy)]
pub struct Artifact {
    name: &'static str,
    content: &'static [u8],
    hash: &'static str,
}

/// Construct an [`Artifact`] from an env var holding a file path at compile
/// time — see the macro's own docs for usage and design notes.
pub use materialized_artifact_macros::artifact;

impl Artifact {
    #[doc(hidden)]
    #[must_use]
    pub const fn __new(name: &'static str, content: &'static [u8], hash: &'static str) -> Self {
        Self {
            name,
            content,
            hash,
        }
    }

    /// Start a fluent materialize chain. Supply optional
    /// [`Materialize::suffix`] / [`Materialize::executable`] knobs, then
    /// terminate with [`Materialize::at`].
    pub const fn materialize(&self) -> Materialize<'static> {
        Materialize {
            artifact: *self,
            suffix: "",
            #[cfg(unix)]
            executable: false,
        }
    }
}

/// Builder returned by [`Artifact::materialize`]. Terminate with
/// [`Materialize::at`] to write the file.
#[derive(Clone, Copy)]
#[must_use = "materialize() only configures — call .at(dir) to write the file"]
pub struct Materialize<'a> {
    artifact: Artifact,
    suffix: &'a str,
    #[cfg(unix)]
    executable: bool,
}

impl Materialize<'_> {
    /// Filename suffix appended after `{name}_{hash}` (e.g. `.dll`, `.dylib`).
    /// Defaults to empty.
    pub const fn suffix(self, suffix: &str) -> Materialize<'_> {
        Materialize {
            artifact: self.artifact,
            suffix,
            #[cfg(unix)]
            executable: self.executable,
        }
    }

    /// Mark the materialized file as executable (`0o755` on Unix; no-op on
    /// Windows where the filesystem has no executable bit).
    #[cfg_attr(not(unix), expect(unused_mut, reason = "executable is Unix-only"))]
    pub const fn executable(mut self) -> Self {
        #[cfg(unix)]
        {
            self.executable = true;
        }
        self
    }

    /// Materialize the artifact in `dir` under a content-addressed filename,
    /// writing it if missing. On Unix, newly created files get `0o755` when
    /// [`Materialize::executable`] was called and `0o644` otherwise, and an
    /// existing regular file's bytes are verified before its mode is
    /// reconciled. Symbolic links and mismatched files are rejected.
    ///
    /// Returns the final path. Existing files are read before reuse.
    ///
    /// # Preconditions
    ///
    /// `dir` must already exist — this method does not create it.
    ///
    /// # Errors
    ///
    /// Returns an error if the directory can't be read/written, the stat
    /// fails for any reason other than not-found, or the temp-file rename
    /// fails and the destination still doesn't exist.
    pub fn at(self, dir: impl AsRef<Path>) -> io::Result<PathBuf> {
        let dir = dir.as_ref();
        let path = dir.join(format!(
            "{}_{}{}",
            self.artifact.name, self.artifact.hash, self.suffix
        ));

        #[cfg(unix)]
        let want_mode: u32 = if self.executable { 0o755 } else { 0o644 };

        if self.verified_existing(&path)? {
            return Ok(path);
        }

        // Slow path: write to a unique temp file in the same directory, then
        // rename into place atomically. The temp must live in `dir` (not the
        // system temp) so the final rename stays within one filesystem — cross-
        // filesystem rename isn't atomic. `NamedTempFile`'s `Drop` removes the
        // temp on any early return, so we never leak partial files on error.
        #[cfg(unix)]
        let mut tmp = {
            use std::os::unix::fs::PermissionsExt;
            // `Builder::permissions` sets the mode at open(2) time, so there's
            // no window where the temp exists with the wrong bits.
            tempfile::Builder::new()
                .permissions(fs::Permissions::from_mode(want_mode))
                .tempfile_in(dir)?
        };
        #[cfg(not(unix))]
        let mut tmp = tempfile::NamedTempFile::new_in(dir)?;
        tmp.as_file_mut().write_all(self.artifact.content)?;

        // `persist_noclobber` (link+unlink on Unix, MoveFileExW without
        // REPLACE_EXISTING on Windows) fails atomically if the destination
        // already exists — so two racing processes can't clobber each other
        // mid-write, and the loser sees the error below.
        if let Err(err) = tmp.persist_noclobber(&path) {
            // A concurrent winner is reusable only when its bytes match the
            // embedded artifact. `err.file` drops here, removing our temp.
            if !self.verified_existing(&path)? {
                return Err(err.error);
            }
        }
        Ok(path)
    }

    fn verified_existing(&self, path: &Path) -> io::Result<bool> {
        let metadata = match fs::symlink_metadata(path) {
            Ok(metadata) => metadata,
            Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(false),
            Err(error) => return Err(error),
        };
        if !metadata.file_type().is_file() {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "materialized artifact is not a regular file",
            ));
        }

        let mut options = fs::OpenOptions::new();
        options.read(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.custom_flags(libc::O_NOFOLLOW);
        }
        let mut file = options.open(path)?;
        if !file.metadata()?.is_file() {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "materialized artifact changed type while opening",
            ));
        }
        let mut actual = Vec::with_capacity(self.artifact.content.len());
        file.read_to_end(&mut actual)?;
        if actual != self.artifact.content {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "materialized artifact contents do not match embedded bytes",
            ));
        }

        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let want_mode = if self.executable { 0o755 } else { 0o644 };
            if file.metadata()?.permissions().mode() & 0o777 != want_mode {
                file.set_permissions(fs::Permissions::from_mode(want_mode))?;
            }
        }
        Ok(true)
    }
}
