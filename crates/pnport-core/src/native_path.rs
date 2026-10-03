// SPDX-License-Identifier: Apache-2.0
//! Native component lookup before PnP classification. Callers retain original
//! pathname bytes when the resolved lookup needs no managed translation.

use std::{
    collections::VecDeque,
    ffi::{OsStr, OsString},
    fs, io,
    path::{Component, Path, PathBuf},
};

use crate::graph::Graph;

#[cfg(target_os = "macos")]
const MAX_SYMLINKS: usize = 32;
#[cfg(not(target_os = "macos"))]
const MAX_SYMLINKS: usize = 40;

pub fn resolved_lookup(path: &Path, follow_last: bool, graph: &Graph) -> Option<PathBuf> {
    let mut remaining: VecDeque<OsString> = path
        .components()
        .map(|part| part.as_os_str().to_os_string())
        .collect();
    let mut resolved = PathBuf::new();
    let mut archive_root: Option<PathBuf> = None;
    let mut followed = 0;
    while let Some(part) = remaining.pop_front() {
        match Path::new(&part).components().next()? {
            Component::RootDir => {
                resolved = PathBuf::from("/");
                archive_root = None;
            }
            Component::CurDir => {}
            Component::ParentDir => {
                resolved.pop();
                if archive_root
                    .as_ref()
                    .is_some_and(|archive| !resolved.starts_with(archive))
                {
                    archive_root = None;
                }
            }
            Component::Normal(name) => {
                let candidate = resolved.join(name);
                match fs::symlink_metadata(&candidate) {
                    Ok(metadata)
                        if metadata.file_type().is_symlink()
                            && (follow_last || !remaining.is_empty()) =>
                    {
                        followed += 1;
                        if followed > MAX_SYMLINKS {
                            return None;
                        }
                        let target = fs::read_link(&candidate).ok()?;
                        let mut expanded: VecDeque<OsString> = target
                            .components()
                            .map(|part| part.as_os_str().to_os_string())
                            .collect();
                        expanded.append(&mut remaining);
                        remaining = expanded;
                    }
                    Ok(metadata) => {
                        let archive_boundary = metadata.is_file()
                            && candidate.extension() == Some(OsStr::new("zip"))
                            && graph.is_location_ancestor(&candidate);
                        // Yarn's registered archive is a regular host file,
                        // while its children belong to the PnP virtual view.
                        // Only a graph-owned ZIP boundary may cross that
                        // otherwise native ENOTDIR result.
                        if !remaining.is_empty() && !metadata.is_dir() && !archive_boundary {
                            return None;
                        }
                        resolved = candidate;
                        if archive_boundary {
                            archive_root = Some(resolved.clone());
                        }
                    }
                    Err(error) if error.kind() == io::ErrorKind::NotFound => {
                        // A virtual node_modules component has no physical
                        // directory; the PnP view resolves it after this walk.
                        resolved = candidate;
                    }
                    Err(error)
                        if error.raw_os_error() == Some(libc::ENOTDIR)
                            && archive_root.is_some() =>
                    {
                        // The host reports ENOTDIR below the ZIP file; the
                        // view resolves these components after this walk.
                        resolved = candidate;
                    }
                    Err(_) => return None,
                }
            }
            Component::Prefix(_) => return None,
        }
    }
    Some(resolved)
}
