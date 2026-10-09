//! Decode selected synchronous file syscall path arguments while a tracee is
//! stopped.

use std::{
    ffi::OsString,
    fs, io,
    os::unix::ffi::{OsStrExt, OsStringExt},
    path::{Component, Path, PathBuf},
};

use libc::{c_void, iovec};

use super::{supervision, RawEntry, TraceFailure};
use crate::{
    record::{AccessPath, FileIdentity, NativePath, Operation, PathClass},
    unix_paths::{path_identity, resolve_final_component, FinalSymlink},
};

const MAX_PATH_BYTES: usize = 4096;

#[derive(Debug, Clone)]
pub struct DecodedOperation {
    pub operation: Operation,
    pub open_mutates: bool,
    pub paths: Vec<AccessPath>,
    pub path_unavailable: bool,
    pub descriptor: Option<i32>,
}

fn open_flags(entry: &RawEntry) -> Option<u64> {
    #[cfg(target_arch = "x86_64")]
    if entry.syscall == libc::SYS_creat as u64 {
        return None;
    }
    let flags = if entry.syscall == libc::SYS_openat2 as u64 {
        if entry.args[3] < 8 || entry.args[2] == 0 {
            return None;
        }
        let mut flags = 0_u64;
        let local = iovec {
            iov_base: (&raw mut flags).cast::<c_void>(),
            iov_len: 8,
        };
        let remote = iovec {
            iov_base: entry.args[2] as usize as *mut c_void,
            iov_len: 8,
        };
        // SAFETY: the tracee is stopped and both vectors cover eight bytes.
        if unsafe {
            libc::process_vm_readv(
                entry.tid as i32,
                &raw const local,
                1,
                &raw const remote,
                1,
                0,
            )
        } != 8
        {
            return None;
        }
        flags
    } else if entry.syscall == libc::SYS_openat as u64 {
        entry.args[2]
    } else {
        entry.args[1]
    };
    Some(flags)
}

fn open_mutates(entry: &RawEntry) -> bool {
    open_flags(entry).is_none_or(|flags| {
        flags & (libc::O_CREAT as u64 | libc::O_TRUNC as u64) != 0
            || flags & libc::O_TMPFILE as u64 == libc::O_TMPFILE as u64
    })
}

#[derive(Clone, Copy)]
enum Source {
    Path(usize),
    At(usize, usize),
    Descriptor(usize),
}

// Policy belongs to each native pathname argument, rather than the broad
// operation category: linkat can follow its source while retaining its target.
fn final_symlink(entry: &RawEntry, source: Source) -> FinalSymlink {
    use FinalSymlink::{Follow, NoFollow};
    let number = entry.syscall;
    let first = matches!(source, Source::Path(0) | Source::At(0, 1));
    if matches!(source, Source::Descriptor(_)) {
        return Follow;
    }
    if number == libc::SYS_readlinkat as u64
        || number == libc::SYS_unlinkat as u64
        || number == libc::SYS_renameat as u64
        || number == libc::SYS_renameat2 as u64
        || number == libc::SYS_symlinkat as u64
        || number == libc::SYS_mkdirat as u64
        || number == libc::SYS_mknodat as u64
    {
        return NoFollow;
    }
    if number == libc::SYS_linkat as u64 {
        return if first && entry.args[4] & libc::AT_SYMLINK_FOLLOW as u64 != 0 {
            Follow
        } else {
            NoFollow
        };
    }
    let flags = if number == libc::SYS_statx as u64 {
        entry.args[2]
    } else if number == libc::SYS_newfstatat as u64
        || number == libc::SYS_faccessat2 as u64
        || number == libc::SYS_utimensat as u64
    {
        entry.args[3]
    } else if number == libc::SYS_fchownat as u64 || number == libc::SYS_execveat as u64 {
        entry.args[4]
    } else {
        0
    };
    if flags & libc::AT_SYMLINK_NOFOLLOW as u64 != 0 {
        return NoFollow;
    }
    if number == libc::SYS_openat as u64 || number == libc::SYS_openat2 as u64 {
        return if open_flags(entry).is_some_and(|flags| flags & libc::O_NOFOLLOW as u64 != 0) {
            NoFollow
        } else {
            Follow
        };
    }
    #[cfg(target_arch = "x86_64")]
    {
        if number == libc::SYS_lstat as u64
            || number == libc::SYS_readlink as u64
            || number == libc::SYS_unlink as u64
            || number == libc::SYS_rmdir as u64
            || number == libc::SYS_rename as u64
            || number == libc::SYS_link as u64
            || number == libc::SYS_symlink as u64
            || number == libc::SYS_mkdir as u64
        {
            return NoFollow;
        }
        if number == libc::SYS_open as u64
            && open_flags(entry).is_some_and(|flags| flags & libc::O_NOFOLLOW as u64 != 0)
        {
            return NoFollow;
        }
    }
    Follow
}

fn spec(number: u64) -> Option<(Operation, Source, Option<Source>)> {
    use Operation as Op;
    use Source as Src;

    if number == libc::SYS_read as u64 || number == libc::SYS_readv as u64 {
        return Some((Op::Read, Src::Descriptor(0), None));
    }
    if number == libc::SYS_pread64 as u64
        || number == libc::SYS_preadv as u64
        || number == libc::SYS_preadv2 as u64
    {
        return Some((Op::PositionalRead, Src::Descriptor(0), None));
    }
    if number == libc::SYS_write as u64 || number == libc::SYS_writev as u64 {
        return Some((Op::Write, Src::Descriptor(0), None));
    }
    if number == libc::SYS_pwrite64 as u64
        || number == libc::SYS_pwritev as u64
        || number == libc::SYS_pwritev2 as u64
    {
        return Some((Op::PositionalWrite, Src::Descriptor(0), None));
    }
    if number == libc::SYS_openat as u64 || number == libc::SYS_openat2 as u64 {
        return Some((Op::Open, Src::At(0, 1), None));
    }
    if number == libc::SYS_close as u64 {
        return Some((Op::Close, Src::Descriptor(0), None));
    }
    if number == libc::SYS_newfstatat as u64
        || number == libc::SYS_statx as u64
        || number == libc::SYS_faccessat as u64
        || number == libc::SYS_faccessat2 as u64
        || number == libc::SYS_readlinkat as u64
    {
        return Some((Op::Metadata, Src::At(0, 1), None));
    }
    if number == libc::SYS_fstat as u64 || number == libc::SYS_fchdir as u64 {
        return Some((Op::Metadata, Src::Descriptor(0), None));
    }
    if number == libc::SYS_chdir as u64 {
        return Some((Op::Metadata, Src::Path(0), None));
    }
    if number == libc::SYS_getdents64 as u64 {
        return Some((Op::Directory, Src::Descriptor(0), None));
    }
    if number == libc::SYS_unlinkat as u64
        || number == libc::SYS_mkdirat as u64
        || number == libc::SYS_fchmodat as u64
        || number == libc::SYS_fchownat as u64
        || number == libc::SYS_utimensat as u64
        || number == libc::SYS_mknodat as u64
    {
        return Some((Op::Mutation, Src::At(0, 1), None));
    }
    if number == libc::SYS_renameat as u64 || number == libc::SYS_renameat2 as u64 {
        return Some((Op::Mutation, Src::At(0, 1), Some(Src::At(2, 3))));
    }
    if number == libc::SYS_linkat as u64 {
        return Some((Op::Mutation, Src::At(0, 1), Some(Src::At(2, 3))));
    }
    if number == libc::SYS_symlinkat as u64 {
        // The first argument is text stored in the symlink, not a source
        // pathname that the kernel accesses.
        return Some((Op::Mutation, Src::At(1, 2), None));
    }
    if number == libc::SYS_ftruncate as u64 {
        return Some((Op::Mutation, Src::Descriptor(0), None));
    }
    if number == libc::SYS_truncate as u64 {
        return Some((Op::Mutation, Src::Path(0), None));
    }
    if number == libc::SYS_execve as u64 {
        return Some((Op::Exec, Src::Path(0), None));
    }
    if number == libc::SYS_execveat as u64 {
        return Some((Op::Exec, Src::At(0, 1), None));
    }
    x64_spec(number)
}

#[cfg(target_arch = "x86_64")]
fn x64_spec(number: u64) -> Option<(Operation, Source, Option<Source>)> {
    use Operation as Op;
    use Source as Src;
    if number == libc::SYS_open as u64 || number == libc::SYS_creat as u64 {
        return Some((Op::Open, Src::Path(0), None));
    }
    if number == libc::SYS_stat as u64
        || number == libc::SYS_lstat as u64
        || number == libc::SYS_access as u64
        || number == libc::SYS_readlink as u64
    {
        return Some((Op::Metadata, Src::Path(0), None));
    }
    if number == libc::SYS_getdents as u64 {
        return Some((Op::Directory, Src::Descriptor(0), None));
    }
    if number == libc::SYS_unlink as u64
        || number == libc::SYS_mkdir as u64
        || number == libc::SYS_rmdir as u64
        || number == libc::SYS_chmod as u64
        || number == libc::SYS_chown as u64
    {
        return Some((Op::Mutation, Src::Path(0), None));
    }
    if number == libc::SYS_rename as u64 || number == libc::SYS_link as u64 {
        return Some((Op::Mutation, Src::Path(0), Some(Src::Path(1))));
    }
    if number == libc::SYS_symlink as u64 {
        return Some((Op::Mutation, Src::Path(1), None));
    }
    None
}

#[cfg(target_arch = "aarch64")]
fn x64_spec(_: u64) -> Option<(Operation, Source, Option<Source>)> {
    None
}

fn read_path(tid: u32, pointer: u64) -> Result<Option<PathBuf>, TraceFailure> {
    if pointer == 0 {
        return Ok(None);
    }
    let mut bytes = [0_u8; MAX_PATH_BYTES];
    let local = iovec {
        iov_base: bytes.as_mut_ptr().cast::<c_void>(),
        iov_len: bytes.len(),
    };
    let remote = iovec {
        iov_base: pointer as usize as *mut c_void,
        iov_len: bytes.len(),
    };
    // SAFETY: process_vm_readv copies at most one bounded local buffer from
    // the ptrace-stopped tracee. Invalid remote pointers return an error.
    let count =
        unsafe { libc::process_vm_readv(tid as i32, &raw const local, 1, &raw const remote, 1, 0) };
    if count <= 0 {
        if io::Error::last_os_error().raw_os_error() == Some(libc::EFAULT) {
            return Ok(None);
        }
        return Err(supervision("path_memory_read"));
    }
    let Some(length) = bytes[..count as usize].iter().position(|byte| *byte == 0) else {
        // A path crossing an unreadable page or exceeding PATH_MAX remains an
        // observed failed operation, without inventing pathname bytes.
        return Ok(None);
    };
    Ok(Some(PathBuf::from(OsString::from_vec(
        bytes[..length].to_vec(),
    ))))
}

fn descriptor_path(
    tid: u32,
    descriptor: i32,
) -> Result<Option<(PathBuf, Option<FileIdentity>)>, TraceFailure> {
    let proc_path = format!("/proc/{tid}/fd/{descriptor}");
    let path = match fs::read_link(&proc_path) {
        Ok(path) => path,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(_) => return Err(supervision("descriptor_path_read")),
    };
    if !path.is_absolute() {
        // Pipes, sockets, and anonymous inodes are outside file operations.
        return Ok(None);
    }
    let identity = file_id::get_file_id(&proc_path)
        .map(FileIdentity::from)
        .map_err(|_| supervision("descriptor_identity"))?;
    Ok(Some((path, Some(identity))))
}

fn base_path(tid: u32, descriptor: i32) -> Result<Option<PathBuf>, TraceFailure> {
    if descriptor == libc::AT_FDCWD {
        fs::read_link(format!("/proc/{tid}/cwd"))
            .map(Some)
            .map_err(|_| supervision("cwd_read"))
    } else {
        Ok(descriptor_path(tid, descriptor)?.map(|(path, _)| path))
    }
}

fn empty_descriptor_path(
    tid: u32,
    descriptor: i32,
) -> Result<Option<(PathBuf, Option<FileIdentity>)>, TraceFailure> {
    if descriptor != libc::AT_FDCWD {
        return descriptor_path(tid, descriptor);
    }
    let proc_path = format!("/proc/{tid}/cwd");
    let path = fs::read_link(&proc_path).map_err(|_| supervision("cwd_read"))?;
    let identity = file_id::get_file_id(&proc_path)
        .map(FileIdentity::from)
        .map_err(|_| supervision("cwd_identity"))?;
    Ok(Some((path, Some(identity))))
}

fn empty_path_uses_descriptor(entry: &RawEntry, path_index: usize) -> bool {
    if path_index != 1 {
        return false;
    }
    let flags = if entry.syscall == libc::SYS_statx as u64 {
        entry.args[2]
    } else if entry.syscall == libc::SYS_newfstatat as u64
        || entry.syscall == libc::SYS_faccessat2 as u64
    {
        entry.args[3]
    } else if entry.syscall == libc::SYS_execveat as u64 || entry.syscall == libc::SYS_linkat as u64
    {
        entry.args[4]
    } else {
        return false;
    };
    flags & libc::AT_EMPTY_PATH as u64 != 0
}

fn absolute_path(
    tid: u32,
    path: PathBuf,
    descriptor: i32,
) -> Result<Option<PathBuf>, TraceFailure> {
    if path.is_absolute() {
        Ok(Some(path))
    } else {
        Ok(base_path(tid, descriptor)?.map(|base| base.join(path)))
    }
}

fn resolve_even_if_absent(path: &Path) -> Result<Option<PathBuf>, TraceFailure> {
    let mut cursor = path;
    let mut tail = Vec::<OsString>::new();
    loop {
        match fs::canonicalize(cursor) {
            Ok(mut resolved) => {
                for component in tail.iter().rev() {
                    resolved.push(component);
                }
                return Ok(Some(lexical_normalize(&resolved)));
            }
            Err(error)
                if matches!(
                    error.kind(),
                    io::ErrorKind::NotFound | io::ErrorKind::NotADirectory
                ) =>
            {
                let component = cursor
                    .components()
                    .next_back()
                    .ok_or_else(|| supervision("missing_path_ancestor"))?;
                tail.push(component.as_os_str().to_os_string());
                cursor = cursor
                    .parent()
                    .ok_or_else(|| supervision("missing_path_parent"))?;
            }
            Err(error) if error.kind() == io::ErrorKind::PermissionDenied => {
                return Ok(None);
            }
            Err(error) => {
                use std::io::Write;
                let _ = writeln!(
                    io::stderr(),
                    "clibox fspy tracer: stage=path_resolution_error kind={:?} os_code={:?}",
                    error.kind(),
                    error.raw_os_error()
                );
                return Err(supervision("path_resolution"));
            }
        }
    }
}

fn lexical_normalize(path: &Path) -> PathBuf {
    let mut normalized = PathBuf::new();
    for component in path.components() {
        match component {
            Component::ParentDir => {
                normalized.pop();
            }
            Component::CurDir => {}
            Component::RootDir | Component::Normal(_) | Component::Prefix(_) => {
                normalized.push(component.as_os_str());
            }
        }
    }
    normalized
}

fn access_path(
    root: &Path,
    logical: PathBuf,
    identity: Option<FileIdentity>,
    policy: FinalSymlink,
) -> Result<Option<AccessPath>, TraceFailure> {
    let Some(resolved) = resolve_final_component(&logical, policy, resolve_even_if_absent)? else {
        return Ok(None);
    };
    let project_relative = resolved.strip_prefix(root).ok();
    Ok(Some(AccessPath {
        class: if project_relative.is_some() {
            PathClass::Project
        } else {
            PathClass::External
        },
        logical: NativePath::UnixBytes(logical.as_os_str().as_bytes().to_vec()),
        resolved: Some(NativePath::UnixBytes(
            resolved.as_os_str().as_bytes().to_vec(),
        )),
        project_relative: project_relative.map(|relative| {
            let bytes = relative.as_os_str().as_bytes();
            NativePath::UnixBytes(if bytes.is_empty() {
                b".".to_vec()
            } else {
                bytes.to_vec()
            })
        }),
        identity: identity.or_else(|| path_identity(&logical, policy)),
    }))
}

fn from_source(
    entry: &RawEntry,
    root: &Path,
    source: Source,
) -> Result<(Option<AccessPath>, bool), TraceFailure> {
    let (path, identity) = match source {
        Source::Path(index) => (
            read_path(entry.tid, entry.args[index])?
                .filter(|path| !path.as_os_str().is_empty())
                .map(|path| absolute_path(entry.tid, path, libc::AT_FDCWD))
                .transpose()?
                .flatten(),
            None,
        ),
        Source::At(dir_index, path_index) => match read_path(entry.tid, entry.args[path_index])? {
            Some(path) if path.as_os_str().is_empty() => {
                if empty_path_uses_descriptor(entry, path_index) {
                    let Some((path, identity)) =
                        empty_descriptor_path(entry.tid, entry.args[dir_index] as i32)?
                    else {
                        return Ok((None, true));
                    };
                    (Some(path), identity)
                } else {
                    (None, None)
                }
            }
            Some(path) => (
                absolute_path(entry.tid, path, entry.args[dir_index] as i32)?,
                None,
            ),
            None => (None, None),
        },
        Source::Descriptor(index) => {
            let descriptor = entry.args[index] as i32;
            let Some((path, identity)) = descriptor_path(entry.tid, descriptor)? else {
                return Ok((None, false));
            };
            (Some(path), identity)
        }
    };
    let Some(path) = path else {
        return Ok((None, !matches!(source, Source::Descriptor(_))));
    };
    let classified = access_path(root, path, identity, final_symlink(entry, source))?;
    let unavailable = classified.is_none();
    Ok((classified, unavailable))
}

/// Decode a selected file syscall at entry, before the tracee resumes. A
/// successful return guarantees only that the arguments were observed; the
/// caller must still pair the native completion and enforce full coverage.
pub fn decode(entry: &RawEntry, root: &Path) -> Result<Option<DecodedOperation>, TraceFailure> {
    let Some((operation, first, second)) = spec(entry.syscall) else {
        return Ok(None);
    };
    let descriptor = match first {
        Source::Descriptor(index) => Some(entry.args[index] as i32),
        _ => None,
    };
    let mut paths = Vec::with_capacity(2);
    let (first_path, first_unavailable) = from_source(entry, root, first)?;
    if let Some(path) = first_path {
        paths.push(path);
    }
    let mut path_unavailable = first_unavailable;
    if let Some(second) = second {
        let (second_path, second_unavailable) = from_source(entry, root, second)?;
        path_unavailable |= second_unavailable;
        if let Some(path) = second_path {
            paths.push(path);
        }
    }
    if paths.is_empty() && descriptor.is_none() && !path_unavailable {
        return Err(supervision("missing_operation_paths"));
    }
    Ok(Some(DecodedOperation {
        operation,
        open_mutates: operation == Operation::Open && open_mutates(entry),
        paths,
        path_unavailable,
        descriptor,
    }))
}

#[cfg(test)]
mod tests {
    use std::os::{fd::AsRawFd, unix::fs::symlink};

    use super::*;

    fn entry(number: i64, args: [u64; 6]) -> RawEntry {
        RawEntry {
            ordinal: 1,
            pid: std::process::id(),
            tid: std::process::id(),
            parent_pid: None,
            syscall: number as u64,
            args,
            monotonic_ns: 1,
        }
    }

    #[test]
    fn nofollow_metadata_handlers_retain_each_native_entry() {
        use std::ffi::CString;
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
            let path = CString::new(logical.as_os_str().as_bytes()).unwrap();
            let pointer = path.as_ptr() as u64;
            let at = libc::AT_FDCWD as u64;
            let flag = libc::AT_SYMLINK_NOFOLLOW as u64;
            let mut cases = vec![
                entry(libc::SYS_readlinkat, [at, pointer, 0, 0, 0, 0]),
                entry(libc::SYS_newfstatat, [at, pointer, 0, flag, 0, 0]),
                entry(libc::SYS_statx, [at, pointer, flag, 0, 0, 0]),
                entry(libc::SYS_faccessat2, [at, pointer, 0, flag, 0, 0]),
                entry(libc::SYS_fchownat, [at, pointer, 0, 0, flag, 0]),
                entry(libc::SYS_utimensat, [at, pointer, 0, flag, 0, 0]),
            ];
            #[cfg(target_arch = "x86_64")]
            cases.extend([
                entry(libc::SYS_lstat, [pointer, 0, 0, 0, 0, 0]),
                entry(libc::SYS_readlink, [pointer, 0, 0, 0, 0, 0]),
            ]);
            for case in cases {
                let decoded = decode(&case, &root).unwrap().unwrap();
                assert!(!decoded.path_unavailable);
                assert_eq!(decoded.paths.len(), 1);
                let observed = &decoded.paths[0];
                assert_eq!(
                    observed.class,
                    PathClass::Project,
                    "{} {name}",
                    case.syscall
                );
                assert_eq!(
                    observed.logical,
                    NativePath::UnixBytes(logical.as_os_str().as_bytes().to_vec())
                );
                assert_eq!(observed.resolved, Some(observed.logical.clone()));
                assert_eq!(
                    observed.identity,
                    path_identity(&logical, FinalSymlink::NoFollow)
                );
            }
            let parent = fs::File::open(&root).unwrap();
            let relative = CString::new(name).unwrap();
            let relative_call = entry(
                libc::SYS_readlinkat,
                [
                    parent.as_raw_fd() as u64,
                    relative.as_ptr() as u64,
                    0,
                    0,
                    0,
                    0,
                ],
            );
            assert_eq!(
                decode(&relative_call, &root).unwrap().unwrap().paths[0].logical,
                NativePath::UnixBytes(logical.as_os_str().as_bytes().to_vec())
            );
            let mut metadata = std::mem::MaybeUninit::<libc::stat>::uninit();
            let mut target = [0_u8; MAX_PATH_BYTES];
            // SAFETY: fixture-owned terminated pathname and bounded outputs.
            unsafe {
                assert_eq!(libc::lstat(path.as_ptr(), metadata.as_mut_ptr()), 0);
                assert_eq!(
                    libc::fstatat(
                        libc::AT_FDCWD,
                        path.as_ptr(),
                        metadata.as_mut_ptr(),
                        libc::AT_SYMLINK_NOFOLLOW
                    ),
                    0
                );
                assert!(
                    libc::readlink(path.as_ptr(), target.as_mut_ptr().cast(), target.len()) > 0
                );
                assert!(
                    libc::readlinkat(
                        libc::AT_FDCWD,
                        path.as_ptr(),
                        target.as_mut_ptr().cast(),
                        target.len()
                    ) > 0
                );
            }
        }
    }

    #[test]
    fn syscall_specific_flags_preserve_following_and_entry_mutations() {
        use std::ffi::CString;
        let directory = tempfile::tempdir().unwrap();
        let base = directory.path().canonicalize().unwrap();
        let root = base.join("root");
        fs::create_dir(&root).unwrap();
        let outside = base.join("outside");
        fs::write(&outside, b"outside").unwrap();
        let logical = root.join("link");
        symlink(&outside, &logical).unwrap();
        let path = CString::new(logical.as_os_str().as_bytes()).unwrap();
        let destination = CString::new(root.join("destination").as_os_str().as_bytes()).unwrap();
        symlink(&outside, root.join("destination")).unwrap();
        let pointer = path.as_ptr() as u64;
        let target = destination.as_ptr() as u64;
        let at = libc::AT_FDCWD as u64;
        for case in [
            entry(libc::SYS_newfstatat, [at, pointer, 0, 0, 0, 0]),
            entry(libc::SYS_statx, [at, pointer, 0, 0, 0, 0]),
            entry(
                libc::SYS_openat,
                [at, pointer, libc::O_RDONLY as u64, 0, 0, 0],
            ),
            entry(
                libc::SYS_linkat,
                [at, pointer, at, target, libc::AT_SYMLINK_FOLLOW as u64, 0],
            ),
        ] {
            let decoded = decode(&case, &root).unwrap().unwrap();
            assert_eq!(decoded.paths[0].class, PathClass::External);
        }
        for case in [
            entry(libc::SYS_unlinkat, [at, pointer, 0, 0, 0, 0]),
            entry(libc::SYS_renameat, [at, pointer, at, target, 0, 0]),
            entry(libc::SYS_renameat2, [at, pointer, at, target, 0, 0]),
            entry(libc::SYS_linkat, [at, pointer, at, target, 0, 0]),
            entry(
                libc::SYS_openat,
                [
                    at,
                    pointer,
                    (libc::O_PATH | libc::O_NOFOLLOW) as u64,
                    0,
                    0,
                    0,
                ],
            ),
        ] {
            let decoded = decode(&case, &root).unwrap().unwrap();
            assert!(decoded
                .paths
                .iter()
                .all(|path| path.class == PathClass::Project));
            assert_eq!(
                decoded.paths[0].identity,
                path_identity(&logical, FinalSymlink::NoFollow)
            );
        }

        #[cfg(target_arch = "x86_64")]
        for case in [
            entry(libc::SYS_stat, [pointer, 0, 0, 0, 0, 0]),
            entry(libc::SYS_open, [pointer, libc::O_RDONLY as u64, 0, 0, 0, 0]),
        ] {
            assert_eq!(
                decode(&case, &root).unwrap().unwrap().paths[0].class,
                PathClass::External
            );
        }
        #[cfg(target_arch = "x86_64")]
        for case in [
            entry(libc::SYS_unlink, [pointer, 0, 0, 0, 0, 0]),
            entry(libc::SYS_rename, [pointer, target, 0, 0, 0, 0]),
            entry(libc::SYS_link, [pointer, target, 0, 0, 0, 0]),
        ] {
            assert!(decode(&case, &root)
                .unwrap()
                .unwrap()
                .paths
                .iter()
                .all(|path| path.class == PathClass::Project));
        }
        let how = [libc::O_PATH as u64 | libc::O_NOFOLLOW as u64, 0, 0];
        let case = entry(
            libc::SYS_openat2,
            [at, pointer, how.as_ptr() as u64, 24, 0, 0],
        );
        assert_eq!(
            decode(&case, &root).unwrap().unwrap().paths[0].class,
            PathClass::Project
        );
        let invalid_how = entry(libc::SYS_openat2, [at, pointer, 0, 24, 0, 0]);
        let decoded = decode(&invalid_how, &root).unwrap().unwrap();
        assert!(decoded.open_mutates);
        assert_eq!(decoded.paths[0].class, PathClass::External);
        let mut metadata = std::mem::MaybeUninit::<libc::stat>::uninit();
        // SAFETY: terminated fixture pathname, valid stat output and returned
        // fd.
        unsafe {
            assert_eq!(libc::stat(path.as_ptr(), metadata.as_mut_ptr()), 0);
            let fd = libc::open(path.as_ptr(), libc::O_RDONLY);
            assert!(fd >= 0);
            assert_eq!(libc::close(fd), 0);
        }
        fs::remove_file(root.join("destination")).unwrap();
        // Native link/rename/unlink operate on the fixture link entry, not its
        // outside target. Decode before each operation, as the tracer does.
        unsafe {
            assert_eq!(
                libc::linkat(
                    libc::AT_FDCWD,
                    path.as_ptr(),
                    libc::AT_FDCWD,
                    destination.as_ptr(),
                    0
                ),
                0
            );
        }
        assert!(fs::symlink_metadata(root.join("destination"))
            .unwrap()
            .file_type()
            .is_symlink());
        assert_eq!(
            path_identity(&logical, FinalSymlink::NoFollow),
            path_identity(&root.join("destination"), FinalSymlink::NoFollow)
        );
        fs::remove_file(root.join("destination")).unwrap();
        unsafe {
            assert_eq!(
                libc::renameat(
                    libc::AT_FDCWD,
                    path.as_ptr(),
                    libc::AT_FDCWD,
                    destination.as_ptr()
                ),
                0
            );
            assert_eq!(libc::unlinkat(libc::AT_FDCWD, destination.as_ptr(), 0), 0);
        }
        assert_eq!(fs::read(&outside).unwrap(), b"outside");
    }

    #[test]
    fn empty_at_path_uses_descriptor_only_with_native_flag() {
        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().canonicalize().unwrap();
        let file_path = root.join("input.txt");
        fs::write(&file_path, b"input").unwrap();
        let file = fs::File::open(&file_path).unwrap();
        let empty = b"\0";
        let mut entry = RawEntry {
            ordinal: 1,
            pid: std::process::id(),
            tid: std::process::id(),
            parent_pid: None,
            syscall: libc::SYS_statx as u64,
            args: [file.as_raw_fd() as u64, empty.as_ptr() as u64, 0, 0, 0, 0],
            monotonic_ns: 1,
        };
        let ordinary = decode(&entry, &root).unwrap().unwrap();
        assert!(ordinary.paths.is_empty());
        assert!(ordinary.path_unavailable);

        entry.args[2] = libc::AT_EMPTY_PATH as u64;
        let descriptor = decode(&entry, &root).unwrap().unwrap();
        assert_eq!(descriptor.paths.len(), 1);
        assert_eq!(descriptor.paths[0].class, PathClass::Project);
        assert!(descriptor.paths[0].identity.is_some());
        assert!(!descriptor.path_unavailable);
        let expected_identity = descriptor.paths[0].identity;

        entry.syscall = libc::SYS_newfstatat as u64;
        entry.args[2] = 0;
        entry.args[3] = libc::AT_EMPTY_PATH as u64;
        let descriptor = decode(&entry, &root).unwrap().unwrap();
        assert_eq!(descriptor.paths[0].identity, expected_identity);
        assert_eq!(descriptor.paths[0].class, PathClass::Project);
    }

    #[test]
    fn missing_path_parent_components_do_not_escape_classification() {
        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().canonicalize().unwrap();
        let path = root.join("missing/../../outside");
        let decoded = access_path(&root, path, None, FinalSymlink::Follow)
            .unwrap()
            .unwrap();
        assert_eq!(decoded.class, PathClass::External);
        assert!(decoded.project_relative.is_none());
    }

    #[test]
    fn existing_symlink_is_resolved_before_missing_suffix() {
        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().canonicalize().unwrap();
        fs::create_dir(root.join("inside")).unwrap();
        symlink(root.join("inside"), root.join("alias")).unwrap();
        let decoded = access_path(&root, root.join("alias/absent"), None, FinalSymlink::Follow)
            .unwrap()
            .unwrap();
        assert_eq!(decoded.class, PathClass::Project);
        assert_eq!(
            decoded.project_relative,
            Some(NativePath::UnixBytes(b"inside/absent".to_vec()))
        );
    }

    #[test]
    fn regular_file_ancestor_keeps_a_failed_path_observable() {
        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().canonicalize().unwrap();
        fs::write(root.join("parent.txt"), b"content").unwrap();
        let decoded = access_path(
            &root,
            root.join("parent.txt/child"),
            None,
            FinalSymlink::Follow,
        )
        .unwrap()
        .unwrap();
        assert_eq!(decoded.class, PathClass::Project);
        assert_eq!(
            decoded.project_relative,
            Some(NativePath::UnixBytes(b"parent.txt/child".to_vec()))
        );
    }

    #[test]
    fn invalid_native_path_pointer_keeps_the_failed_operation_observable() {
        let directory = tempfile::tempdir().unwrap();
        let entry = RawEntry {
            ordinal: 1,
            pid: std::process::id(),
            tid: std::process::id(),
            parent_pid: None,
            syscall: libc::SYS_openat as u64,
            args: [libc::AT_FDCWD as u64, 0, 0, 0, 0, 0],
            monotonic_ns: 1,
        };
        let decoded = decode(&entry, directory.path()).unwrap().unwrap();
        assert_eq!(decoded.operation, Operation::Open);
        assert!(decoded.paths.is_empty());
        assert!(decoded.path_unavailable);
    }

    #[test]
    fn inaccessible_ancestor_keeps_failed_path_unavailable() {
        use std::os::unix::fs::PermissionsExt;

        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().canonicalize().unwrap();
        let hidden = root.join("hidden");
        fs::create_dir(&hidden).unwrap();
        symlink(root.join("missing"), hidden.join("link")).unwrap();
        fs::set_permissions(&hidden, fs::Permissions::from_mode(0o000)).unwrap();
        let target = hidden.join("input.txt");
        let denied = fs::canonicalize(&target)
            .is_err_and(|error| error.kind() == io::ErrorKind::PermissionDenied);
        let observations: Vec<_> = [target, hidden.join("link")]
            .into_iter()
            .flat_map(|path| {
                [FinalSymlink::Follow, FinalSymlink::NoFollow]
                    .into_iter()
                    .map(move |policy| (path.clone(), policy))
            })
            .map(|(path, policy)| access_path(&root, path, None, policy))
            .collect();
        fs::set_permissions(&hidden, fs::Permissions::from_mode(0o700)).unwrap();
        if denied {
            for observed in observations {
                assert!(observed.unwrap().is_none());
            }
        }
        let restored = access_path(&root, hidden.join("link"), None, FinalSymlink::NoFollow)
            .unwrap()
            .unwrap();
        assert_eq!(restored.class, PathClass::Project);
        assert!(restored.identity.is_some());
    }
}
