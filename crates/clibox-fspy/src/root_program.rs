//! Bounded Linux executable selection, with injectable access evidence for
//! tests.

use std::{
    env,
    ffi::OsString,
    os::unix::ffi::OsStrExt,
    path::{Path, PathBuf},
    process::Command,
};

#[derive(Debug)]
pub(crate) struct RootProgram {
    pub selected: PathBuf,
    pub failed_candidates: Vec<(PathBuf, i32)>,
}

#[derive(Debug, PartialEq, Eq)]
pub(crate) enum ResolveFailure {
    Access(i32),
    Limit,
}

#[cfg(target_os = "linux")]
pub(crate) fn execution_access(path: &Path) -> Result<(), i32> {
    let metadata =
        std::fs::metadata(path).map_err(|error| error.raw_os_error().unwrap_or(libc::EIO))?;
    if !metadata.is_file() {
        return Err(libc::EACCES);
    }
    let path = std::ffi::CString::new(path.as_os_str().as_bytes()).map_err(|_| libc::EINVAL)?;
    // SAFETY: the kernel reads this live NUL-terminated path only during the
    // call. faccessat2 checks effective IDs, ACLs and mount policy in the
    // kernel. Do not fall back to mode bits or libc's older ACL-blind
    // emulation when the syscall is unavailable: uncertain admission must
    // not launch user code.
    let result = unsafe {
        libc::syscall(
            libc::SYS_faccessat2,
            libc::AT_FDCWD,
            path.as_ptr(),
            libc::X_OK,
            libc::AT_EACCESS,
        )
    };
    if result == 0 {
        Ok(())
    } else {
        Err(std::io::Error::last_os_error()
            .raw_os_error()
            .unwrap_or(libc::EIO))
    }
}

pub(crate) fn resolve(
    command: &Command,
    max_failed: usize,
    mut access: impl FnMut(&Path) -> Result<(), i32>,
) -> Result<RootProgram, ResolveFailure> {
    let cwd = command.get_current_dir().unwrap_or_else(|| Path::new("."));
    let cwd = std::path::absolute(cwd)
        .map_err(|error| ResolveFailure::Access(error.raw_os_error().unwrap_or(libc::EIO)))?;
    let program = Path::new(command.get_program());
    if program.as_os_str().as_bytes().contains(&b'/') {
        let selected = if program.is_absolute() {
            program.to_path_buf()
        } else {
            cwd.join(program)
        };
        access(&selected).map_err(ResolveFailure::Access)?;
        return Ok(RootProgram {
            selected,
            failed_candidates: Vec::new(),
        });
    }
    let path_env = command
        .get_envs()
        .find(|(key, _)| *key == "PATH")
        .map(|(_, value)| value.map(OsString::from))
        .unwrap_or_else(|| env::var_os("PATH"))
        .unwrap_or_else(|| OsString::from("/bin:/usr/bin"));
    let mut failed_candidates = Vec::new();
    let mut denied = false;
    for directory in env::split_paths(&path_env) {
        let base = if directory.is_absolute() {
            directory
        } else {
            cwd.join(directory)
        };
        let candidate = base.join(program);
        let error = match access(&candidate) {
            Ok(()) => {
                return Ok(RootProgram {
                    selected: candidate,
                    failed_candidates,
                })
            }
            Err(error) => error,
        };
        // Match exec PATH search: denied and absent candidates permit a later
        // image; unexpected access errors stop admission rather than guessing.
        if !matches!(error, libc::EACCES | libc::ENOENT | libc::ENOTDIR) {
            return Err(ResolveFailure::Access(error));
        }
        if failed_candidates.len() >= max_failed {
            return Err(ResolveFailure::Limit);
        }
        denied |= error == libc::EACCES;
        failed_candidates.push((candidate, error));
    }
    Err(ResolveFailure::Access(if denied {
        libc::EACCES
    } else {
        libc::ENOENT
    }))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn command(path: &str) -> Command {
        let mut command = Command::new("tool");
        command
            .current_dir("/fixture")
            .env("PATH", path)
            .arg("original argument");
        command
    }

    #[test]
    fn denied_and_missing_candidates_preserve_errno_before_valid_image() {
        let command = command("denied:missing:not-directory:valid");
        let selected = resolve(&command, 3, |path| {
            match path
                .parent()
                .unwrap()
                .file_name()
                .unwrap()
                .to_str()
                .unwrap()
            {
                "denied" => Err(libc::EACCES),
                "missing" => Err(libc::ENOENT),
                "not-directory" => Err(libc::ENOTDIR),
                "valid" => Ok(()),
                _ => panic!("unexpected candidate"),
            }
        })
        .unwrap();
        assert_eq!(selected.selected, Path::new("/fixture/valid/tool"));
        assert_eq!(
            selected.failed_candidates,
            vec![
                (PathBuf::from("/fixture/denied/tool"), libc::EACCES),
                (PathBuf::from("/fixture/missing/tool"), libc::ENOENT),
                (PathBuf::from("/fixture/not-directory/tool"), libc::ENOTDIR),
            ]
        );
        assert_eq!(command.get_program(), "tool");
        assert_eq!(
            command.get_args().collect::<Vec<_>>(),
            ["original argument"]
        );
    }

    #[test]
    fn unexpected_or_unavailable_evidence_stops_before_later_image() {
        for error in [
            libc::EIO,
            libc::ENOSYS,
            libc::EINVAL,
            libc::EPERM,
            libc::ELOOP,
        ] {
            let mut calls = 0;
            assert_eq!(
                resolve(&command("first:valid"), 3, |_| {
                    calls += 1;
                    Err(error)
                })
                .unwrap_err(),
                ResolveFailure::Access(error)
            );
            assert_eq!(calls, 1);
        }
    }

    #[test]
    fn all_denied_and_missing_searches_keep_permission_precedence() {
        assert_eq!(
            resolve(&command("a:b"), 2, |_| Err(libc::EACCES)).unwrap_err(),
            ResolveFailure::Access(libc::EACCES)
        );
        let mut first = true;
        assert_eq!(
            resolve(&command("a:b"), 2, |_| {
                let error = if first { libc::EACCES } else { libc::ENOENT };
                first = false;
                Err(error)
            })
            .unwrap_err(),
            ResolveFailure::Access(libc::EACCES)
        );
        assert_eq!(
            resolve(&command("a:b"), 2, |_| Err(libc::ENOENT)).unwrap_err(),
            ResolveFailure::Access(libc::ENOENT)
        );
    }

    #[test]
    fn explicit_paths_do_not_search_and_limits_bound_failed_pairs() {
        for program in ["./tool", "/absolute/tool"] {
            let mut command = Command::new(program);
            command.current_dir("/fixture").env("PATH", "valid");
            let mut calls = Vec::new();
            assert_eq!(
                resolve(&command, 0, |path| {
                    calls.push(path.to_path_buf());
                    Err(libc::EACCES)
                })
                .unwrap_err(),
                ResolveFailure::Access(libc::EACCES)
            );
            assert_eq!(calls.len(), 1);
            assert_eq!(
                calls[0],
                if program.starts_with('/') {
                    PathBuf::from(program)
                } else {
                    Path::new("/fixture").join(program)
                }
            );
        }
        assert_eq!(
            resolve(&command("a:b:valid"), 1, |_| Err(libc::ENOENT)).unwrap_err(),
            ResolveFailure::Limit
        );
        assert!(resolve(&command("valid"), 0, |_| Ok(()))
            .unwrap()
            .failed_candidates
            .is_empty());
    }

    #[test]
    fn empty_relative_absolute_and_removed_path_controls() {
        let selected = resolve(&command(":relative:/absolute"), 3, |path| {
            if path == Path::new("/absolute/tool") {
                Ok(())
            } else {
                Err(libc::ENOENT)
            }
        })
        .unwrap();
        assert_eq!(
            selected
                .failed_candidates
                .iter()
                .map(|(path, _)| path.clone())
                .collect::<Vec<_>>(),
            [
                PathBuf::from("/fixture/tool"),
                PathBuf::from("/fixture/relative/tool")
            ]
        );
        let mut command = command("ignored");
        command.env_remove("PATH");
        assert_eq!(
            resolve(&command, 0, |_| Ok(())).unwrap().selected,
            Path::new("/bin/tool")
        );
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn native_access_uses_effective_owner_permissions() {
        use std::os::unix::fs::PermissionsExt;
        // Root may execute the other-execute fixture; the regression requires
        // an unprivileged effective user and makes no privilege changes.
        if unsafe { libc::geteuid() } == 0 {
            return;
        }
        let directory = tempfile::tempdir().unwrap();
        let denied = directory.path().join("denied");
        std::fs::write(&denied, b"never executed").unwrap();
        std::fs::set_permissions(&denied, std::fs::Permissions::from_mode(0o641)).unwrap();
        assert_eq!(execution_access(&denied), Err(libc::EACCES));
        std::fs::set_permissions(&denied, std::fs::Permissions::from_mode(0o755)).unwrap();
        assert_eq!(execution_access(&denied), Ok(()));
        assert_eq!(execution_access(directory.path()), Err(libc::EACCES));
    }
}
