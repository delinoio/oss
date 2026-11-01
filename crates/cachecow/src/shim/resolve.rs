// SPDX-License-Identifier: Apache-2.0
//! Per-invocation real-tool resolution that skips CacheCow shims.

use std::{collections::HashSet, fs, io};

use super::{
    Candidate, Resolution, ResolveError, ResolveFailure, ResolveOptions, Tool,
    install::{digest, read_manifest},
};
use crate::paths::{self, FileId};

fn fail(resolution: Resolution, error: impl Into<anyhow::Error>) -> ResolveFailure {
    ResolveFailure {
        resolution,
        error: error.into(),
    }
}

fn executable(meta: &fs::Metadata) -> bool {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        meta.is_file() && meta.permissions().mode() & 0o111 != 0
    }
    #[cfg(windows)]
    {
        meta.is_file()
    }
}

pub fn resolve(tool: Tool, opts: &ResolveOptions) -> Result<Resolution, ResolveFailure> {
    let mut result = Resolution::default();
    let mut excluded: HashSet<String> = HashSet::new();
    let mut identities: Vec<FileId> = Vec::new();
    if !opts.ccow_executable.is_empty() {
        match paths::file_id(&opts.ccow_executable, true) {
            Ok(id) => identities.push(id),
            Err(e) => return Err(fail(result, e)),
        }
        match fs::read(&opts.ccow_executable) {
            Ok(b) => {
                excluded.insert(digest(&b));
            }
            Err(e) => return Err(fail(result, e)),
        }
    }
    if !opts.shim_dir.is_empty() {
        match read_manifest(&opts.shim_dir) {
            Ok(m) => {
                for hashes in m.hashes.values() {
                    excluded.extend(hashes.iter().cloned());
                }
            }
            Err(e) => return Err(fail(result, e)),
        }
    }
    let names: Vec<String> = if cfg!(windows) {
        let ext = if opts.path_ext.is_empty() {
            ".COM;.EXE;.BAT;.CMD"
        } else {
            opts.path_ext.as_str()
        };
        ext.split(';')
            .filter(|e| e.starts_with('.') && !e.contains(['/', '\\']))
            .map(|e| format!("{tool}{e}"))
            .collect()
    } else {
        vec![tool.as_str().to_string()]
    };
    let shim_root = if opts.shim_dir.is_empty() {
        None
    } else {
        paths::eval_symlinks(&opts.shim_dir).ok()
    };
    let mut denied = false;
    for dir in paths::split_list(&opts.path) {
        let dir = if dir.is_empty() { ".".to_string() } else { dir };
        for name in &names {
            let p = match paths::abs(&paths::join2(&dir, name)) {
                Ok(p) => p,
                Err(e) => return Err(fail(result, e)),
            };
            let st = match fs::metadata(&p) {
                Ok(st) => st,
                Err(e) if e.kind() == io::ErrorKind::NotFound => continue,
                Err(e) if e.kind() == io::ErrorKind::PermissionDenied => {
                    denied = true;
                    continue;
                }
                Err(e) => return Err(fail(result, e)),
            };
            let mut c = Candidate {
                path: p.clone(),
                excluded: false,
                executable: executable(&st),
            };
            if paths::file_id(&p, true).is_ok_and(|id| identities.contains(&id)) {
                c.excluded = true;
            }
            if let Some(root) = &shim_root
                && paths::eval_symlinks(&p).is_ok_and(|real| paths::dir(&real) == *root)
            {
                c.excluded = true;
            }
            if c.executable && !c.excluded && !excluded.is_empty() {
                match fs::read(&p) {
                    Ok(b) => c.excluded = excluded.contains(&digest(&b)),
                    Err(e) => return Err(fail(result, e)),
                }
            }
            let (is_excluded, is_executable) = (c.excluded, c.executable);
            result.candidates.push(c);
            if is_excluded {
                continue;
            }
            if !is_executable {
                denied = true;
                continue;
            }
            result.path = p;
            return Ok(result);
        }
    }
    Err(fail(result, ResolveError { tool, denied }))
}
