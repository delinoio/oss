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
use crate::record::{AccessPath, FileIdentity, NativePath, Operation, PathClass};

const MAX_PATH_BYTES: usize = 4096;

#[derive(Debug, Clone)]
pub struct DecodedOperation {
    pub operation: Operation,
    pub paths: Vec<AccessPath>,
    pub path_unavailable: bool,
    pub descriptor: Option<i32>,
}

#[derive(Clone, Copy)]
enum Source {
    Path(usize),
    At(usize, usize),
    Descriptor(usize),
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

fn resolve_even_if_absent(path: &Path) -> Result<PathBuf, TraceFailure> {
    let mut cursor = path;
    let mut tail = Vec::<OsString>::new();
    loop {
        match fs::canonicalize(cursor) {
            Ok(mut resolved) => {
                for component in tail.iter().rev() {
                    resolved.push(component);
                }
                return Ok(lexical_normalize(&resolved));
            }
            Err(error) if error.kind() == io::ErrorKind::NotFound => {
                let component = cursor
                    .components()
                    .next_back()
                    .ok_or_else(|| supervision("missing_path_ancestor"))?;
                tail.push(component.as_os_str().to_os_string());
                cursor = cursor
                    .parent()
                    .ok_or_else(|| supervision("missing_path_parent"))?;
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
) -> Result<AccessPath, TraceFailure> {
    let resolved = resolve_even_if_absent(&logical)?;
    let project_relative = resolved.strip_prefix(root).ok();
    Ok(AccessPath {
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
        identity,
    })
}

fn from_source(
    entry: &RawEntry,
    root: &Path,
    source: Source,
) -> Result<(Option<AccessPath>, bool), TraceFailure> {
    let (path, identity) = match source {
        Source::Path(index) => (
            read_path(entry.tid, entry.args[index])?
                .map(|path| absolute_path(entry.tid, path, libc::AT_FDCWD))
                .transpose()?
                .flatten(),
            None,
        ),
        Source::At(dir_index, path_index) => {
            let path = read_path(entry.tid, entry.args[path_index])?
                .map(|path| absolute_path(entry.tid, path, entry.args[dir_index] as i32))
                .transpose()?
                .flatten();
            (path, None)
        }
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
    Ok((Some(access_path(root, path, identity)?), false))
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
        paths,
        path_unavailable,
        descriptor,
    }))
}

#[cfg(test)]
mod tests {
    use std::os::unix::fs::symlink;

    use super::*;

    #[test]
    fn missing_path_parent_components_do_not_escape_classification() {
        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().canonicalize().unwrap();
        let path = root.join("missing/../../outside");
        let decoded = access_path(&root, path, None).unwrap();
        assert_eq!(decoded.class, PathClass::External);
        assert!(decoded.project_relative.is_none());
    }

    #[test]
    fn existing_symlink_is_resolved_before_missing_suffix() {
        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().canonicalize().unwrap();
        fs::create_dir(root.join("inside")).unwrap();
        symlink(root.join("inside"), root.join("alias")).unwrap();
        let decoded = access_path(&root, root.join("alias/absent"), None).unwrap();
        assert_eq!(decoded.class, PathClass::Project);
        assert_eq!(
            decoded.project_relative,
            Some(NativePath::UnixBytes(b"inside/absent".to_vec()))
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
}
