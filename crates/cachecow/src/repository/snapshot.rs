// SPDX-License-Identifier: Apache-2.0
//! Discovery snapshots and file stamps (Go `snapshot.go`).
//!
//! A snapshot is an ephemeral, same-user validation token, not cache
//! provenance. It contains paths and digests only. Capture it client-side
//! immediately before requesting a lease and verify it under the lease before
//! mutation. Its conservative inventory can invalidate on unrelated source-tree
//! changes.

use std::{collections::BTreeSet, io::Write};

use anyhow::{Result, bail};
use sha2::Digest;

use super::{
    FileStamp, Info, Kind, Snapshot, canonical, contains, discover, git_string, io_not_found,
    path_error, verify, verify_cache_claim, worktree_missing,
};
use crate::{Context, paths};

/// Runs Git, validates every cache, and captures the index, ignore rules, Git
/// configuration and source namespace. Verification uses no subprocess. Cache
/// contents are excluded because the lease owns them; changes to their Git
/// tracking/ignore status still change the captured index or ignore digests.
pub fn capture_snapshot(
    ctx: &Context,
    r: &Info,
    caches: &[String],
    protected: &[String],
) -> Result<Snapshot> {
    let mut s = Snapshot {
        schema: 1,
        repository: r.clone(),
        protected: protected.to_vec(),
        ..Snapshot::default()
    };
    if caches.is_empty() {
        bail!("snapshot requires verified cache roots");
    }
    verify(r)?;
    for p in caches {
        s.caches.push(canonical(p)?);
    }
    let list = git_string(ctx, &r.root, &["worktree", "list", "--porcelain", "-z"])?;
    let mut files: BTreeSet<String> = BTreeSet::new();
    files.insert(paths::join2(&r.common_dir, "config"));
    files.insert(paths::join(&[r.common_dir.as_str(), "info", "exclude"]));
    for field in list.split('\0') {
        let Some(worktree) = field.strip_prefix("worktree ") else {
            continue;
        };
        // A deleted or unmounted worktree has no index or sources to capture.
        // The worktree registry in the inventory still changes when Git prunes
        // or re-adds the record, and the cache validation below rejects a
        // cache inside its reserved path. The snapshot records the skipped
        // path because restoring the directory leaves the registry unchanged;
        // verification then requires fresh discovery. The registration's
        // `gitdir` backlink is stamped as well, because `git worktree repair`
        // at a new location rewrites that file without renaming the
        // registration, and the old path stays absent.
        if worktree_missing(worktree)? {
            tracing::debug!(worktree, "skipping missing registered worktree");
            s.missing_worktrees.push(worktree.to_string());
            files.extend(registration_backlinks(&r.common_dir, worktree)?);
            continue;
        }
        let w = discover(ctx, worktree)?;
        if w.kind == Kind::Bare {
            continue;
        }
        // Git identifies wholly ignored directories. Do not traverse their
        // build churn, but retain the directory entry so symlink replacement is
        // visible. A forced tracked file makes its directory relevant again.
        let ignored = git_string(
            ctx,
            &w.root,
            &[
                "ls-files",
                "--others",
                "--ignored",
                "--exclude-standard",
                "--directory",
                "-z",
            ],
        )?;
        let tracked = git_string(ctx, &w.root, &["ls-files", "-z"])?;
        let tracked: Vec<String> = tracked
            .split('\0')
            .filter(|f| !f.is_empty())
            .map(|f| paths::join2(&w.root, &paths::from_slash(f)))
            .collect();
        for name in ignored.split('\0') {
            if !name.ends_with('/') {
                continue;
            }
            let path = paths::join2(&w.root, &paths::from_slash(name));
            if !tracked.iter().any(|file| contains(&path, file)) {
                s.ignored_directories.push(path);
            }
        }
        for p in [
            paths::join2(&w.root, ".git"),
            paths::join2(&w.git_dir, "index"),
            paths::join2(&w.git_dir, "HEAD"),
            paths::join2(&w.git_dir, "commondir"),
            paths::join2(&w.git_dir, "gitdir"),
            paths::join2(&w.git_dir, "config.worktree"),
        ] {
            if std::fs::metadata(&p).is_ok_and(|m| m.is_dir()) {
                continue;
            }
            files.insert(p);
        }
        s.worktrees.push(w);
    }
    // Includes are represented by their resolved file origins, without
    // retaining any configuration values. Known absent user files are captured
    // as absent.
    let config = git_string(
        ctx,
        &r.root,
        &["config", "--null", "--show-origin", "--list"],
    )?;
    let fields: Vec<&str> = config.split('\0').collect();
    let mut i = 0;
    while i + 1 < fields.len() {
        if let Some(p) = fields[i].strip_prefix("file:") {
            let p = if paths::is_abs(p) {
                p.to_string()
            } else {
                paths::join2(&r.root, p)
            };
            files.insert(p);
        }
        i += 2;
    }
    if let Ok(home) = paths::user_home_dir() {
        files.insert(paths::join2(&home, ".gitconfig"));
        let xdg = std::env::var("XDG_CONFIG_HOME")
            .ok()
            .filter(|v| !v.is_empty())
            .unwrap_or_else(|| paths::join2(&home, ".config"));
        files.insert(paths::join(&[xdg.as_str(), "git", "config"]));
        files.insert(paths::join(&[xdg.as_str(), "git", "ignore"]));
    }
    // --get exits 1 for absence; the standard ignore location is captured
    // above. Other Git/config failures are caught by the list call.
    if let Ok(b) = git_string(
        ctx,
        &r.root,
        &["config", "--path", "--get", "core.excludesfile"],
    ) {
        let p = b.strip_suffix('\n').unwrap_or(&b);
        let p = if paths::is_abs(p) {
            p.to_string()
        } else {
            paths::join2(&r.root, p)
        };
        files.insert(p);
    }
    for p in &files {
        s.files.push(file_stamp(p)?);
    }
    s.inventory_digest = s.inventory()?;
    for p in &s.caches {
        r.validate_cache(ctx, p, protected)?;
    }
    s.verify()?;
    tracing::debug!(
        worktrees = s.worktrees.len(),
        files = s.files.len(),
        ignored_directories = s.ignored_directories.len(),
        "discovery snapshot captured"
    );
    Ok(s)
}

/// Returns the `gitdir` backlink files of the registrations under
/// `<common_dir>/worktrees` that point at `worktree`. Git keeps the worktree
/// path in this file and `git worktree repair` rewrites it after a move
/// without renaming the registration directory. The primary worktree has no
/// registration, so it yields nothing.
fn registration_backlinks(common_dir: &str, worktree: &str) -> Result<Vec<String>> {
    let registry = paths::join2(common_dir, "worktrees");
    let entries = match std::fs::read_dir(&registry) {
        Ok(entries) => entries,
        Err(e) if io_not_found(&e) => return Ok(Vec::new()),
        Err(e) => return Err(path_error("open", &registry, e)),
    };
    let wanted = backlink_key(worktree);
    let mut found = Vec::new();
    for entry in entries {
        let entry = entry.map_err(|e| path_error("readdirent", &registry, e))?;
        let backlink = paths::join2(&entry.path().to_string_lossy(), "gitdir");
        let text = match std::fs::read_to_string(&backlink) {
            Ok(text) => text,
            Err(e) if io_not_found(&e) => continue,
            Err(e) => return Err(path_error("open", &backlink, e)),
        };
        let target = text.trim_end_matches(['\n', '\r']);
        let target = target
            .strip_suffix("/.git")
            .or_else(|| target.strip_suffix("\\.git"))
            .unwrap_or(target);
        if backlink_key(target) == wanted {
            found.push(backlink);
        }
    }
    found.sort();
    Ok(found)
}

/// Compares Git worktree paths regardless of the separator style.
fn backlink_key(path: &str) -> String {
    path.replace('\\', "/")
}

impl Snapshot {
    /// Checks the captured claims using filesystem reads only. Run while
    /// holding all relevant cache/source leases. A mismatch requires fresh
    /// discovery.
    pub fn verify(&self) -> Result<()> {
        if self.schema != 1
            || self.caches.is_empty()
            || self.worktrees.is_empty()
            || self.files.is_empty()
            || self.inventory_digest.len() != 64
        {
            bail!("unknown or incomplete discovery snapshot; repeat discovery");
        }
        verify(&self.repository)?;
        for r in &self.worktrees {
            verify(r)?;
            if r.common_dir != self.repository.common_dir {
                bail!("snapshot contains a different repository");
            }
        }
        for p in &self.caches {
            verify_cache_claim(&self.repository, p, &self.protected)?;
        }
        // A skipped worktree that reappears (restore or remount) adds source
        // paths that the captured inventory does not cover.
        for p in &self.missing_worktrees {
            if !worktree_missing(p)? {
                bail!(
                    "a registered worktree that was missing at discovery is available again; \
                     repeat discovery"
                );
            }
        }
        for old in &self.files {
            if *old != file_stamp(&old.path)? {
                bail!("repository metadata changed after discovery; repeat discovery");
            }
        }
        if self.inventory()? != self.inventory_digest {
            bail!("source paths or ignore rules changed after discovery; repeat discovery");
        }
        Ok(())
    }

    fn inventory(&self) -> Result<String> {
        let mut h = HashWriter(sha2::Sha256::new());
        for r in &self.worktrees {
            let git_entry = paths::join2(&r.root, ".git");
            paths::walk_dir(&r.root, &mut |d| {
                let p = &d.path;
                if *p == git_entry {
                    return Ok(if d.is_dir() {
                        paths::Walk::SkipDir
                    } else {
                        paths::Walk::Continue
                    });
                }
                for cache in &self.caches {
                    if contains(cache, p) {
                        return Ok(if d.is_dir() {
                            paths::Walk::SkipDir
                        } else {
                            paths::Walk::Continue
                        });
                    }
                }
                // Names/types detect creation, deletion, and replacement of
                // source paths. Source bytes are not hashed: build tools
                // retain freshness responsibility.
                encode(&mut h, &(p.as_str(), paths::go_type_bits(d.file_type)))?;
                if d.is_dir() && self.ignored_directories.iter().any(|i| i == p) {
                    return Ok(paths::Walk::SkipDir);
                }
                if d.name == ".gitignore" {
                    encode(&mut h, &file_stamp(p)?)?;
                    return Ok(paths::Walk::Continue);
                }
                if d.file_type.is_symlink() {
                    let target = std::fs::read_link(p).map_err(|e| path_error("readlink", p, e))?;
                    encode(&mut h, &paths::path_string(&target)?)?;
                }
                Ok(paths::Walk::Continue)
            })?;
        }
        // Worktree registration changes must invalidate the captured
        // membership.
        let registry = paths::join2(&self.repository.common_dir, "worktrees");
        match std::fs::read_dir(&registry) {
            Ok(entries) => {
                let mut names = Vec::new();
                for entry in entries {
                    let entry = entry.map_err(|e| path_error("readdirent", &registry, e))?;
                    names.push(entry.file_name().to_string_lossy().into_owned());
                }
                names.sort();
                for name in names {
                    encode(&mut h, &name)?;
                }
            }
            Err(e) if io_not_found(&e) => {}
            Err(e) => return Err(path_error("open", &registry, e)),
        }
        Ok(hex::encode(h.0.finalize()))
    }
}

struct HashWriter(sha2::Sha256);

impl Write for HashWriter {
    fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
        self.0.update(buf);
        Ok(buf.len())
    }

    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

/// Writes one JSON line, like Go's `json.Encoder.Encode`.
fn encode<T: serde::Serialize + ?Sized>(h: &mut HashWriter, value: &T) -> Result<()> {
    serde_json::to_writer(&mut *h, value)?;
    h.write_all(b"\n")?;
    Ok(())
}

fn modified_nanos(m: &std::fs::Metadata) -> i64 {
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        m.mtime()
            .saturating_mul(1_000_000_000)
            .saturating_add(m.mtime_nsec())
    }
    #[cfg(not(unix))]
    {
        match m.modified() {
            Ok(t) => match t.duration_since(std::time::UNIX_EPOCH) {
                Ok(d) => i64::try_from(d.as_nanos()).unwrap_or(i64::MAX),
                Err(e) => -i64::try_from(e.duration().as_nanos()).unwrap_or(i64::MAX),
            },
            Err(_) => 0,
        }
    }
}

pub(super) fn file_stamp(p: &str) -> Result<FileStamp> {
    let mut out = FileStamp {
        path: p.to_string(),
        ..FileStamp::default()
    };
    let st = match std::fs::symlink_metadata(p) {
        Ok(st) => st,
        Err(e) if io_not_found(&e) => return Ok(out),
        Err(e) => return Err(path_error("lstat", p, e)),
    };
    out.exists = true;
    out.mode = paths::go_file_mode(&st);
    out.size = i64::try_from(st.len()).unwrap_or(i64::MAX);
    out.modified = modified_nanos(&st);
    let bytes = if st.file_type().is_symlink() {
        let target = std::fs::read_link(p).map_err(|e| path_error("readlink", p, e))?;
        let mut b = paths::path_string(&target)?.into_bytes();
        let contents = std::fs::read(p).map_err(|e| path_error("open", p, e))?;
        b.extend_from_slice(&contents);
        b
    } else if st.file_type().is_file() {
        std::fs::read(p).map_err(|e| path_error("open", p, e))?
    } else {
        bail!("snapshot metadata is not a regular file");
    };
    out.digest = crate::jsonx::sha256_hex(&bytes);
    Ok(out)
}

/// Retains paths and content digests, never configuration bytes. Include
/// absent candidates so creation can invalidate a discovered cache claim.
pub fn capture_file_stamps(paths: &[String]) -> Result<Vec<FileStamp>> {
    paths.iter().map(|p| file_stamp(p)).collect()
}

pub fn verify_file_stamps(stamps: &[FileStamp]) -> Result<()> {
    for old in stamps {
        if *old != file_stamp(&old.path)? {
            bail!("cache-selection configuration changed after discovery; repeat discovery");
        }
    }
    Ok(())
}
