// SPDX-License-Identifier: Apache-2.0
//! Native component lookup before PnP classification. Callers retain original
//! pathname bytes when the resolved lookup needs no managed translation.

use std::{
    collections::VecDeque,
    ffi::{OsStr, OsString},
    fs, io,
    os::unix::ffi::OsStrExt,
    path::{Component, Path, PathBuf},
};

use crate::{
    diagnostic::{Code, Error, Result},
    graph::Graph,
    view::Translation,
};

#[cfg(target_os = "macos")]
const MAX_SYMLINKS: usize = 32;
#[cfg(not(target_os = "macos"))]
const MAX_SYMLINKS: usize = 40;

#[derive(Clone, Copy, Eq, PartialEq)]
pub enum SymlinkPolicy {
    Allow,
    Reject,
}

pub enum Lookup {
    Resolved {
        path: PathBuf,
        requires_directory: bool,
    },
    NativeFailure {
        path: PathBuf,
        errno: i32,
    },
}

struct ParentTraversal {
    directory: PathBuf,
    remaining: PathBuf,
}

pub struct ResolvedLookup {
    path: PathBuf,
    parents: Vec<ParentTraversal>,
    requires_directory: bool,
    native_failure: Option<Lookup>,
    conflict: bool,
}

impl ResolvedLookup {
    pub fn validate_parents(
        self,
        mut translate: impl FnMut(&Path) -> Result<Translation>,
    ) -> Result<Lookup> {
        for parent in self.parents {
            let (physical, errno) = match translate(&parent.directory) {
                Ok(translation) => {
                    let errno = match fs::metadata(&translation.physical) {
                        Ok(metadata) if metadata.is_dir() => continue,
                        Ok(_) => libc::ENOTDIR,
                        Err(error) => error.raw_os_error().unwrap_or(libc::EIO),
                    };
                    (translation.physical, errno)
                }
                Err(error) if error.code == Code::PnportResolutionFailed => {
                    (parent.directory, libc::ENOENT)
                }
                Err(error) => return Err(error),
            };
            // The prefix must exist as a directory before '..' can remove it.
            // Darwin forwards this failing backing lookup to libc; Linux
            // returns the observed errno before a normalized managed rewrite.
            return Ok(Lookup::NativeFailure {
                path: physical.join("..").join(parent.remaining),
                errno,
            });
        }
        // The kernel cannot reach a later namespace when an earlier '..'
        // traversal already failed. Preserve that native lookup error first.
        if self.conflict {
            return Err(Error::new(
                Code::PnportFilesystemConflict,
                "A physical entry conflicts with the virtual dependency directory.",
            ));
        }
        if let Some(failure) = self.native_failure {
            return Ok(failure);
        }
        if self.requires_directory {
            let translation = match translate(&self.path) {
                Ok(translation) => translation,
                Err(error) if error.code == Code::PnportResolutionFailed => {
                    return Ok(Lookup::NativeFailure {
                        path: self.path.join("."),
                        errno: libc::ENOENT,
                    });
                }
                Err(error) => return Err(error),
            };
            let errno = match fs::metadata(&translation.physical) {
                Ok(metadata) if metadata.is_dir() => None,
                Ok(_) => Some(libc::ENOTDIR),
                Err(error) => Some(error.raw_os_error().unwrap_or(libc::EIO)),
            };
            if let Some(errno) = errno {
                return Ok(Lookup::NativeFailure {
                    path: translation.physical.join("."),
                    errno,
                });
            }
        }
        Ok(Lookup::Resolved {
            path: self.path,
            requires_directory: self.requires_directory,
        })
    }
}

pub fn resolved_lookup(path: &Path, follow_last: bool, graph: &Graph) -> Option<ResolvedLookup> {
    resolved_lookup_with_policy(path, follow_last, graph, SymlinkPolicy::Allow)
}

pub fn resolved_lookup_with_policy(
    path: &Path,
    follow_last: bool,
    graph: &Graph,
    policy: SymlinkPolicy,
) -> Option<ResolvedLookup> {
    let mut remaining: VecDeque<OsString> = path
        .components()
        .map(|part| part.as_os_str().to_os_string())
        .collect();
    let mut resolved = PathBuf::new();
    let mut archive_root: Option<PathBuf> = None;
    let mut parents = Vec::new();
    let mut requires_directory = terminal_directory(path);
    let mut followed = 0;
    while let Some(part) = remaining.pop_front() {
        match Path::new(&part).components().next()? {
            Component::RootDir => {
                resolved = PathBuf::from("/");
                archive_root = None;
            }
            Component::CurDir => {}
            Component::ParentDir => {
                parents.push(ParentTraversal {
                    directory: resolved.clone(),
                    remaining: remaining.iter().collect(),
                });
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
                if name == "node_modules" && graph.check_path_conflicts(&candidate).is_err() {
                    return Some(ResolvedLookup {
                        path: candidate,
                        parents,
                        requires_directory,
                        native_failure: None,
                        conflict: true,
                    });
                }
                match fs::symlink_metadata(&candidate) {
                    Ok(metadata) if metadata.file_type().is_symlink() => {
                        if policy == SymlinkPolicy::Reject {
                            return Some(ResolvedLookup {
                                path: candidate.clone(),
                                parents,
                                requires_directory,
                                conflict: false,
                                native_failure: Some(Lookup::NativeFailure {
                                    path: candidate,
                                    errno: libc::ELOOP,
                                }),
                            });
                        }
                        if !follow_last && remaining.is_empty() && !requires_directory {
                            resolved = candidate;
                            continue;
                        }
                        followed += 1;
                        if followed > MAX_SYMLINKS {
                            return None;
                        }
                        let target = fs::read_link(&candidate).ok()?;
                        if remaining.is_empty() {
                            requires_directory |= terminal_directory(&target);
                        }
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
    Some(ResolvedLookup {
        path: resolved,
        parents,
        requires_directory,
        native_failure: None,
        conflict: false,
    })
}

/// Structural cache creation requires a literal namespace leaf. Keep raw
/// components: Path::components removes '.' and lookup resolves '..'/symlinks.
pub fn structural_cache_root(path: &Path) -> bool {
    path.as_os_str()
        .as_bytes()
        .rsplit(|byte| *byte == b'/')
        .find(|component| !component.is_empty())
        == Some(b"node_modules")
}

fn terminal_directory(path: &Path) -> bool {
    let bytes = path.as_os_str().as_bytes();
    bytes.ends_with(b"/") || bytes.ends_with(b"/.")
}
