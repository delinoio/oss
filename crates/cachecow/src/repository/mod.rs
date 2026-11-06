// SPDX-License-Identifier: Apache-2.0
//! Git worktree discovery and local cache boundary validation (Go
//! `internal/repository`). Discovery does not authorize mutation: callers
//! revalidate under their locks.

use std::io::Read;

use anyhow::{Result, anyhow, bail};
use serde::{Deserialize, Serialize};

use crate::{Context, paths};

mod owner;
mod snapshot;
mod verify;

pub use owner::check_owner;
pub use snapshot::{capture_file_stamps, capture_snapshot, verify_file_stamps};
pub use verify::{verify, verify_cache_claim};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Kind {
    #[default]
    Checkout,
    Bare,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum SourceOrigin {
    Invocation,
    Saved,
    Primary,
}

/// Canonical repository identity. Field names match the Go JSON encoding.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Info {
    /// Canonical execution directory. Compare it, never the raw process cwd,
    /// with the canonical `root` (#4575).
    #[serde(rename = "CWD")]
    pub cwd: String,
    #[serde(rename = "Root")]
    pub root: String,
    #[serde(rename = "GitDir")]
    pub git_dir: String,
    #[serde(rename = "CommonDir")]
    pub common_dir: String,
    /// Empty for a bare repository, including its linked worktrees.
    #[serde(rename = "Primary")]
    pub primary: String,
    #[serde(rename = "Kind")]
    pub kind: Kind,
    /// SHA-256 of the canonical worktree Git directory.
    #[serde(rename = "ID")]
    pub id: String,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Source {
    pub repository: Info,
    pub origin: SourceOrigin,
    pub is_self: bool,
}

/// One input file's path, metadata and content digest (never its contents).
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FileStamp {
    #[serde(rename = "Path")]
    pub path: String,
    #[serde(rename = "Exists")]
    pub exists: bool,
    #[serde(rename = "Mode")]
    pub mode: u32,
    #[serde(rename = "Size")]
    pub size: i64,
    #[serde(rename = "Modified")]
    pub modified: i64,
    #[serde(rename = "Digest")]
    pub digest: String,
}

/// Ephemeral same-user validation token captured before requesting a lease.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Snapshot {
    #[serde(rename = "Schema")]
    pub schema: i64,
    #[serde(rename = "Repository")]
    pub repository: Info,
    #[serde(rename = "Worktrees")]
    pub worktrees: Vec<Info>,
    /// Registered worktree directories that were missing at capture and were
    /// skipped. Verification requires them to stay missing, because a restored
    /// or remounted directory brings sources that this snapshot never
    /// inventoried.
    #[serde(rename = "MissingWorktrees", default)]
    pub missing_worktrees: Vec<String>,
    #[serde(rename = "IgnoredDirectories")]
    pub ignored_directories: Vec<String>,
    #[serde(rename = "Caches")]
    pub caches: Vec<String>,
    #[serde(rename = "Protected")]
    pub protected: Vec<String>,
    #[serde(rename = "Files")]
    pub files: Vec<FileStamp>,
    #[serde(rename = "InventoryDigest")]
    pub inventory_digest: String,
}

/// An operating-system failure for a path. Display matches Go's
/// `*fs.PathError` ("op path: cause"), and the I/O cause stays in the chain
/// for [`crate::error::is_not_found`].
#[derive(Debug, thiserror::Error)]
#[error("{op} {path}: {source}")]
pub(crate) struct PathError {
    pub op: &'static str,
    pub path: String,
    #[source]
    pub source: std::io::Error,
}

pub(crate) fn path_error(op: &'static str, path: &str, source: std::io::Error) -> anyhow::Error {
    anyhow::Error::new(PathError {
        op,
        path: path.to_string(),
        source,
    })
}

/// Go `os.IsNotExist` for a raw I/O error.
pub(crate) fn io_not_found(e: &std::io::Error) -> bool {
    e.kind() == std::io::ErrorKind::NotFound
}

/// Reports whether a registered worktree directory no longer exists. Git keeps
/// the record of a deleted or unmounted worktree until `git worktree prune`,
/// and a locked record or Git older than 2.31 carries no `prunable` marker, so
/// the decision comes from the filesystem. Any error other than absence (for
/// example permission denied) is returned so the caller still fails.
pub(crate) fn worktree_missing(path: &str) -> Result<bool> {
    match std::fs::symlink_metadata(path) {
        Ok(_) => Ok(false),
        Err(e) if io_not_found(&e) => Ok(true),
        Err(e) => Err(path_error("lstat", path, e)),
    }
}

/// Runs `cmd` with null stdin and captured stdout/stderr, killing it when
/// `ctx` finishes (Go `exec.CommandContext(...).Output()`).
pub(crate) fn run_output(
    ctx: &Context,
    mut cmd: std::process::Command,
) -> Result<std::process::Output> {
    use std::process::Stdio;
    ctx.err()?;
    cmd.stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    let mut child = cmd.spawn()?;
    let mut stdout = child.stdout.take();
    let mut stderr = child.stderr.take();
    let out_reader = std::thread::spawn(move || {
        let mut b = Vec::new();
        if let Some(s) = stdout.as_mut() {
            let _ = s.read_to_end(&mut b);
        }
        b
    });
    let err_reader = std::thread::spawn(move || {
        let mut b = Vec::new();
        if let Some(s) = stderr.as_mut() {
            let _ = s.read_to_end(&mut b);
        }
        b
    });
    let status = loop {
        if let Some(status) = child.try_wait()? {
            break status;
        }
        if ctx.is_done() {
            let _ = child.kill();
            let _ = child.wait();
            let _ = out_reader.join();
            let _ = err_reader.join();
            return Err(ctx
                .err()
                .err()
                .unwrap_or(crate::context::CancelError::Canceled)
                .into());
        }
        std::thread::sleep(std::time::Duration::from_millis(2));
    };
    let stdout = out_reader.join().unwrap_or_default();
    let stderr = err_reader.join().unwrap_or_default();
    Ok(std::process::Output {
        status,
        stdout,
        stderr,
    })
}

#[cfg(test)]
thread_local! {
    /// Test-only replacement for the ambient environment seen by Git on this
    /// thread, so tests never mutate process-wide state.
    pub(crate) static ENV_OVERRIDE: std::cell::RefCell<Option<Vec<(String, String)>>> =
        const { std::cell::RefCell::new(None) };
}

fn ambient_env() -> Vec<(String, String)> {
    #[cfg(test)]
    if let Some(env) = ENV_OVERRIDE.with(|v| v.borrow().clone()) {
        return env;
    }
    std::env::vars_os()
        .map(|(k, v)| {
            (
                k.to_string_lossy().into_owned(),
                v.to_string_lossy().into_owned(),
            )
        })
        .collect()
}

/// Runs Git for the repository selected by `cwd` only. Ambient `GIT_*`
/// repository redirection is removed from the environment.
pub(crate) fn git(ctx: &Context, cwd: &str, args: &[&str]) -> Result<Vec<u8>> {
    let failure =
        || anyhow!("Git discovery failed; check the checkout, permissions, and Git installation");
    let mut cmd = std::process::Command::new("git");
    cmd.arg("-C").arg(cwd).args(args).env_clear();
    for (k, v) in ambient_env() {
        if !k.starts_with("GIT_") {
            cmd.env(k, v);
        }
    }
    let output = run_output(ctx, cmd).map_err(|_| failure())?;
    if !output.status.success() {
        tracing::debug!(args = ?args, status = ?output.status, "git command failed");
        return Err(failure());
    }
    Ok(output.stdout)
}

pub(crate) fn git_string(ctx: &Context, cwd: &str, args: &[&str]) -> Result<String> {
    String::from_utf8(git(ctx, cwd, args)?)
        .map_err(|_| anyhow!("Git returned output that is not valid UTF-8"))
}

fn git_path(ctx: &Context, cwd: &str, flag: &str) -> Result<String> {
    let s = git_string(ctx, cwd, &["rev-parse", "--path-format=absolute", flag])?;
    canonical(s.strip_suffix('\n').unwrap_or(&s))
}

pub(crate) fn identity(git_dir: &str) -> String {
    crate::jsonx::sha256_hex(git_dir.as_bytes())
}

/// Discovers the canonical repository for `cwd` (runs Git). [`Info::cwd`] is
/// the canonical working directory.
pub fn discover(ctx: &Context, cwd: &str) -> Result<Info> {
    let mut r = Info::default();
    let p = canonical(cwd)?;
    check_owner(&p)?;
    r.cwd.clone_from(&p);
    r.git_dir = git_path(ctx, &p, "--absolute-git-dir")?;
    r.common_dir = git_path(ctx, &p, "--git-common-dir")?;
    for v in [&r.git_dir, &r.common_dir] {
        check_owner(v)?;
    }
    let b = git_string(ctx, &p, &["rev-parse", "--is-bare-repository"])?;
    r.kind = Kind::Checkout;
    if b.trim() == "true" {
        r.kind = Kind::Bare;
        r.root.clone_from(&r.common_dir);
    } else {
        r.root = git_path(ctx, &p, "--show-toplevel")?;
    }
    check_owner(&r.root)?;
    let b = git_string(ctx, &p, &["worktree", "list", "--porcelain", "-z"])?;
    // Git lists the primary worktree (or bare repository) first. NUL records
    // preserve spaces, newlines, and non-ASCII names without Git quote
    // decoding.
    let fields: Vec<&str> = b.split('\0').collect();
    let Some(first) = fields.first().and_then(|f| f.strip_prefix("worktree ")) else {
        bail!("Git returned an unknown worktree list; upgrade Git");
    };
    let mut bare = false;
    for f in &fields[1..] {
        if f.is_empty() {
            break;
        }
        if *f == "bare" {
            bare = true;
        }
    }
    if !bare {
        r.primary = canonical(first)?;
    }
    r.id = identity(&r.git_dir);
    tracing::debug!(root = %r.root, kind = ?r.kind, "repository discovered");
    Ok(r)
}

/// Invocation > saved setting > primary checkout. An invalid explicit
/// selection never falls back to a different source.
pub fn select_source(
    ctx: &Context,
    current: &Info,
    invocation: &str,
    saved: &str,
) -> Result<Source> {
    let (mut p, mut origin) = (invocation.to_string(), SourceOrigin::Invocation);
    if p.is_empty() {
        (p, origin) = (saved.to_string(), SourceOrigin::Saved);
    }
    if p.is_empty() {
        (p, origin) = (current.primary.clone(), SourceOrigin::Primary);
    }
    if p.is_empty() {
        bail!("bare repository has no primary checkout; select a source worktree with --source");
    }
    if !paths::is_abs(&p) {
        p = paths::join2(&current.cwd, &p);
    }
    // Check the path first. Without this, a deleted or mistyped source falls
    // through to the ownership check, which walks up to the nearest existing
    // parent and can report a misleading ownership error (for example for a
    // path below a directory that another user owns).
    match std::fs::metadata(&p) {
        Ok(m) if !m.is_dir() => {
            tracing::debug!(source = %p, ?origin, "source is not a directory");
            bail!(
                "source {p} is not a directory; select a checkout of this Git repository with \
                 --source"
            );
        }
        Ok(_) => {}
        Err(e) if io_not_found(&e) => {
            tracing::debug!(source = %p, ?origin, "source path does not exist");
            bail!(
                "source {p} does not exist; select an existing checkout of this Git repository \
                 with --source, or run ccow config unset source to remove a saved source"
            );
        }
        // Other errors, such as a denied permission, surface in discovery.
        Err(_) => {}
    }
    let r = discover(ctx, &p)?;
    if r.kind == Kind::Bare || r.common_dir != current.common_dir {
        bail!("source must be a checkout of this same Git repository; select another --source");
    }
    let is_self = r.id == current.id;
    tracing::debug!(source = %r.root, ?origin, is_self, "source selected");
    Ok(Source {
        repository: r,
        origin,
        is_self,
    })
}

/// Resolves existing ancestors so aliases of absent caches share an identity.
/// Rejects `..` components and dangling links.
pub fn canonical(path: &str) -> Result<String> {
    if path.is_empty() {
        bail!("empty path is not a cache location");
    }
    // Abs/Clean would erase a symlink followed by ".." before the filesystem
    // resolves it, potentially validating a different cache from the build.
    if path.split(paths::is_separator).any(|c| c == "..") {
        bail!("parent traversal is ambiguous in cache paths; use a resolved path without ..");
    }
    let mut p = paths::abs(path)?;
    let mut tail: Vec<String> = Vec::new();
    loop {
        match std::fs::symlink_metadata(&p) {
            Ok(_) => break,
            Err(e) if io_not_found(&e) => {
                let parent = paths::dir(&p);
                if parent == p {
                    return Err(path_error("lstat", &p, e));
                }
                tail.push(paths::base(&p));
                p = parent;
            }
            Err(e) => return Err(path_error("lstat", &p, e)),
        }
    }
    let mut p = paths::eval_symlinks(&p).map_err(|e| path_error("lstat", &p, e))?;
    for t in tail.iter().rev() {
        p = paths::join2(&p, t);
    }
    Ok(paths::clean(&p))
}

/// Lexical containment (`child` equals or is below `parent`).
pub fn contains(parent: &str, child: &str) -> bool {
    paths::lexically_within(parent, child)
}

pub fn overlaps(a: &str, b: &str) -> bool {
    contains(a, b) || contains(b, a)
}

const TRACKED_OVERLAP: &str =
    "cache overlaps tracked or unignored files; configure and ignore a dedicated cache directory";

impl Info {
    /// Protects worktree roots, Git metadata, tracked/unignored files and the
    /// supplied protected paths; returns the canonical cache path.
    pub fn validate_cache(
        &self,
        ctx: &Context,
        path: &str,
        protected: &[String],
    ) -> Result<String> {
        let p = canonical(path)?;
        check_owner(&p)?;
        match std::fs::metadata(&p) {
            Ok(st) if !st.is_dir() => bail!("cache root must be a directory"),
            Ok(_) => {}
            Err(e) if io_not_found(&e) => {}
            Err(e) => return Err(path_error("stat", &p, e)),
        }
        if self.kind == Kind::Bare {
            bail!("cache discovery requires a checkout, not a bare repository");
        }
        let mut guarded = vec![
            self.common_dir.clone(),
            self.git_dir.clone(),
            paths::join2(&self.root, ".git"),
            paths::join2(&self.root, ".ccow"),
        ];
        guarded.extend(protected.iter().cloned());
        for v in &guarded {
            let q = canonical(v)?;
            if overlaps(&p, &q) {
                bail!("cache overlaps protected storage; configure a dedicated cache directory");
            }
        }
        let list = git_string(ctx, &self.root, &["worktree", "list", "--porcelain", "-z"])?;
        for field in list.split('\0') {
            let Some(worktree) = field.strip_prefix("worktree ") else {
                continue;
            };
            let q = canonical(worktree)?;
            if contains(&p, &q) {
                bail!("cache cannot contain a worktree; configure a dedicated cache directory");
            }
            if q == self.common_dir {
                continue; // Bare primary entry has no index or source tree.
            }
            for name in [".git", ".ccow"] {
                let protected_path = canonical(&paths::join2(&q, name))?;
                if overlaps(&p, &protected_path) {
                    bail!("cache overlaps worktree metadata or settings");
                }
            }
            if worktree_missing(&q)? {
                // A missing directory has no files to protect, but a locked
                // worktree can reappear, so its path stays reserved.
                if overlaps(&p, &q) {
                    bail!(
                        "cache is inside a registered worktree that is missing; run `git worktree \
                         prune` or select another cache directory"
                    );
                }
                tracing::debug!(worktree = %q, "skipping missing registered worktree");
                continue;
            }
            let files = git_string(
                ctx,
                &q,
                &[
                    "ls-files",
                    "--cached",
                    "--others",
                    "--exclude-standard",
                    "-z",
                ],
            )?;
            for name in files.split('\0') {
                if name.is_empty() {
                    continue;
                }
                let entry = paths::join2(&q, name);
                if overlaps(&p, &entry) {
                    bail!(TRACKED_OVERLAP);
                }
                let source = source_location(&entry, 0)?;
                if overlaps(&p, &source) {
                    bail!(TRACKED_OVERLAP);
                }
            }
        }
        Ok(p)
    }
}

/// Source entries may be dangling symlinks in unrelated worktrees. Resolve
/// their intended target too, so an absent target inside the selected cache
/// remains protected. Cache claims themselves still use strict [`canonical`].
fn source_location(path: &str, depth: usize) -> Result<String> {
    let err = match canonical(path) {
        Ok(v) => return Ok(v),
        Err(e) => e,
    };
    if !crate::error::is_not_found(&err) || depth >= 40 {
        return Err(err);
    }
    match std::fs::symlink_metadata(path) {
        Ok(m) if m.file_type().is_symlink() => {}
        _ => return Err(err),
    }
    let target = std::fs::read_link(path).map_err(|e| path_error("readlink", path, e))?;
    let mut target = paths::path_string(&target)?;
    if !paths::is_abs(&target) {
        target = paths::join2(&paths::dir(path), &target);
    }
    source_location(&target, depth + 1)
}

#[cfg(test)]
pub(crate) mod tests;
