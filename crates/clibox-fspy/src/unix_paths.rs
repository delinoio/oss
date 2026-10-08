//! Native final-entry lookup policy shared with portable Unix fixtures.

use std::{
    ffi::OsStr,
    fs,
    os::unix::{ffi::OsStrExt, fs::MetadataExt},
    path::{Path, PathBuf},
};

use crate::record::FileIdentity;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum FinalSymlink {
    Follow,
    NoFollow,
}

fn final_entry(path: &Path, policy: FinalSymlink) -> Option<(&Path, &OsStr)> {
    if policy != FinalSymlink::NoFollow {
        return None;
    }
    let bytes = path.as_os_str().as_bytes();
    let separator = bytes.iter().rposition(|byte| *byte == b'/')?;
    let name = &bytes[separator + 1..];
    // Path::components drops trailing separators and dots. Those native
    // lookups still traverse the preceding link, so inspect the raw bytes.
    if matches!(name, b"" | b"." | b"..") {
        return None;
    }
    Some((
        Path::new(OsStr::from_bytes(&bytes[..=separator])),
        OsStr::from_bytes(name),
    ))
}

pub(crate) fn resolve_final_component<E>(
    logical: &Path,
    policy: FinalSymlink,
    resolve: impl FnOnce(&Path) -> Result<Option<PathBuf>, E>,
) -> Result<Option<PathBuf>, E> {
    match final_entry(logical, policy) {
        Some((parent, name)) => {
            // Canonicalizing the parent can succeed without search permission
            // on that final directory. Probe the native nofollow entry lookup
            // before appending the leaf; otherwise an inaccessible ancestor
            // would acquire guessed project containment. This never follows
            // the final link, including a dangling or self-referencing link.
            if fs::symlink_metadata(logical)
                .is_err_and(|error| error.kind() == std::io::ErrorKind::PermissionDenied)
            {
                return Ok(None);
            }
            resolve(parent).map(|parent| parent.map(|parent| parent.join(name)))
        }
        None => resolve(logical),
    }
}

pub(crate) fn path_identity(logical: &Path, policy: FinalSymlink) -> Option<FileIdentity> {
    if final_entry(logical, policy).is_some() {
        fs::symlink_metadata(logical)
            .ok()
            .map(|metadata| FileIdentity::Inode {
                device: metadata.dev(),
                inode: metadata.ino(),
            })
    } else {
        file_id::get_file_id(logical).ok().map(FileIdentity::from)
    }
}

#[cfg(test)]
mod tests {
    use std::{io, os::unix::fs::symlink};

    use super::*;

    #[test]
    fn nofollow_inaccessible_parent_does_not_publish_guessed_entry() {
        use std::{cell::Cell, os::unix::fs::PermissionsExt};
        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().canonicalize().unwrap();
        let hidden = root.join("hidden");
        fs::create_dir(&hidden).unwrap();
        symlink("missing", hidden.join("link")).unwrap();
        fs::set_permissions(&hidden, fs::Permissions::from_mode(0o000)).unwrap();
        let logical = hidden.join("link");
        let denied = fs::symlink_metadata(&logical)
            .is_err_and(|error| error.kind() == io::ErrorKind::PermissionDenied);
        let parent_resolved = Cell::new(false);
        let observed = resolve_final_component(&logical, FinalSymlink::NoFollow, |parent| {
            parent_resolved.set(true);
            fs::canonicalize(parent).map(Some)
        });
        // Restore fixture cleanup access before assertions, including failure.
        fs::set_permissions(&hidden, fs::Permissions::from_mode(0o700)).unwrap();
        if denied {
            assert!(observed.unwrap().is_none());
            assert!(!parent_resolved.get());
        }
        let observed = resolve_final_component(&logical, FinalSymlink::NoFollow, |parent| {
            fs::canonicalize(parent).map(Some)
        })
        .unwrap();
        assert_eq!(observed, Some(logical.clone()));
        assert!(path_identity(&logical, FinalSymlink::NoFollow).is_some());
    }

    #[test]
    fn nofollow_preserves_entry_identity_without_reading_targets() {
        let directory = tempfile::tempdir().unwrap();
        let base = directory.path().canonicalize().unwrap();
        let root = base.join("root");
        fs::create_dir(&root).unwrap();
        fs::write(root.join("inside"), b"inside").unwrap();
        fs::write(base.join("outside"), b"outside").unwrap();
        for (name, target) in [
            ("internal", root.join("inside")),
            ("external", base.join("outside")),
            ("self", root.join("self")),
            ("dangling", base.join("missing")),
        ] {
            let logical = root.join(name);
            symlink(target, &logical).unwrap();
            let resolved = resolve_final_component(&logical, FinalSymlink::NoFollow, |path| {
                fs::canonicalize(path).map(Some)
            })
            .unwrap()
            .unwrap();
            assert_eq!(resolved, logical);
            let metadata = fs::symlink_metadata(&logical).unwrap();
            assert_eq!(
                path_identity(&logical, FinalSymlink::NoFollow),
                Some(FileIdentity::Inode {
                    device: metadata.dev(),
                    inode: metadata.ino()
                })
            );
        }
        let external = root.join("external");
        assert_eq!(
            resolve_final_component(&external, FinalSymlink::Follow, |path| fs::canonicalize(
                path
            )
            .map(Some))
            .unwrap(),
            Some(base.join("outside"))
        );
        assert_ne!(
            path_identity(&external, FinalSymlink::NoFollow),
            path_identity(&external, FinalSymlink::Follow)
        );
    }

    #[test]
    fn nofollow_resolves_ancestors_and_retains_native_terminal_lookup() {
        let directory = tempfile::tempdir().unwrap();
        let base = directory.path().canonicalize().unwrap();
        let root = base.join("root");
        let outside = base.join("outside");
        fs::create_dir(&root).unwrap();
        fs::create_dir(&outside).unwrap();
        fs::write(outside.join("leaf"), b"outside").unwrap();
        symlink(&outside, root.join("ancestor")).unwrap();
        for suffix in ["ancestor/leaf", "ancestor/", "ancestor/.", "ancestor/.."] {
            let logical = root.join(suffix);
            let resolved = resolve_final_component(&logical, FinalSymlink::NoFollow, |path| {
                fs::canonicalize(path).map(Some)
            })
            .unwrap()
            .unwrap();
            assert_eq!(resolved, fs::canonicalize(&logical).unwrap(), "{suffix}");
        }
        let logical = root.join("missing/leaf");
        assert!(
            resolve_final_component(&logical, FinalSymlink::NoFollow, |_| Ok::<_, io::Error>(
                None
            ))
            .unwrap()
            .is_none()
        );
        assert!(
            resolve_final_component(&logical, FinalSymlink::NoFollow, |_| Err::<
                Option<PathBuf>,
                _,
            >(
                io::Error::from(io::ErrorKind::PermissionDenied)
            ))
            .is_err()
        );
    }
}
