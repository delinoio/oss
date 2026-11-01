// SPDX-License-Identifier: Apache-2.0
//! Filesystem-only identity and cache-claim verification (Go `verify.go`).
//! Suitable for supervisor hooks, which cannot run subprocesses.

use anyhow::{Result, bail};

use super::{Info, Kind, canonical, check_owner, contains, identity, overlaps, path_error};
use crate::paths;

/// Checks a client's repository identity using filesystem reads only:
/// ownership, Git indirection, linked-worktree backlinks and canonical
/// identity. It does not re-run tool discovery or interpret the Git index.
pub fn verify(r: &Info) -> Result<()> {
    for p in [&r.root, &r.git_dir, &r.common_dir, &r.cwd] {
        let c = canonical(p)?;
        if &c != p {
            bail!("repository claim is not canonical; repeat discovery");
        }
        check_owner(p)?;
    }
    if r.id != identity(&r.git_dir) {
        bail!("repository identity changed; repeat discovery");
    }
    match r.kind {
        Kind::Bare => {
            if r.root != r.git_dir || r.git_dir != r.common_dir {
                bail!("invalid bare repository claim");
            }
        }
        Kind::Checkout => {
            if !contains(&r.root, &r.cwd) {
                bail!("execution directory is outside the claimed checkout");
            }
            if checkout_git_dir(&r.root)? != r.git_dir {
                bail!("checkout Git directory changed; repeat discovery");
            }
        }
    }
    let commondir = paths::join2(&r.git_dir, "commondir");
    let common = match std::fs::read(&commondir) {
        Ok(b) => resolve_git_path(&r.git_dir, &String::from_utf8_lossy(&b))?,
        Err(e) if super::io_not_found(&e) => r.git_dir.clone(),
        Err(e) => return Err(path_error("open", &commondir, e)),
    };
    if common != r.common_dir {
        bail!("Git common directory changed; repeat discovery");
    }
    if r.git_dir != r.common_dir {
        let gitdir = paths::join2(&r.git_dir, "gitdir");
        let b = std::fs::read(&gitdir).map_err(|e| path_error("open", &gitdir, e))?;
        let backlink = resolve_git_path(&r.git_dir, &String::from_utf8_lossy(&b))?;
        let expected = canonical(&paths::join2(&r.root, ".git"))?;
        if backlink != expected {
            bail!("linked-worktree backlink changed; repeat discovery");
        }
    }
    for p in [
        paths::join2(&r.git_dir, "HEAD"),
        paths::join2(&r.common_dir, "config"),
    ] {
        let s = std::fs::symlink_metadata(&p).map_err(|e| path_error("lstat", &p, e))?;
        if !s.file_type().is_file() {
            bail!("Git identity metadata must be regular files");
        }
        check_owner(&p)?;
    }
    if !r.primary.is_empty() {
        let primary = canonical(&r.primary)?;
        if primary != r.primary {
            bail!("primary checkout claim is not canonical");
        }
        check_owner(&primary)?;
        if checkout_git_dir(&primary)? != r.common_dir {
            bail!("primary checkout does not own the common Git directory");
        }
    }
    Ok(())
}

fn resolve_git_path(base: &str, value: &str) -> Result<String> {
    let v = value.strip_suffix('\n').unwrap_or(value);
    let v = v.strip_suffix('\r').unwrap_or(v);
    if v.is_empty() {
        bail!("empty Git metadata path");
    }
    let p = if paths::is_abs(v) {
        v.to_string()
    } else {
        paths::join2(base, v)
    };
    canonical(&p)
}

fn checkout_git_dir(root: &str) -> Result<String> {
    let p = paths::join2(root, ".git");
    let s = std::fs::symlink_metadata(&p).map_err(|e| path_error("lstat", &p, e))?;
    if s.is_dir() {
        return canonical(&p);
    }
    if !s.file_type().is_file() {
        bail!("checkout .git must be a directory or regular Git pointer file");
    }
    check_owner(&p)?;
    let b = std::fs::read(&p).map_err(|e| path_error("open", &p, e))?;
    let text = String::from_utf8_lossy(&b);
    let Some(rest) = text.strip_prefix("gitdir: ") else {
        bail!("invalid checkout Git pointer");
    };
    resolve_git_path(root, rest)
}

/// Rechecks canonical paths, ownership and protected boundaries without
/// starting Git or any tool. Unlike [`Info::validate_cache`], it does not
/// inspect tracked/unignored files or enumerate other worktrees.
pub fn verify_cache_claim(r: &Info, path: &str, protected: &[String]) -> Result<String> {
    verify(r)?;
    if r.kind != Kind::Checkout {
        bail!("cache claim requires a checkout");
    }
    let c = canonical(path)?;
    if c != path {
        bail!("cache claim is not canonical; repeat discovery");
    }
    check_owner(path)?;
    match std::fs::metadata(path) {
        Ok(s) if !s.is_dir() => bail!("cache claim is not a directory"),
        Ok(_) => {}
        Err(e) if super::io_not_found(&e) => {}
        Err(e) => return Err(path_error("stat", path, e)),
    }
    for root in [&r.root, &r.primary] {
        if !root.is_empty() && contains(path, root) {
            bail!("cache claim contains a source checkout");
        }
    }
    let mut guarded = vec![
        r.git_dir.clone(),
        r.common_dir.clone(),
        paths::join2(&r.root, ".git"),
        paths::join2(&r.root, ".ccow"),
    ];
    guarded.extend(protected.iter().cloned());
    for v in &guarded {
        let p = canonical(v)?;
        if overlaps(path, &p) {
            bail!("cache claim overlaps protected storage");
        }
    }
    Ok(c)
}
