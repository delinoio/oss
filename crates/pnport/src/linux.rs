//! Linux syscall interception for executables which cannot load the preload.
//!
//! The filter follows fspy's pinned Linux seccomp approach. Virtualization
//! needs to replace pathname arguments, so selected calls use
//! `SECCOMP_RET_TRACE` and a tracer owned by the launching supervisor. The
//! The helper stops itself before the supervisor seizes that owned child;
//! pnport never attaches to unrelated tasks.

use std::{
    collections::{HashMap, HashSet, VecDeque},
    ffi::{CString, OsStr, OsString},
    fs::{self, File},
    io::{self, Read, Seek, SeekFrom},
    mem,
    os::{
        fd::{AsRawFd, FromRawFd, OwnedFd},
        unix::{ffi::OsStrExt, process::CommandExt},
    },
    path::{Component, Path, PathBuf},
    process::{Child, Command},
    time::{Duration, Instant},
};

use libc::{self, c_void};
use pnport::{
    diagnostic::{Code, Error, ExecFailureKind, Result},
    executable::Prepared,
    graph::{Graph, Input},
    view::{Translation, View},
};

use crate::input_watch::InputWatch;

const LAUNCH_ARG: &str = "__pnport_linux_launch";
const PROBE_ARG: &str = "__pnport_linux_probe";
const HELPER_FD_ENV: &str = "PNPORT_HELPER_FD";
const WAIT_ALL: i32 = 0x4000_0000;
const SECCOMP_RET_TRACE: u32 = 0x7ff0_0000;
const SECCOMP_RET_ALLOW: u32 = 0x7fff_0000;
const SECCOMP_SET_MODE_FILTER: libc::c_uint = 1;
const PATH_LIMIT: usize = 4096;
// One private tracee mapping holds two pathname slots and the bounded exec
// vector. It is recreated after every exec, which discards the old mapping.
const SCRATCH_SIZE: usize = 8 * 1024 * 1024;
// Linux execve(2) caps argv+envp storage at 3/4 of _STK_LIM (8 MiB).
const EXEC_BYTES_LIMIT: usize = 6 * 1024 * 1024;
// The fixed prefix and resolution bits from Linux's openat2 UAPI.
const OPEN_HOW_SIZE: usize = 24;
const RESOLVE_BENEATH: u64 = 0x08;
const RESOLVE_IN_ROOT: u64 = 0x10;
const CLOSE_RANGE_UNSHARE: u32 = 2;
// Linux UAPI assigns this number on both supported 64-bit architectures.
const SYS_FCHMODAT2: i64 = 452;
// 64-bit Linux UAPI encodings from include/uapi/linux/fs.h. Only these
// known read-only filesystem requests may reach a managed dependency FD.
const FS_IOC_GETFLAGS: u64 = 0x8008_6601;
const FS_IOC_FSGETXATTR: u64 = 0x801c_581f;
const FIONREAD: u64 = libc::FIONREAD;
const TRACE_OPTIONS: usize = (libc::PTRACE_O_TRACESYSGOOD
    | libc::PTRACE_O_TRACEFORK
    | libc::PTRACE_O_TRACEVFORK
    | libc::PTRACE_O_TRACECLONE
    | libc::PTRACE_O_TRACEEXEC
    | libc::PTRACE_O_TRACESECCOMP
    | libc::PTRACE_O_EXITKILL) as usize;
#[cfg(target_arch = "x86_64")]
const SYS_OPEN: i64 = libc::SYS_open;
#[cfg(target_arch = "aarch64")]
const SYS_OPEN: i64 = -1;
#[cfg(target_arch = "x86_64")]
const SYS_LSTAT: i64 = libc::SYS_lstat;
#[cfg(target_arch = "aarch64")]
const SYS_LSTAT: i64 = -1;
#[cfg(target_arch = "x86_64")]
const SYS_ACCESS: i64 = libc::SYS_access;
#[cfg(target_arch = "aarch64")]
const SYS_ACCESS: i64 = -1;
#[cfg(target_arch = "x86_64")]
const SYS_READLINK: i64 = libc::SYS_readlink;
#[cfg(target_arch = "aarch64")]
const SYS_READLINK: i64 = -1;
#[cfg(target_arch = "x86_64")]
const SYS_DUP2: i64 = libc::SYS_dup2;
#[cfg(target_arch = "aarch64")]
const SYS_DUP2: i64 = -1;
#[cfg(target_arch = "x86_64")]
const SYS_FUTIMESAT: i64 = libc::SYS_futimesat;
#[cfg(target_arch = "aarch64")]
const SYS_FUTIMESAT: i64 = -1;

fn unsupported(message: &'static str) -> Error {
    Error::new(Code::PnportUnsupportedOperation, message)
}
fn injection_failed() -> Error {
    Error::new(
        Code::PnportInjectionFailed,
        "Linux syscall interception failed; the owned process tree was stopped.",
    )
}

fn escapes_beneath(path: &Path) -> bool {
    let mut depth = 0usize;
    for component in path.components() {
        match component {
            Component::Normal(_) => depth += 1,
            Component::ParentDir if depth == 0 => return true,
            Component::ParentDir => depth -= 1,
            _ => {}
        }
    }
    false
}

fn resolved_caller_lookup(path: &Path, follow_last: bool, graph: &Graph) -> Option<PathBuf> {
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
                        if followed > 40 {
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

fn follows_final_component(
    call: i64,
    regs: &Registers,
    path_arg: usize,
    open_flags: Option<i32>,
) -> bool {
    if let Some(flags) = open_flags {
        return flags & libc::O_NOFOLLOW == 0;
    }
    if call == libc::SYS_linkat {
        return path_arg == 1 && argument(regs, 4) as i32 & libc::AT_SYMLINK_FOLLOW != 0;
    }
    if matches!(call, n if n == libc::SYS_newfstatat || n == libc::SYS_faccessat2
        || n == libc::SYS_utimensat || n == SYS_FCHMODAT2)
    {
        return argument(regs, 3) as i32 & libc::AT_SYMLINK_NOFOLLOW == 0;
    }
    if call == libc::SYS_statx || call == libc::SYS_inotify_add_watch {
        return argument(regs, 2) as i32
            & (if call == libc::SYS_statx {
                libc::AT_SYMLINK_NOFOLLOW
            } else {
                libc::IN_DONT_FOLLOW as i32
            })
            == 0;
    }
    if call == libc::SYS_fchownat {
        return argument(regs, 4) as i32 & libc::AT_SYMLINK_NOFOLLOW == 0;
    }
    if matches!(call, n if n == libc::SYS_readlinkat || n == libc::SYS_unlinkat
        || n == libc::SYS_mkdirat || n == libc::SYS_mknodat || n == libc::SYS_symlinkat
        || n == libc::SYS_renameat || n == libc::SYS_renameat2 || n == SYS_LSTAT
        || n == SYS_READLINK || n == libc::SYS_lgetxattr || n == libc::SYS_llistxattr
        || n == libc::SYS_lsetxattr || n == libc::SYS_lremovexattr)
    {
        return false;
    }
    #[cfg(target_arch = "x86_64")]
    if matches!(call, n if n == libc::SYS_unlink || n == libc::SYS_mkdir
        || n == libc::SYS_mknod || n == libc::SYS_symlink || n == libc::SYS_rename
        || n == libc::SYS_link || n == libc::SYS_rmdir || n == libc::SYS_lchown)
    {
        return false;
    }
    true
}

pub fn is_static(path: &Path) -> Result<bool> {
    let mut file = File::open(path).map_err(|_| injection_failed())?;
    let len = file.metadata().map_err(|_| injection_failed())?.len();
    let mut header = [0u8; 64];
    file.read_exact(&mut header)
        .map_err(|_| unsupported("The executable is not a supported Linux ELF image."))?;
    if &header[..4] != b"\x7fELF" || header[4] != 2 || header[5] != 1 {
        return Err(unsupported(
            "The executable is not a supported 64-bit Linux ELF image.",
        ));
    }
    let machine = u16::from_le_bytes(header[18..20].try_into().unwrap());
    if machine
        != if cfg!(target_arch = "aarch64") {
            183
        } else {
            62
        }
    {
        return Err(unsupported(
            "The executable architecture does not match this Linux host.",
        ));
    }
    let offset = u64::from_le_bytes(header[32..40].try_into().unwrap());
    let stride = u16::from_le_bytes(header[54..56].try_into().unwrap()) as u64;
    let count = u16::from_le_bytes(header[56..58].try_into().unwrap()) as u64;
    if stride < 56
        || count == 0
        || count > 128
        || offset
            .checked_add(stride.checked_mul(count).ok_or_else(injection_failed)?)
            .is_none_or(|end| end > len)
    {
        return Err(unsupported("The Linux ELF program headers are malformed."));
    }
    for index in 0..count {
        file.seek(SeekFrom::Start(offset + index * stride))
            .map_err(|_| injection_failed())?;
        let mut kind = [0u8; 4];
        file.read_exact(&mut kind).map_err(|_| injection_failed())?;
        if u32::from_le_bytes(kind) == 3 {
            return Ok(false);
        }
    }
    Ok(true)
}

fn traced_syscalls() -> Vec<i64> {
    #[allow(unused_mut, reason = "x86-64 extends the common syscall set")]
    let mut calls = vec![
        libc::SYS_openat,
        libc::SYS_openat2,
        libc::SYS_newfstatat,
        libc::SYS_statx,
        libc::SYS_statfs,
        libc::SYS_readlinkat,
        libc::SYS_faccessat,
        libc::SYS_faccessat2,
        libc::SYS_execve,
        libc::SYS_execveat,
        libc::SYS_chdir,
        libc::SYS_fchdir,
        libc::SYS_getcwd,
        libc::SYS_clone,
        libc::SYS_clone3,
        libc::SYS_unshare,
        libc::SYS_setns,
        libc::SYS_chroot,
        libc::SYS_pivot_root,
        libc::SYS_close,
        libc::SYS_close_range,
        libc::SYS_dup,
        libc::SYS_dup3,
        libc::SYS_fcntl,
        libc::SYS_ioctl,
        libc::SYS_recvmsg,
        libc::SYS_recvmmsg,
        libc::SYS_pidfd_getfd,
        libc::SYS_unlinkat,
        libc::SYS_mkdirat,
        libc::SYS_renameat,
        libc::SYS_renameat2,
        libc::SYS_linkat,
        libc::SYS_symlinkat,
        libc::SYS_mknodat,
        libc::SYS_fchmodat,
        SYS_FCHMODAT2,
        libc::SYS_fchownat,
        libc::SYS_utimensat,
        libc::SYS_fchmod,
        libc::SYS_fchown,
        libc::SYS_ftruncate,
        libc::SYS_fallocate,
        libc::SYS_truncate,
        libc::SYS_getxattr,
        libc::SYS_lgetxattr,
        libc::SYS_listxattr,
        libc::SYS_llistxattr,
        libc::SYS_setxattr,
        libc::SYS_lsetxattr,
        libc::SYS_removexattr,
        libc::SYS_lremovexattr,
        libc::SYS_fsetxattr,
        libc::SYS_fremovexattr,
        libc::SYS_inotify_add_watch,
        libc::SYS_fanotify_mark,
        libc::SYS_open_by_handle_at,
        libc::SYS_io_uring_setup,
        libc::SYS_io_uring_enter,
        libc::SYS_io_uring_register,
    ];
    #[cfg(target_arch = "x86_64")]
    calls.extend([
        libc::SYS_vfork,
        libc::SYS_open,
        libc::SYS_stat,
        libc::SYS_lstat,
        libc::SYS_access,
        libc::SYS_readlink,
        libc::SYS_unlink,
        libc::SYS_mkdir,
        libc::SYS_rename,
        libc::SYS_creat,
        libc::SYS_link,
        libc::SYS_symlink,
        libc::SYS_rmdir,
        libc::SYS_chmod,
        libc::SYS_chown,
        libc::SYS_lchown,
        libc::SYS_utime,
        libc::SYS_utimes,
        libc::SYS_mknod,
        libc::SYS_dup2,
        libc::SYS_futimesat,
    ]);
    calls
}

fn fd_sensitive_syscall(call: i64) -> bool {
    if matches!(
        call,
        x if x == libc::SYS_close
            || x == libc::SYS_close_range
            || x == libc::SYS_dup
            || x == libc::SYS_dup3
            || x == libc::SYS_fcntl
            || x == libc::SYS_ioctl
            || x == libc::SYS_fchdir
            || x == libc::SYS_fchmod
            || x == libc::SYS_fchown
            || x == libc::SYS_ftruncate
            || x == libc::SYS_fallocate
            || x == libc::SYS_fsetxattr
            || x == libc::SYS_fremovexattr
            || x == libc::SYS_fchmodat
            || x == SYS_FCHMODAT2
            || x == libc::SYS_fchownat
            || x == libc::SYS_utimensat
            || x == libc::SYS_truncate
            || x == libc::SYS_setxattr
            || x == libc::SYS_lsetxattr
            || x == libc::SYS_removexattr
            || x == libc::SYS_lremovexattr
            || x == libc::SYS_unlinkat
            || x == libc::SYS_renameat
            || x == libc::SYS_renameat2
            || x == libc::SYS_linkat
            || x == libc::SYS_symlinkat
            || x == libc::SYS_mkdirat
            || x == libc::SYS_mknodat
    ) {
        return true;
    }
    #[cfg(target_arch = "x86_64")]
    {
        matches!(
            call,
            x if x == libc::SYS_dup2
                || x == libc::SYS_futimesat
                || x == libc::SYS_truncate
                || x == libc::SYS_chmod
                || x == libc::SYS_chown
                || x == libc::SYS_lchown
                || x == libc::SYS_utime
                || x == libc::SYS_utimes
                || x == libc::SYS_unlink
                || x == libc::SYS_rename
                || x == libc::SYS_link
                || x == libc::SYS_symlink
                || x == libc::SYS_mkdir
                || x == libc::SYS_rmdir
                || x == libc::SYS_mknod
        )
    }
    #[cfg(target_arch = "aarch64")]
    {
        false
    }
}

fn install_filter() -> std::io::Result<()> {
    let arch = if cfg!(target_arch = "aarch64") {
        0xc000_00b7
    } else {
        0xc000_003e
    };
    let mut filters = Vec::<libc::sock_filter>::new();
    let statement = |code, value| libc::sock_filter {
        code,
        jt: 0,
        jf: 0,
        k: value,
    };
    filters.push(statement(
        (libc::BPF_LD | libc::BPF_W | libc::BPF_ABS) as u16,
        4,
    ));
    filters.push(libc::sock_filter {
        code: (libc::BPF_JMP | libc::BPF_JEQ | libc::BPF_K) as u16,
        jt: 1,
        jf: 0,
        k: arch,
    });
    filters.push(statement(
        (libc::BPF_RET | libc::BPF_K) as u16,
        libc::SECCOMP_RET_KILL_PROCESS,
    ));
    filters.push(statement(
        (libc::BPF_LD | libc::BPF_W | libc::BPF_ABS) as u16,
        0,
    ));
    for number in traced_syscalls() {
        filters.push(libc::sock_filter {
            code: (libc::BPF_JMP | libc::BPF_JEQ | libc::BPF_K) as u16,
            jt: 0,
            jf: 1,
            k: number as u32,
        });
        filters.push(statement(
            (libc::BPF_RET | libc::BPF_K) as u16,
            SECCOMP_RET_TRACE,
        ));
    }
    filters.push(statement(
        (libc::BPF_RET | libc::BPF_K) as u16,
        SECCOMP_RET_ALLOW,
    ));
    let program = libc::sock_fprog {
        len: filters.len() as u16,
        filter: filters.as_mut_ptr(),
    };
    unsafe {
        if libc::prctl(libc::PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) != 0
            || libc::syscall(libc::SYS_seccomp, SECCOMP_SET_MODE_FILTER, 0, &program) != 0
        {
            return Err(std::io::Error::last_os_error());
        }
    }
    Ok(())
}

/// Dispatches the private launch mode before clap processes public arguments.
pub fn dispatch_helper() {
    let mut args = std::env::args_os();
    args.next();
    match args.next().as_deref() {
        Some(value) if value == OsStr::new(PROBE_ARG) && authenticate_helper() => {
            let status = helper_setup()
                .and_then(|_| {
                    let path = b"/pnport-probe-must-be-rewritten\0";
                    let fd = unsafe {
                        libc::syscall(
                            libc::SYS_openat,
                            libc::AT_FDCWD,
                            path.as_ptr().cast::<libc::c_char>(),
                            libc::O_RDONLY,
                            0,
                        )
                    };
                    if fd < 0 {
                        return Err(std::io::Error::last_os_error());
                    }
                    // _exit closes this probe descriptor without another
                    // filtered syscall stop that the capability check needs
                    // to mediate.
                    Ok(())
                })
                .map(|_| 0)
                .unwrap_or(125);
            unsafe { libc::_exit(status) }
        }
        Some(value) if value == OsStr::new(LAUNCH_ARG) && authenticate_helper() => {
            if helper_setup().is_err() {
                record_helper_failure();
                unsafe { libc::_exit(125) }
            }
            let Some(program) = args.next() else {
                unsafe { libc::_exit(125) }
            };
            let error = Command::new(program).args(args).exec();
            let code = if error.kind() == std::io::ErrorKind::NotFound {
                Code::PnportCommandNotFound
            } else {
                Code::PnportCommandNotExecutable
            };
            record_helper_code(code);
            unsafe {
                libc::_exit(if code == Code::PnportCommandNotFound {
                    127
                } else {
                    126
                })
            }
        }
        _ => {}
    }
}

fn authenticate_helper() -> bool {
    use std::os::unix::fs::MetadataExt;

    let Some(fd) = std::env::var(HELPER_FD_ENV)
        .ok()
        .and_then(|value| value.parse::<i32>().ok())
        .filter(|fd| *fd >= 3)
    else {
        return false;
    };
    let parent = unsafe { libc::getppid() };
    let mut peer = unsafe { mem::zeroed::<libc::ucred>() };
    let mut length = mem::size_of::<libc::ucred>() as libc::socklen_t;
    if unsafe {
        libc::getsockopt(
            fd,
            libc::SOL_SOCKET,
            libc::SO_PEERCRED,
            (&mut peer as *mut libc::ucred).cast(),
            &mut length,
        )
    } != 0
        || length as usize != mem::size_of::<libc::ucred>()
        || peer.pid != parent
        || peer.uid != unsafe { libc::getuid() }
        || peer.gid != unsafe { libc::getgid() }
    {
        return false;
    }
    let same_image = fs::metadata("/proc/self/exe")
        .and_then(|child| {
            fs::metadata(format!("/proc/{parent}/exe"))
                .map(|owner| child.dev() == owner.dev() && child.ino() == owner.ino())
        })
        .unwrap_or(false);
    if !same_image {
        return false;
    }
    unsafe { libc::close(fd) };
    std::env::remove_var(HELPER_FD_ENV);
    true
}

fn spawn_owned_helper(command: &mut Command) -> Result<(Child, OwnedFd)> {
    let mut pair = [0i32; 2];
    if unsafe {
        libc::socketpair(
            libc::AF_UNIX,
            libc::SOCK_STREAM | libc::SOCK_CLOEXEC,
            0,
            pair.as_mut_ptr(),
        )
    } != 0
    {
        return Err(injection_failed());
    }
    let owner = unsafe { OwnedFd::from_raw_fd(pair[0]) };
    let helper = unsafe { OwnedFd::from_raw_fd(pair[1]) };
    let helper_fd = helper.as_raw_fd();
    command.env(HELPER_FD_ENV, helper_fd.to_string());
    unsafe {
        command.pre_exec(move || {
            if libc::fcntl(helper_fd, libc::F_SETFD, 0) < 0 {
                return Err(io::Error::last_os_error());
            }
            Ok(())
        });
    }
    let child = command.spawn().map_err(|_| injection_failed())?;
    drop(helper);
    Ok((child, owner))
}

fn record_helper_failure() {
    record_helper_code(Code::PnportInjectionFailed);
}

fn record_helper_code(code: Code) {
    if let Some(session) = std::env::var_os("PNPORT_SESSION") {
        let _ = fs::write(PathBuf::from(session).join("failure"), code.as_str());
    }
}

fn helper_setup() -> std::io::Result<()> {
    install_filter()?;
    unsafe {
        libc::raise(libc::SIGSTOP);
    }
    Ok(())
}

pub fn probe() -> Result<()> {
    let binary = std::env::current_exe().map_err(|_| injection_failed())?;
    let (mut child, owner) = spawn_owned_helper(Command::new(binary).arg(PROBE_ARG))?;
    let pid = child.id() as i32;
    let mediated = (|| -> Result<()> {
        let mut status = 0;
        if unsafe { libc::waitpid(pid, &mut status, libc::WUNTRACED) } != pid
            || !libc::WIFSTOPPED(status)
            || libc::WSTOPSIG(status) != libc::SIGSTOP
        {
            tracing::debug!(
                action = "linux_probe",
                stage = "initial_stop",
                status,
                "Probe stop unavailable"
            );
            return Err(injection_failed());
        }
        drop(owner);
        if unsafe { libc::ptrace(libc::PTRACE_SEIZE, pid, 0, TRACE_OPTIONS) } != 0 {
            tracing::debug!(
                action = "linux_probe",
                stage = "options",
                "Probe trace options unavailable"
            );
            return Err(injection_failed());
        }
        if unsafe { libc::kill(pid, libc::SIGCONT) } != 0 {
            return Err(injection_failed());
        }
        let mut seccomp_stop = false;
        for _ in 0..8 {
            if unsafe { libc::waitpid(pid, &mut status, WAIT_ALL) } != pid
                || !libc::WIFSTOPPED(status)
            {
                break;
            }
            if status >> 16 == libc::PTRACE_EVENT_SECCOMP {
                seccomp_stop = true;
                break;
            }
            if status >> 16 == libc::PTRACE_EVENT_STOP || libc::WSTOPSIG(status) == libc::SIGCONT {
                resume(pid, false, 0)?;
                continue;
            }
            break;
        }
        if !seccomp_stop {
            tracing::debug!(
                action = "linux_probe",
                stage = "seccomp_stop",
                status,
                "Probe trace event unavailable"
            );
            return Err(injection_failed());
        }
        let mut regs = registers(pid)?;
        if number(&regs) != libc::SYS_openat
            || read_path(pid, argument(&regs, 1))?
                != Some(PathBuf::from("/pnport-probe-must-be-rewritten"))
        {
            tracing::debug!(
                action = "linux_probe",
                stage = "pathname",
                call = number(&regs),
                "Probe pathname did not match"
            );
            return Err(injection_failed());
        }
        // The probe is our own helper with a known ordinary stack. Arbitrary
        // tracees receive a dedicated mapping before path rewriting.
        rewrite_probe_path(pid, &mut regs, 1, Path::new("/dev/null"))?;
        resume(pid, true, 0)?;
        if unsafe { libc::waitpid(pid, &mut status, 0) } != pid
            || !libc::WIFSTOPPED(status)
            || libc::WSTOPSIG(status) != (libc::SIGTRAP | 0x80)
            || result(&registers(pid)?) < 0
        {
            tracing::debug!(
                action = "linux_probe",
                stage = "syscall_exit",
                status,
                "Probe syscall did not open the rewritten path"
            );
            return Err(injection_failed());
        }
        resume(pid, false, 0)
    })();
    if let Err(error) = mediated {
        tracing::debug!(
            action = "linux_probe",
            stage = "mediation",
            code = error.code.as_str(),
            "Probe mediation failed"
        );
        unsafe { libc::kill(pid, libc::SIGKILL) };
        let _ = child.wait();
        return Err(unsupported(
            "Linux seccomp pathname mediation is unavailable.",
        ));
    }
    let status = child.wait().map_err(|_| injection_failed())?;
    if !status.success() {
        tracing::debug!(
            action = "linux_probe",
            stage = "child_exit",
            ?status,
            "Probe helper did not complete"
        );
        return Err(unsupported(
            "Linux seccomp pathname mediation is unavailable.",
        ));
    }
    Ok(())
}

#[cfg(target_arch = "x86_64")]
type Registers = libc::user_regs_struct;
#[cfg(target_arch = "aarch64")]
type Registers = libc::user_regs_struct;

fn registers(pid: i32) -> Result<Registers> {
    let mut value = unsafe { mem::zeroed::<Registers>() };
    let mut iov = libc::iovec {
        iov_base: (&mut value as *mut Registers).cast::<c_void>(),
        iov_len: mem::size_of::<Registers>(),
    };
    if unsafe { libc::ptrace(libc::PTRACE_GETREGSET, pid, libc::NT_PRSTATUS, &mut iov) } != 0 {
        return Err(injection_failed());
    }
    Ok(value)
}
fn set_registers(pid: i32, value: &Registers) -> Result<()> {
    let mut iov = libc::iovec {
        iov_base: (value as *const Registers).cast_mut().cast::<c_void>(),
        iov_len: mem::size_of::<Registers>(),
    };
    if unsafe { libc::ptrace(libc::PTRACE_SETREGSET, pid, libc::NT_PRSTATUS, &mut iov) } != 0 {
        return Err(injection_failed());
    }
    Ok(())
}
#[cfg(target_arch = "x86_64")]
fn number(regs: &Registers) -> i64 {
    regs.orig_rax as i64
}
#[cfg(target_arch = "x86_64")]
fn set_number(regs: &mut Registers, value: i64) {
    regs.orig_rax = value as u64;
    regs.rax = value as u64;
}
#[cfg(target_arch = "aarch64")]
fn set_number(regs: &mut Registers, value: i64) {
    regs.regs[8] = value as u64;
}
#[cfg(target_arch = "aarch64")]
fn set_active_syscall(pid: i32, value: i64) -> Result<()> {
    // arm64 keeps the active syscall number outside NT_PRSTATUS. Changing x8
    // alone does not replace the syscall already stopped by seccomp.
    const NT_ARM_SYSTEM_CALL: usize = 0x404;
    let mut number = value as i32;
    let mut iov = libc::iovec {
        iov_base: (&mut number as *mut i32).cast(),
        iov_len: mem::size_of::<i32>(),
    };
    if unsafe { libc::ptrace(libc::PTRACE_SETREGSET, pid, NT_ARM_SYSTEM_CALL, &mut iov) } != 0 {
        return Err(injection_failed());
    }
    Ok(())
}
#[cfg(target_arch = "x86_64")]
fn rewind_syscall(regs: &mut Registers) -> Result<()> {
    regs.rip = regs.rip.checked_sub(2).ok_or_else(injection_failed)?;
    Ok(())
}
#[cfg(target_arch = "aarch64")]
fn rewind_syscall(regs: &mut Registers) -> Result<()> {
    regs.pc = regs.pc.checked_sub(4).ok_or_else(injection_failed)?;
    Ok(())
}
fn prepare_replayed_syscall(regs: &mut Registers) -> Result<()> {
    let call = number(regs);
    rewind_syscall(regs)?;
    // x86-64 stores -ENOSYS in rax at a seccomp stop. After rewinding the
    // instruction, userspace must put the original number back in rax;
    // orig_rax alone is not the syscall instruction's input.
    set_number(regs, call);
    Ok(())
}
#[cfg(target_arch = "aarch64")]
fn number(regs: &Registers) -> i64 {
    regs.regs[8] as i64
}
fn deny_syscall(pid: i32, regs: &mut Registers) -> Result<()> {
    set_number(regs, -1);
    set_registers(pid, regs)?;
    #[cfg(target_arch = "aarch64")]
    set_active_syscall(pid, -1)?;
    Ok(())
}
fn finish_denied_syscall(pid: i32, regs: &mut Registers) -> Result<()> {
    deny_syscall(pid, regs)?;
    // A signal injected at a seccomp entry can restart the original syscall
    // after its handler returns. Reach the cancelled syscall's exit stop
    // before cleanup is allowed to deliver any graceful signal.
    resume(pid, true, 0)?;
    let mut status = 0;
    loop {
        let stopped = unsafe { libc::waitpid(pid, &mut status, WAIT_ALL) };
        if stopped == pid {
            break;
        }
        if stopped < 0 && std::io::Error::last_os_error().raw_os_error() == Some(libc::EINTR) {
            continue;
        }
        return Err(injection_failed());
    }
    if !libc::WIFSTOPPED(status) || libc::WSTOPSIG(status) != (libc::SIGTRAP | 0x80) {
        return Err(injection_failed());
    }
    let mut returned = registers(pid)?;
    set_result(&mut returned, -(libc::ENOSYS as i64));
    set_registers(pid, &returned)?;
    resume(pid, false, 0)
}
#[cfg(target_arch = "x86_64")]
fn argument(regs: &Registers, index: usize) -> u64 {
    [regs.rdi, regs.rsi, regs.rdx, regs.r10, regs.r8, regs.r9][index]
}
#[cfg(target_arch = "aarch64")]
fn argument(regs: &Registers, index: usize) -> u64 {
    regs.regs[index]
}
#[cfg(target_arch = "x86_64")]
fn set_argument(regs: &mut Registers, index: usize, value: u64) {
    match index {
        0 => regs.rdi = value,
        1 => regs.rsi = value,
        2 => regs.rdx = value,
        3 => regs.r10 = value,
        4 => regs.r8 = value,
        5 => regs.r9 = value,
        _ => unreachable!(),
    }
}
#[cfg(target_arch = "aarch64")]
fn set_argument(regs: &mut Registers, index: usize, value: u64) {
    regs.regs[index] = value;
}
#[cfg(target_arch = "x86_64")]
fn stack_pointer(regs: &Registers) -> u64 {
    regs.rsp
}
#[cfg(target_arch = "aarch64")]
fn stack_pointer(regs: &Registers) -> u64 {
    regs.sp
}
#[cfg(target_arch = "x86_64")]
fn result(regs: &Registers) -> i64 {
    regs.rax as i64
}
#[cfg(target_arch = "aarch64")]
fn result(regs: &Registers) -> i64 {
    regs.regs[0] as i64
}
#[cfg(target_arch = "x86_64")]
fn set_result(regs: &mut Registers, value: i64) {
    regs.rax = value as u64;
}
#[cfg(target_arch = "aarch64")]
fn set_result(regs: &mut Registers, value: i64) {
    regs.regs[0] = value as u64;
}

fn read_remote(pid: i32, address: u64, max: usize) -> Result<Vec<u8>> {
    let mut output = vec![0u8; max];
    let local = libc::iovec {
        iov_base: output.as_mut_ptr().cast(),
        iov_len: max,
    };
    let remote = libc::iovec {
        iov_base: address as usize as *mut c_void,
        iov_len: max,
    };
    let count = unsafe { libc::process_vm_readv(pid, &local, 1, &remote, 1, 0) };
    if count <= 0 {
        return Err(injection_failed());
    }
    output.truncate(count as usize);
    Ok(output)
}

fn receives_ancillary_data(pid: i32, message: u64) -> Result<bool> {
    if message == 0 {
        // Leave a null pointer to the kernel's ordinary EFAULT handling.
        return Ok(false);
    }
    let bytes = read_remote(pid, message, mem::size_of::<libc::msghdr>())?;
    if bytes.len() != mem::size_of::<libc::msghdr>() {
        return Err(injection_failed());
    }
    let mut header = unsafe { mem::zeroed::<libc::msghdr>() };
    unsafe {
        std::ptr::copy_nonoverlapping(
            bytes.as_ptr(),
            (&mut header as *mut libc::msghdr).cast(),
            bytes.len(),
        );
    }
    Ok(!header.msg_control.is_null() && header.msg_controllen > 0)
}

fn read_path(pid: i32, address: u64) -> Result<Option<PathBuf>> {
    if address == 0 {
        return Ok(None);
    }
    let mut bytes = Vec::new();
    while bytes.len() < PATH_LIMIT {
        let part = match read_remote(
            pid,
            address + bytes.len() as u64,
            (PATH_LIMIT - bytes.len()).min(256),
        ) {
            Ok(part) => part,
            Err(_) if std::io::Error::last_os_error().raw_os_error() == Some(libc::EFAULT) => {
                return Ok(None);
            }
            Err(error) => return Err(error),
        };
        if let Some(end) = part.iter().position(|byte| *byte == 0) {
            bytes.extend_from_slice(&part[..end]);
            return Ok(Some(PathBuf::from(OsStr::from_bytes(&bytes))));
        }
        bytes.extend_from_slice(&part);
    }
    Err(unsupported(
        "A child pathname exceeded the Linux path limit.",
    ))
}

enum ChildRead<T> {
    Value(T),
    Fault,
}

fn read_exec_memory(pid: i32, address: u64, length: usize) -> Result<ChildRead<Vec<u8>>> {
    match read_remote(pid, address, length) {
        Ok(bytes) => Ok(ChildRead::Value(bytes)),
        Err(_) if io::Error::last_os_error().raw_os_error() == Some(libc::EFAULT) => {
            Ok(ChildRead::Fault)
        }
        Err(error) => Err(error),
    }
}

fn read_pointer_vector(pid: i32, address: u64) -> Result<ChildRead<Vec<u64>>> {
    if address == 0 {
        return Ok(ChildRead::Value(Vec::new()));
    }
    let mut values = Vec::new();
    let mut offset = 0;
    while offset < EXEC_BYTES_LIMIT {
        let bytes = match read_exec_memory(
            pid,
            address + offset as u64,
            (EXEC_BYTES_LIMIT - offset).min(PATH_LIMIT),
        )? {
            ChildRead::Value(bytes) => bytes,
            ChildRead::Fault => return Ok(ChildRead::Fault),
        };
        if bytes.len() < mem::size_of::<u64>() || bytes.len() % mem::size_of::<u64>() != 0 {
            return Ok(ChildRead::Fault);
        }
        for chunk in bytes.chunks_exact(mem::size_of::<u64>()) {
            let pointer = u64::from_ne_bytes(chunk.try_into().map_err(|_| injection_failed())?);
            if pointer == 0 {
                return Ok(ChildRead::Value(values));
            }
            values.push(pointer);
        }
        offset += bytes.len();
    }
    Err(unsupported(
        "A child exec pointer vector exceeded Linux's argument byte limit.",
    ))
}

fn child_search_path(pid: i32, envp: u64) -> Result<ChildRead<Option<OsString>>> {
    let pointers = match read_pointer_vector(pid, envp)? {
        ChildRead::Value(pointers) => pointers,
        ChildRead::Fault => return Ok(ChildRead::Fault),
    };
    for pointer in pointers {
        let prefix = match read_exec_memory(pid, pointer, 5)? {
            ChildRead::Value(prefix) => prefix,
            ChildRead::Fault => return Ok(ChildRead::Fault),
        };
        if prefix.get(..5) == Some(b"PATH=") {
            let mut bytes = Vec::new();
            while bytes.len() < EXEC_BYTES_LIMIT {
                let part = match read_exec_memory(
                    pid,
                    pointer + bytes.len() as u64,
                    (EXEC_BYTES_LIMIT - bytes.len()).min(256),
                )? {
                    ChildRead::Value(part) => part,
                    ChildRead::Fault => return Ok(ChildRead::Fault),
                };
                if let Some(end) = part.iter().position(|byte| *byte == 0) {
                    bytes.extend_from_slice(&part[..end]);
                    break;
                }
                bytes.extend_from_slice(&part);
            }
            if bytes.len() == EXEC_BYTES_LIMIT {
                return Err(unsupported(
                    "A child PATH value exceeded Linux's argument byte limit.",
                ));
            }
            return Ok(ChildRead::Value(Some(OsString::from(OsStr::from_bytes(
                &bytes[5..],
            )))));
        }
        if prefix.len() < 5 && !prefix.contains(&0) {
            return Ok(ChildRead::Fault);
        }
    }
    Ok(ChildRead::Value(None))
}
fn write_remote(pid: i32, address: u64, bytes: &[u8]) -> Result<()> {
    let local = libc::iovec {
        iov_base: bytes.as_ptr().cast_mut().cast(),
        iov_len: bytes.len(),
    };
    let remote = libc::iovec {
        iov_base: address as usize as *mut c_void,
        iov_len: bytes.len(),
    };
    if unsafe { libc::process_vm_writev(pid, &local, 1, &remote, 1, 0) } != bytes.len() as isize {
        return Err(injection_failed());
    }
    Ok(())
}
fn write_remote_or_fault(pid: i32, address: u64, bytes: &[u8]) -> Result<bool> {
    let local = libc::iovec {
        iov_base: bytes.as_ptr().cast_mut().cast(),
        iov_len: bytes.len(),
    };
    let remote = libc::iovec {
        iov_base: address as usize as *mut c_void,
        iov_len: bytes.len(),
    };
    let count = unsafe { libc::process_vm_writev(pid, &local, 1, &remote, 1, 0) };
    if count == bytes.len() as isize {
        return Ok(true);
    }
    if count >= 0 || std::io::Error::last_os_error().raw_os_error() == Some(libc::EFAULT) {
        return Ok(false);
    }
    Err(injection_failed())
}
fn rewrite_probe_path(pid: i32, regs: &mut Registers, index: usize, path: &Path) -> Result<()> {
    write_path_at(pid, regs, index, path, 0, stack_pointer(regs))
}
fn write_path_at(
    pid: i32,
    regs: &mut Registers,
    index: usize,
    path: &Path,
    slot: usize,
    top: u64,
) -> Result<()> {
    let bytes = CString::new(path.as_os_str().as_bytes()).map_err(|_| injection_failed())?;
    if bytes.as_bytes_with_nul().len() > PATH_LIMIT {
        return Err(injection_failed());
    }
    let address = top
        .checked_sub(bytes.as_bytes_with_nul().len() as u64 + 256 + (slot * PATH_LIMIT) as u64)
        .ok_or_else(injection_failed)?
        & !15;
    write_remote(pid, address, bytes.as_bytes_with_nul())?;
    set_argument(regs, index, address);
    set_registers(pid, regs)
}

fn rewrite_exec_arguments(
    pid: i32,
    regs: &mut Registers,
    argv_arg: usize,
    path_address: u64,
    prefixes: &[OsString],
    original: &[u64],
    scratch_base: u64,
) -> Result<()> {
    let mut cursor = path_address;
    let mut pointers = vec![path_address];
    for prefix in prefixes {
        let bytes = CString::new(prefix.as_os_str().as_bytes()).map_err(|_| injection_failed())?;
        cursor = cursor
            .checked_sub(bytes.as_bytes_with_nul().len() as u64 + 16)
            .ok_or_else(injection_failed)?
            & !15;
        if cursor < scratch_base {
            return Err(unsupported(
                "A child exec vector exceeded its scratch mapping.",
            ));
        }
        write_remote(pid, cursor, bytes.as_bytes_with_nul())?;
        pointers.push(cursor);
    }
    pointers.extend(original.iter().skip(1).copied());
    pointers.push(0);
    let raw: Vec<u8> = pointers
        .iter()
        .flat_map(|pointer| pointer.to_ne_bytes())
        .collect();
    if raw.len() > EXEC_BYTES_LIMIT {
        return Err(unsupported(
            "A child exec pointer vector exceeded Linux's argument byte limit.",
        ));
    }
    cursor = cursor
        .checked_sub(raw.len() as u64 + 16)
        .ok_or_else(injection_failed)?
        & !15;
    if cursor < scratch_base {
        return Err(unsupported(
            "A child exec vector exceeded its scratch mapping.",
        ));
    }
    write_remote(pid, cursor, &raw)?;
    set_argument(regs, argv_arg, cursor);
    set_registers(pid, regs)
}

fn resume(pid: i32, syscall_exit: bool, signal: i32) -> Result<()> {
    let action = if syscall_exit {
        libc::PTRACE_SYSCALL
    } else {
        libc::PTRACE_CONT
    };
    if unsafe { libc::ptrace(action, pid, 0, signal) } != 0 {
        return Err(injection_failed());
    }
    Ok(())
}

#[derive(Clone)]
enum Pending {
    Open(Translation),
    Close(i32),
    CloseRange(u32, u32),
    Dup(Option<Translation>),
    ChangeDirectory(Option<PathBuf>),
    LinkMetadata {
        output: u64,
        target_len: usize,
        statx: bool,
    },
    ReadLink {
        output: u64,
        capacity: usize,
        target: PathBuf,
    },
    GetCwd {
        output: u64,
        capacity: usize,
        logical: PathBuf,
    },
    ForcedError(i32),
    Ordinary,
}

#[derive(Clone, Copy)]
struct ScratchSlot {
    base: u64,
    // A vfork child uses its suspended parent's slot in the shared mm. Its
    // exit or exec must not release that slot while the parent still owns it.
    borrowed: bool,
}

#[derive(Default)]
struct ScratchSpace {
    all: Vec<u64>,
    free: Vec<u64>,
}

struct Trace<'a> {
    view: &'a mut View,
    tasks: HashSet<i32>,
    startup_stops: HashSet<i32>,
    early_stops: HashSet<i32>,
    reaped_early: HashSet<i32>,
    groups: HashMap<i32, i32>,
    pending: HashMap<i32, Pending>,
    fds: HashMap<i32, HashMap<i32, Translation>>,
    dup_reservations: HashMap<i32, (i32, Translation)>,
    fd_busy: HashMap<i32, i32>,
    fd_waiting: VecDeque<(i32, i32)>,
    cwd: HashMap<i32, PathBuf>,
    scratch: HashMap<i32, ScratchSlot>,
    scratch_pending: HashMap<i32, Registers>,
    task_spaces: HashMap<i32, u64>,
    spaces: HashMap<u64, ScratchSpace>,
    next_space: u64,
    spawn_vm: HashMap<i32, bool>,
    watch: InputWatch,
    active_markers: HashSet<PathBuf>,
    root: i32,
    root_result: Option<i32>,
    root_exit_code: Option<i32>,
    root_exec: bool,
}

impl Trace<'_> {
    fn release_fd_barrier(&mut self, pid: i32) -> Result<()> {
        let group = self.groups.get(&pid).copied().unwrap_or(pid);
        if self.fd_busy.get(&group) != Some(&pid) {
            return Ok(());
        }
        self.fd_busy.remove(&group);
        while let Some(index) = self
            .fd_waiting
            .iter()
            .position(|(owner, _)| *owner == group)
        {
            let (_, next) = self.fd_waiting.remove(index).ok_or_else(injection_failed)?;
            if self.tasks.contains(&next) {
                tracing::trace!(
                    action = "linux_fd_barrier_resume",
                    group,
                    next,
                    "Resuming queued descriptor syscall"
                );
                self.fd_busy.insert(group, next);
                return if self.pending.contains_key(&next) {
                    resume(next, true, 0)
                } else {
                    self.enter(next)
                };
            }
        }
        Ok(())
    }

    fn resume_fd_sensitive(&mut self, pid: i32) -> Result<()> {
        self.pending.insert(pid, Pending::Ordinary);
        resume(pid, true, 0)
    }

    fn descriptor(&self, pid: i32, fd: i32) -> Option<Translation> {
        let group = Self::group(pid);
        if let Some(entry) = self.fds.get(&group).and_then(|fds| fds.get(&fd)) {
            return Some(entry.clone());
        }
        if let Some((_, entry)) =
            self.dup_reservations
                .iter()
                .find_map(|(task, (target, entry))| {
                    (Self::group(*task) == group && *target == fd).then_some((task, entry))
                })
        {
            return Some(entry.clone());
        }
        // The kernel installs a newly opened or duplicated descriptor before
        // its owner reaches the exit stop. Another thread can reach a seccomp
        // stop first, so also inspect the live descriptor before admitting a
        // mutation or a descriptor alias.
        let target = fs::read_link(format!("/proc/{pid}/fd/{fd}")).ok()?;
        if !target.is_absolute()
            || !(target.starts_with(&self.view.cache.root)
                || target.starts_with(self.view.session.join("views"))
                || self.view.graph.managed(&target))
        {
            return None;
        }
        Some(Translation {
            logical: target.clone(),
            physical: target,
            readonly: true,
            virtual_link: false,
        })
    }

    fn seed_inherited_descriptors(&mut self) -> Result<()> {
        let pid = self.root;
        let cache_root = fs::canonicalize(&self.view.cache.root).map_err(|_| injection_failed())?;
        let session_root = fs::canonicalize(&self.view.session).map_err(|_| injection_failed())?;
        let mut inherited = HashMap::new();
        for entry in fs::read_dir(format!("/proc/{pid}/fd")).map_err(|_| injection_failed())? {
            let entry = entry.map_err(|_| injection_failed())?;
            let Some(fd) = entry
                .file_name()
                .to_str()
                .and_then(|name| name.parse::<i32>().ok())
            else {
                continue;
            };
            let target = fs::read_link(entry.path()).map_err(|_| injection_failed())?;
            if !target.is_absolute()
                || !(target.starts_with(&cache_root)
                    || target.starts_with(session_root.join("views"))
                    || self.view.graph.managed(&target))
            {
                continue;
            }
            let info = fs::read_to_string(format!("/proc/{pid}/fdinfo/{fd}"))
                .map_err(|_| injection_failed())?;
            let flags = info
                .lines()
                .find_map(|line| line.strip_prefix("flags:"))
                .and_then(|value| i32::from_str_radix(value.trim(), 8).ok())
                .ok_or_else(injection_failed)?;
            if flags & libc::O_ACCMODE != libc::O_RDONLY {
                return Err(unsupported(
                    "An inherited writable dependency descriptor cannot be mediated.",
                ));
            }
            tracing::debug!(
                action = "linux_inherited_descriptor",
                pid,
                fd,
                "Tracking an inherited managed descriptor"
            );
            inherited.insert(
                fd,
                Translation {
                    logical: target.clone(),
                    physical: target,
                    readonly: true,
                    virtual_link: false,
                },
            );
        }
        self.fds.insert(pid, inherited);
        Ok(())
    }

    fn scratch_base(&self, pid: i32) -> Result<u64> {
        self.scratch
            .get(&pid)
            .map(|slot| slot.base)
            .ok_or_else(injection_failed)
    }

    fn new_space(&mut self, inherited: Vec<u64>) -> Result<u64> {
        let id = self.next_space;
        self.next_space = id.checked_add(1).ok_or_else(injection_failed)?;
        self.spaces.insert(
            id,
            ScratchSpace {
                free: inherited.clone(),
                all: inherited,
            },
        );
        Ok(id)
    }

    fn release_task_space(&mut self, pid: i32) {
        self.scratch_pending.remove(&pid);
        self.spawn_vm.remove(&pid);
        let space = self.task_spaces.remove(&pid);
        if let (Some(space), Some(slot)) = (space, self.scratch.remove(&pid)) {
            if !slot.borrowed {
                if let Some(state) = self.spaces.get_mut(&space) {
                    state.free.push(slot.base);
                }
            }
        }
        if let Some(space) = space {
            if !self.task_spaces.values().any(|current| *current == space) {
                self.spaces.remove(&space);
            }
        }
    }

    fn register_child_space(
        &mut self,
        parent: i32,
        child: i32,
        shares_vm: bool,
        vfork: bool,
    ) -> Result<()> {
        let parent_space = *self.task_spaces.get(&parent).ok_or_else(injection_failed)?;
        if shares_vm {
            self.task_spaces.insert(child, parent_space);
            if vfork {
                // The parent cannot run until this child exits or execs, so
                // both can use the same slot without concurrent rewrites.
                let slot = *self.scratch.get(&parent).ok_or_else(injection_failed)?;
                self.scratch.insert(
                    child,
                    ScratchSlot {
                        base: slot.base,
                        borrowed: true,
                    },
                );
            }
        } else {
            // fork copies existing mappings into a private mm. Every copied
            // slot is available to the child independently of the parent.
            let inherited = self
                .spaces
                .get(&parent_space)
                .ok_or_else(injection_failed)?
                .all
                .clone();
            let space = self.new_space(inherited)?;
            self.task_spaces.insert(child, space);
        }
        Ok(())
    }

    fn rewrite_path(
        &self,
        pid: i32,
        regs: &mut Registers,
        index: usize,
        path: &Path,
    ) -> Result<()> {
        self.rewrite_path_slot(pid, regs, index, path, 0)
    }

    fn rewrite_path_slot(
        &self,
        pid: i32,
        regs: &mut Registers,
        index: usize,
        path: &Path,
        slot: usize,
    ) -> Result<()> {
        let top = self
            .scratch_base(pid)?
            .checked_add(SCRATCH_SIZE as u64)
            .ok_or_else(injection_failed)?;
        write_path_at(pid, regs, index, path, slot, top)
    }

    fn start_scratch(&mut self, pid: i32) -> Result<()> {
        let space = *self.task_spaces.get(&pid).ok_or_else(injection_failed)?;
        if let Some(base) = self
            .spaces
            .get_mut(&space)
            .ok_or_else(injection_failed)?
            .free
            .pop()
        {
            self.scratch.insert(
                pid,
                ScratchSlot {
                    base,
                    borrowed: false,
                },
            );
            tracing::debug!(
                action = "linux_scratch_reuse",
                pid,
                "Reusing owned tracee scratch"
            );
            return self.enter(pid);
        }
        let original = registers(pid)?;
        let mut mapped = original;
        set_number(&mut mapped, libc::SYS_mmap);
        for (index, value) in [
            0,
            SCRATCH_SIZE as u64,
            (libc::PROT_READ | libc::PROT_WRITE) as u64,
            (libc::MAP_PRIVATE | libc::MAP_ANONYMOUS) as u64,
            u64::MAX,
            0,
        ]
        .into_iter()
        .enumerate()
        {
            set_argument(&mut mapped, index, value);
        }
        set_registers(pid, &mapped)?;
        #[cfg(target_arch = "aarch64")]
        set_active_syscall(pid, libc::SYS_mmap)?;
        self.scratch_pending.insert(pid, original);
        tracing::debug!(
            action = "linux_scratch_alloc",
            pid,
            "Allocating owned tracee scratch"
        );
        resume(pid, true, 0)
    }

    fn finish_scratch(&mut self, pid: i32, mut original: Registers) -> Result<()> {
        let mapped = result(&registers(pid)?);
        tracing::debug!(
            action = "linux_scratch_result",
            pid,
            mapped,
            "Tracee mmap result"
        );
        if mapped <= 0 || !(mapped as u64).is_multiple_of(4096) {
            return Err(unsupported("Linux tracee scratch allocation failed."));
        }
        let space = *self.task_spaces.get(&pid).ok_or_else(injection_failed)?;
        self.spaces
            .get_mut(&space)
            .ok_or_else(injection_failed)?
            .all
            .push(mapped as u64);
        self.scratch.insert(
            pid,
            ScratchSlot {
                base: mapped as u64,
                borrowed: false,
            },
        );
        prepare_replayed_syscall(&mut original)?;
        set_registers(pid, &original)?;
        tracing::debug!(
            action = "linux_scratch_ready",
            pid,
            "Owned tracee scratch is ready"
        );
        resume(pid, false, 0)
    }

    fn group(pid: i32) -> i32 {
        fs::read_to_string(format!("/proc/{pid}/status"))
            .ok()
            .and_then(|body| {
                body.lines()
                    .find_map(|line| line.strip_prefix("Tgid:")?.trim().parse::<i32>().ok())
            })
            .unwrap_or(pid)
    }

    fn base(&self, pid: i32, dirfd: i32, path: &Path) -> Result<PathBuf> {
        if path.is_absolute() {
            return Ok(path.to_owned());
        }
        let group = Self::group(pid);
        let root = if dirfd == libc::AT_FDCWD {
            self.cwd
                .get(&group)
                .cloned()
                .unwrap_or_else(|| fs::read_link(format!("/proc/{pid}/cwd")).unwrap_or_default())
        } else if let Some(entry) = self.descriptor(pid, dirfd) {
            // A native directory descriptor survives rename and symlink
            // retargeting. Only materialized virtual paths need the saved
            // logical identity for relative lookup.
            if entry.logical != entry.physical {
                entry.logical.clone()
            } else {
                fs::read_link(format!("/proc/{pid}/fd/{dirfd}")).map_err(|_| injection_failed())?
            }
        } else {
            fs::read_link(format!("/proc/{pid}/fd/{dirfd}")).map_err(|_| injection_failed())?
        };
        if root.as_os_str().is_empty() {
            return Err(injection_failed());
        }
        Ok(root.join(path))
    }

    fn translate(&mut self, lookup: &Path) -> Result<Translation> {
        self.translate_view(lookup).inspect_err(|error| {
            if error.code == Code::PnportResolutionFailed {
                tracing::debug!(action = "linux_resolution_miss", path = %lookup.display(),
                    "Owned child path was not present in the PnP view");
            } else if super::supervisor::handled_signal() == 0 {
                let _ = fs::write(self.view.session.join("failure"), error.code.as_str());
            }
        })
    }

    fn source_path(&self, pid: i32, dirfd: i32, path: &Path) -> Result<PathBuf> {
        let absolute = self.base(pid, dirfd, path)?;
        Ok(self.proc_root(pid, &absolute)?.unwrap_or(absolute))
    }

    fn translate_view(&mut self, path: &Path) -> Result<Translation> {
        let session = self.view.session.clone();
        let watch = &mut self.watch;
        self.view.translate_with_wait(path, &mut || {
            if super::supervisor::handled_signal() != 0 {
                return Err(injection_failed());
            }
            if let Some(error) = super::supervisor::runtime_failure(&session)? {
                return Err(error);
            }
            watch.poll()
        })
    }

    fn missing_path_errno(&mut self, source: &Path, writing: bool) -> Result<i32> {
        if writing {
            if let Some(parent) = source.parent() {
                match self.translate_view(parent) {
                    Ok(parent) if parent.readonly && parent.physical.is_dir() => {
                        return Ok(libc::EROFS);
                    }
                    Ok(_) => {}
                    Err(error) if error.code == Code::PnportResolutionFailed => {}
                    Err(error) => return Err(error),
                }
            }
        }
        Ok(libc::ENOENT)
    }

    fn virtual_link_backing(
        &mut self,
        pid: i32,
        dirfd: i32,
        original: &Path,
    ) -> Result<(PathBuf, PathBuf)> {
        let logical = pnport::graph::normalize(&self.base(pid, dirfd, original)?);
        let parent = self.translate_view(logical.parent().ok_or_else(injection_failed)?)?;
        let link = parent
            .physical
            .join(logical.file_name().ok_or_else(injection_failed)?);
        if !fs::symlink_metadata(&link)
            .map_err(|_| injection_failed())?
            .file_type()
            .is_symlink()
        {
            return Err(injection_failed());
        }
        Ok((logical, link))
    }

    fn proc_root(&self, pid: i32, path: &Path) -> Result<Option<PathBuf>> {
        let Some(text) = path.to_str() else {
            return Ok(None);
        };
        let Some((owner, remainder)) = text.strip_prefix("/proc/").and_then(|p| p.split_once('/'))
        else {
            return Ok(None);
        };
        let group = Self::group(pid);
        let owner_group = match owner {
            "self" | "thread-self" => group,
            _ => match owner.parse::<i32>() {
                Ok(owner) => Self::group(owner),
                Err(_) => return Ok(None),
            },
        };
        if owner_group != group {
            return Ok(None);
        }
        let suffix = if let Some(suffix) = remainder.strip_prefix("root/") {
            suffix
        } else if let Some((task, suffix)) = remainder
            .strip_prefix("task/")
            .and_then(|value| value.split_once("/root/"))
        {
            if task
                .parse::<i32>()
                .ok()
                .map(Self::group)
                .is_none_or(|task_group| task_group != group)
            {
                return Ok(None);
            }
            suffix
        } else {
            return Ok(None);
        };
        let root = fs::read_link(format!("/proc/{pid}/root")).map_err(|_| injection_failed())?;
        Ok(Some(root.join(suffix.trim_start_matches('/'))))
    }

    fn proc_descriptor(&self, pid: i32, path: &Path) -> Option<(Translation, bool)> {
        let text = path.to_str()?;
        let group = Self::group(pid);
        let remainder = if let Some(remainder) = text.strip_prefix("/dev/fd/") {
            remainder
        } else if let Some(fd) = match text {
            "/dev/stdin" => Some("0"),
            "/dev/stdout" => Some("1"),
            "/dev/stderr" => Some("2"),
            _ => None,
        } {
            fd
        } else {
            let (owner, remainder) = text.strip_prefix("/proc/")?.split_once('/')?;
            let owner_group = match owner {
                "self" | "thread-self" => group,
                _ => Self::group(owner.parse::<i32>().ok()?),
            };
            if owner_group != group {
                return None;
            }
            if let Some(remainder) = remainder.strip_prefix("fd/") {
                remainder
            } else {
                let (task, remainder) = remainder.strip_prefix("task/")?.split_once("/fd/")?;
                if Self::group(task.parse::<i32>().ok()?) != group {
                    return None;
                }
                remainder
            }
        };
        let (number, suffix) = remainder
            .split_once('/')
            .map_or((remainder, None), |(number, suffix)| (number, Some(suffix)));
        let fd = number.parse::<i32>().ok()?;
        let mut entry = self.descriptor(pid, fd)?;
        if let Some(suffix) = suffix {
            entry.logical.push(suffix);
            entry.physical.push(suffix);
            entry.virtual_link = false;
        }
        Some((entry, suffix.is_none()))
    }

    fn proc_executable(&mut self, pid: i32, path: &Path) -> Result<Option<Translation>> {
        let Some(text) = path.to_str() else {
            return Ok(None);
        };
        let Some((owner, remainder)) = text.strip_prefix("/proc/").and_then(|p| p.split_once('/'))
        else {
            return Ok(None);
        };
        let group = Self::group(pid);
        let owner_group = match owner {
            "self" | "thread-self" => group,
            _ => owner
                .parse::<i32>()
                .ok()
                .map(Self::group)
                .unwrap_or_default(),
        };
        if owner_group != group {
            return Ok(None);
        }
        let own_executable = remainder == "exe"
            || remainder
                .strip_prefix("task/")
                .and_then(|task| task.strip_suffix("/exe"))
                .and_then(|task| task.parse::<i32>().ok())
                .is_some_and(|task| Self::group(task) == group);
        if !own_executable {
            return Ok(None);
        }
        let physical = fs::read_link(format!("/proc/{pid}/exe")).map_err(|_| injection_failed())?;
        self.translate_view(&physical).map(Some)
    }

    fn proc_cwd(&self, pid: i32, path: &Path) -> Option<(PathBuf, bool)> {
        let (owner, remainder) = path.to_str()?.strip_prefix("/proc/")?.split_once('/')?;
        let group = Self::group(pid);
        let owner_group = match owner {
            "self" | "thread-self" => group,
            _ => Self::group(owner.parse::<i32>().ok()?),
        };
        if owner_group != group {
            return None;
        }
        let remainder = if let Some(remainder) = remainder.strip_prefix("task/") {
            let (task, path) = remainder.split_once('/')?;
            if Self::group(task.parse::<i32>().ok()?) != group {
                return None;
            }
            path
        } else {
            remainder
        };
        let (suffix, exact) = if remainder == "cwd" {
            (None, true)
        } else {
            (Some(remainder.strip_prefix("cwd/")?), false)
        };
        let mut logical = self.cwd.get(&group)?.clone();
        if let Some(suffix) = suffix {
            logical.push(suffix);
        }
        Some((logical, exact))
    }

    fn prepare_script_exec(
        &mut self,
        pid: i32,
        regs: &mut Registers,
        translation: &Translation,
        path_arg: usize,
        argv_arg: usize,
        empty_path: bool,
    ) -> Result<bool> {
        if translation.logical == translation.physical {
            return Ok(false);
        }
        let mut magic = [0u8; 2];
        if File::open(&translation.physical)
            .and_then(|mut file| file.read_exact(&mut magic))
            .is_err()
            || magic != *b"#!"
        {
            return Ok(false);
        }
        let original_argv = match read_pointer_vector(pid, argument(regs, argv_arg))? {
            ChildRead::Value(argv) => argv,
            ChildRead::Fault => {
                self.force_error(pid, regs, path_arg, libc::EFAULT)?;
                return Ok(true);
            }
        };
        let search_path = match child_search_path(pid, argument(regs, argv_arg + 1))? {
            ChildRead::Value(path) => path,
            ChildRead::Fault => {
                self.force_error(pid, regs, path_arg, libc::EFAULT)?;
                return Ok(true);
            }
        };
        let cwd = self.base(pid, libc::AT_FDCWD, Path::new("."))?;
        let prepared = match pnport::executable::prepare_with_context(
            self.view,
            &translation.logical,
            &[],
            search_path.as_deref(),
            &cwd,
        ) {
            Ok(prepared) => prepared,
            Err(error) => {
                let errno = match error.exec_failure {
                    Some(ExecFailureKind::NotFound) => libc::ENOENT,
                    Some(ExecFailureKind::PermissionDenied) => libc::EACCES,
                    Some(ExecFailureKind::InvalidFormat) => libc::ENOEXEC,
                    Some(ExecFailureKind::InterpreterLoop) => libc::ELOOP,
                    None if error.code == Code::PnportResolutionFailed => libc::ENOENT,
                    None => return Err(error),
                };
                self.force_error(pid, regs, path_arg, errno)?;
                return Ok(true);
            }
        };
        if empty_path {
            // The replacement names an interpreter directly; the descriptor
            // no longer selects the executable after this rewrite.
            set_argument(regs, 0, libc::AT_FDCWD as u64);
            set_argument(regs, 4, 0);
        }
        self.rewrite_path(pid, regs, path_arg, &prepared.program)?;
        let path_address = argument(regs, path_arg);
        rewrite_exec_arguments(
            pid,
            regs,
            argv_arg,
            path_address,
            &prepared.args,
            &original_argv,
            self.scratch_base(pid)?,
        )?;
        self.pending.insert(pid, Pending::Ordinary);
        Ok(true)
    }

    fn path_call(&mut self, pid: i32, mut regs: Registers) -> Result<bool> {
        let call = number(&regs);
        let open_how = if call == libc::SYS_openat2 {
            let size = argument(&regs, 3);
            if size < OPEN_HOW_SIZE as u64 {
                return Ok(false);
            }
            let page_size = unsafe { libc::sysconf(libc::_SC_PAGESIZE) };
            if page_size <= 0 {
                return Err(injection_failed());
            }
            if size > page_size as u64 {
                // The kernel owns the error for an oversized structure.
                return Ok(false);
            }
            let bytes = match read_remote(pid, argument(&regs, 2), size as usize) {
                Ok(bytes) if bytes.len() == size as usize => bytes,
                Ok(_) => return Ok(false),
                Err(_) if io::Error::last_os_error().raw_os_error() == Some(libc::EFAULT) => {
                    return Ok(false);
                }
                Err(error) => return Err(error),
            };
            if bytes[OPEN_HOW_SIZE..].iter().any(|byte| *byte != 0) {
                // Unknown extension fields must not bypass virtual pathname
                // mediation, even if a newer kernel would recognize them.
                self.force_error(pid, &mut regs, 1, libc::E2BIG)?;
                return Ok(true);
            }
            Some(bytes)
        } else {
            None
        };
        let (path_arg, dirfd, writing) = match call {
            n if n == libc::SYS_openat => (
                1,
                argument(&regs, 0) as i32,
                argument(&regs, 2) as i32
                    & (libc::O_WRONLY
                        | libc::O_RDWR
                        | libc::O_CREAT
                        | libc::O_TRUNC
                        | libc::O_APPEND)
                    != 0,
            ),
            n if n == libc::SYS_openat2 => {
                let bytes = open_how.as_ref().ok_or_else(injection_failed)?;
                let flags = u64::from_ne_bytes(
                    bytes
                        .get(..8)
                        .ok_or_else(injection_failed)?
                        .try_into()
                        .unwrap(),
                );
                (
                    1,
                    argument(&regs, 0) as i32,
                    flags
                        & (libc::O_WRONLY
                            | libc::O_RDWR
                            | libc::O_CREAT
                            | libc::O_TRUNC
                            | libc::O_APPEND) as u64
                        != 0,
                )
            }
            n if n == libc::SYS_newfstatat
                || n == libc::SYS_statx
                || n == libc::SYS_readlinkat
                || n == libc::SYS_faccessat
                || n == libc::SYS_faccessat2
                || n == libc::SYS_execveat
                || n == libc::SYS_unlinkat
                || n == libc::SYS_mkdirat
                || n == libc::SYS_fchmodat
                || n == SYS_FCHMODAT2
                || n == libc::SYS_fchownat
                || n == libc::SYS_utimensat
                || n == SYS_FUTIMESAT
                || n == libc::SYS_mknodat
                || n == libc::SYS_linkat =>
            {
                let writing = n == libc::SYS_unlinkat
                    || n == libc::SYS_mkdirat
                    || n == libc::SYS_fchmodat
                    || n == SYS_FCHMODAT2
                    || n == libc::SYS_fchownat
                    || n == libc::SYS_utimensat
                    || n == SYS_FUTIMESAT
                    || n == libc::SYS_mknodat
                    || n == libc::SYS_linkat
                    || ((n == libc::SYS_faccessat || n == libc::SYS_faccessat2)
                        && argument(&regs, 2) as i32 & libc::W_OK != 0);
                (1, argument(&regs, 0) as i32, writing)
            }
            n if n == libc::SYS_symlinkat => (2, argument(&regs, 1) as i32, true),
            n if n == libc::SYS_execve
                || n == libc::SYS_chdir
                || n == libc::SYS_statfs
                || n == libc::SYS_getxattr
                || n == libc::SYS_lgetxattr
                || n == libc::SYS_listxattr
                || n == libc::SYS_llistxattr =>
            {
                (0, libc::AT_FDCWD, false)
            }
            n if n == libc::SYS_truncate
                || n == libc::SYS_setxattr
                || n == libc::SYS_lsetxattr
                || n == libc::SYS_removexattr
                || n == libc::SYS_lremovexattr =>
            {
                (0, libc::AT_FDCWD, true)
            }
            n if n == libc::SYS_inotify_add_watch => (1, libc::AT_FDCWD, false),
            n if n == libc::SYS_renameat || n == libc::SYS_renameat2 => {
                (1, argument(&regs, 0) as i32, true)
            }
            #[cfg(target_arch = "x86_64")]
            n if n == libc::SYS_open => (
                0,
                libc::AT_FDCWD,
                argument(&regs, 1) as i32
                    & (libc::O_WRONLY
                        | libc::O_RDWR
                        | libc::O_CREAT
                        | libc::O_TRUNC
                        | libc::O_APPEND)
                    != 0,
            ),
            #[cfg(target_arch = "x86_64")]
            n if n == libc::SYS_symlink => (1, libc::AT_FDCWD, true),
            #[cfg(target_arch = "x86_64")]
            n if matches!(
                n,
                libc::SYS_stat
                    | libc::SYS_lstat
                    | libc::SYS_access
                    | libc::SYS_readlink
                    | libc::SYS_unlink
                    | libc::SYS_mkdir
                    | libc::SYS_rename
                    | libc::SYS_creat
                    | libc::SYS_link
                    | libc::SYS_rmdir
                    | libc::SYS_chmod
                    | libc::SYS_chown
                    | libc::SYS_lchown
                    | libc::SYS_utime
                    | libc::SYS_utimes
                    | libc::SYS_mknod
            ) =>
            {
                (
                    0,
                    libc::AT_FDCWD,
                    matches!(
                        n,
                        libc::SYS_unlink
                            | libc::SYS_mkdir
                            | libc::SYS_rename
                            | libc::SYS_creat
                            | libc::SYS_link
                            | libc::SYS_rmdir
                            | libc::SYS_chmod
                            | libc::SYS_chown
                            | libc::SYS_lchown
                            | libc::SYS_utime
                            | libc::SYS_utimes
                            | libc::SYS_mknod
                    ) || n == libc::SYS_access && argument(&regs, 1) as i32 & libc::W_OK != 0,
                )
            }
            _ => return Ok(false),
        };
        let openat2_resolve = if call == libc::SYS_openat2 {
            let how = open_how.as_ref().ok_or_else(injection_failed)?;
            u64::from_ne_bytes(
                how.get(16..OPEN_HOW_SIZE)
                    .ok_or_else(injection_failed)?
                    .try_into()
                    .map_err(|_| injection_failed())?,
            )
        } else {
            0
        };
        let Some(original) = read_path(pid, argument(&regs, path_arg))? else {
            // The kernel owns EFAULT for invalid child pathname pointers.
            return Ok(false);
        };
        let second = if call == libc::SYS_renameat
            || call == libc::SYS_renameat2
            || call == libc::SYS_linkat
        {
            Some((3, argument(&regs, 2) as i32))
        } else {
            #[cfg(target_arch = "x86_64")]
            {
                if call == libc::SYS_rename || call == libc::SYS_link {
                    Some((1, libc::AT_FDCWD))
                } else {
                    None
                }
            }
            #[cfg(target_arch = "aarch64")]
            {
                None
            }
        };
        let second_original = if let Some((other_arg, _)) = second {
            let Some(other) = read_path(pid, argument(&regs, other_arg))? else {
                return Ok(false);
            };
            Some(other)
        } else {
            None
        };
        let symlink_call = call == libc::SYS_symlinkat || {
            #[cfg(target_arch = "x86_64")]
            {
                call == libc::SYS_symlink
            }
            #[cfg(target_arch = "aarch64")]
            {
                false
            }
        };
        if symlink_call && argument(&regs, 0) == 0 {
            return Ok(false);
        }
        if original.as_os_str().is_empty() {
            if call == libc::SYS_readlinkat {
                // Empty-path readlinkat targets an O_PATH symlink descriptor,
                // not a directory relative to that descriptor.
                if let Some(link) = self
                    .descriptor(pid, dirfd)
                    .as_ref()
                    .filter(|entry| entry.virtual_link)
                    .map(|entry| entry.logical.clone())
                {
                    let target = self.translate_view(&link)?.logical;
                    self.pending.insert(
                        pid,
                        Pending::ReadLink {
                            output: argument(&regs, 2),
                            capacity: argument(&regs, 3) as usize,
                            target,
                        },
                    );
                    return Ok(true);
                }
                return Ok(false);
            }
            let flags = match call {
                n if n == libc::SYS_newfstatat
                    || n == libc::SYS_faccessat2
                    || n == libc::SYS_utimensat
                    || n == SYS_FCHMODAT2 =>
                {
                    argument(&regs, 3)
                }
                n if n == libc::SYS_statx => argument(&regs, 2),
                n if n == libc::SYS_fchownat
                    || n == libc::SYS_execveat
                    || n == libc::SYS_linkat =>
                {
                    argument(&regs, 4)
                }
                _ => 0,
            };
            if flags as i32 & libc::AT_EMPTY_PATH != 0 {
                if call == libc::SYS_execveat {
                    let translation = self.descriptor(pid, dirfd);
                    if let Some(translation) = translation {
                        if self.prepare_script_exec(
                            pid,
                            &mut regs,
                            &translation,
                            path_arg,
                            2,
                            true,
                        )? {
                            return Ok(true);
                        }
                    }
                }
                let readonly = self
                    .descriptor(pid, dirfd)
                    .is_some_and(|entry| entry.readonly);
                if writing && readonly {
                    self.force_error(pid, &mut regs, path_arg, libc::EROFS)?;
                    return Ok(true);
                }
                if call == libc::SYS_linkat {
                    let target_fd = argument(&regs, 2) as i32;
                    let target_arg = 3;
                    let Some(target) = second_original.as_ref() else {
                        return Ok(false);
                    };
                    if !target.is_absolute() && target_fd != libc::AT_FDCWD {
                        match fs::metadata(format!("/proc/{pid}/fd/{target_fd}")) {
                            Ok(metadata) if !metadata.is_dir() => {
                                self.force_error(pid, &mut regs, target_arg, libc::ENOTDIR)?;
                                return Ok(true);
                            }
                            Err(_) => {
                                self.force_error(pid, &mut regs, target_arg, libc::EBADF)?;
                                return Ok(true);
                            }
                            _ => {}
                        }
                    }
                    let source = self.source_path(pid, target_fd, target)?;
                    let Some(lookup) = resolved_caller_lookup(&source, false, &self.view.graph)
                    else {
                        return Ok(false);
                    };
                    let translated = match self.translate(&lookup) {
                        Ok(value) => value,
                        Err(error) if error.code == Code::PnportResolutionFailed => {
                            self.force_error(pid, &mut regs, target_arg, libc::ENOENT)?;
                            return Ok(true);
                        }
                        Err(error) => return Err(error),
                    };
                    if translated.readonly {
                        self.force_error(pid, &mut regs, target_arg, libc::EROFS)?;
                        return Ok(true);
                    }
                    if lookup != translated.physical {
                        self.rewrite_path(pid, &mut regs, target_arg, &translated.physical)?;
                    }
                    self.pending.insert(pid, Pending::Ordinary);
                    return Ok(true);
                }
                return Ok(false);
            }
        }
        let is_open = call == libc::SYS_openat || call == libc::SYS_openat2 || call == SYS_OPEN;
        let open_flags = if call == libc::SYS_openat2 {
            let how = open_how.as_ref().ok_or_else(injection_failed)?;
            Some(i32::from_ne_bytes(
                how.get(..4)
                    .ok_or_else(injection_failed)?
                    .try_into()
                    .map_err(|_| injection_failed())?,
            ))
        } else if call == libc::SYS_openat {
            Some(argument(&regs, 2) as i32)
        } else if call == SYS_OPEN {
            Some(argument(&regs, 1) as i32)
        } else {
            None
        };
        let in_root = openat2_resolve & RESOLVE_IN_ROOT != 0;
        if openat2_resolve & RESOLVE_BENEATH != 0
            && (original.is_absolute() || escapes_beneath(&original))
        {
            // Keep the kernel's EXDEV result for paths that escape dirfd;
            // normalizing these first would erase the attempted traversal.
            return Ok(false);
        }
        if original.as_os_str().is_empty()
            && (call == libc::SYS_newfstatat || call == libc::SYS_statx)
        {
            // Empty-path descriptor operations can target regular files.
            return Ok(false);
        }
        if (!original.is_absolute() || in_root) && dirfd != libc::AT_FDCWD {
            let descriptor = format!("/proc/{pid}/fd/{dirfd}");
            match fs::metadata(descriptor) {
                Ok(metadata) if !metadata.is_dir() => {
                    self.force_error(pid, &mut regs, path_arg, libc::ENOTDIR)?;
                    return Ok(true);
                }
                Err(_) => {
                    self.force_error(pid, &mut regs, path_arg, libc::EBADF)?;
                    return Ok(true);
                }
                _ => {}
            }
        }
        if let Some(executable) = self.proc_executable(pid, &original)? {
            if writing && executable.readonly {
                self.force_error(pid, &mut regs, path_arg, libc::EROFS)?;
                return Ok(true);
            }
            if is_open {
                self.pending.insert(pid, Pending::Open(executable));
                return Ok(true);
            }
            return Ok(false);
        }
        if let Some((descriptor, exact_fd)) = (!in_root)
            .then(|| self.proc_descriptor(pid, &original))
            .flatten()
        {
            if exact_fd && (call == libc::SYS_readlinkat || call == SYS_READLINK) {
                let (output, capacity) = if call == libc::SYS_readlinkat {
                    (argument(&regs, 2), argument(&regs, 3) as usize)
                } else {
                    (argument(&regs, 1), argument(&regs, 2) as usize)
                };
                tracing::debug!(
                    action = "linux_proc_fd",
                    pid,
                    "Restoring a logical descriptor path"
                );
                self.pending.insert(
                    pid,
                    Pending::ReadLink {
                        output,
                        capacity,
                        target: descriptor.logical,
                    },
                );
                return Ok(true);
            }
            if writing && descriptor.readonly {
                self.force_error(pid, &mut regs, path_arg, libc::EROFS)?;
                return Ok(true);
            }
            if call == libc::SYS_chdir {
                let logical = if descriptor.logical == descriptor.physical {
                    None
                } else if exact_fd {
                    Some(descriptor.logical)
                } else {
                    // A suffix can contain relative components or virtual links.
                    Some(self.translate_view(&descriptor.logical)?.logical)
                };
                self.pending.insert(pid, Pending::ChangeDirectory(logical));
                return Ok(true);
            }
            if is_open {
                // The kernel follows /proc/self/fd to the materialized file.
                // Retain the logical ownership on the newly opened descriptor.
                self.pending.insert(pid, Pending::Open(descriptor));
                return Ok(true);
            }
            return Ok(false);
        }
        let proc_cwd = (!in_root).then(|| self.proc_cwd(pid, &original)).flatten();
        if let Some((logical, true)) = &proc_cwd {
            if call == libc::SYS_readlinkat || call == SYS_READLINK {
                let (output, capacity) = if call == libc::SYS_readlinkat {
                    (argument(&regs, 2), argument(&regs, 3) as usize)
                } else {
                    (argument(&regs, 1), argument(&regs, 2) as usize)
                };
                self.pending.insert(
                    pid,
                    Pending::ReadLink {
                        output,
                        capacity,
                        target: logical.clone(),
                    },
                );
                return Ok(true);
            }
        }
        let source = if let Some((logical, _)) = &proc_cwd {
            logical.clone()
        } else if in_root && original.is_absolute() {
            let base = self.base(pid, dirfd, Path::new("."))?;
            base.join(original.strip_prefix("/").map_err(|_| injection_failed())?)
        } else {
            self.source_path(pid, dirfd, &original)?
        };
        let Some(lookup) = resolved_caller_lookup(
            &source,
            follows_final_component(call, &regs, path_arg, open_flags),
            &self.view.graph,
        ) else {
            return Ok(false);
        };
        let mutating = writing
            && call != libc::SYS_faccessat
            && call != libc::SYS_faccessat2
            && call != SYS_ACCESS;
        let translation = match self.translate(&lookup) {
            Ok(value) => value,
            Err(error) if error.code == Code::PnportResolutionFailed => {
                let errno = self.missing_path_errno(&lookup, mutating)?;
                self.force_error(pid, &mut regs, path_arg, errno)?;
                return Ok(true);
            }
            Err(error) => return Err(error),
        };
        if call == libc::SYS_execve || call == libc::SYS_execveat {
            let argv_arg = if call == libc::SYS_execve { 1 } else { 2 };
            if self.prepare_script_exec(pid, &mut regs, &translation, path_arg, argv_arg, false)? {
                return Ok(true);
            }
        }
        if is_open && translation.virtual_link {
            let flags = open_flags.ok_or_else(injection_failed)?;
            if flags & libc::O_NOFOLLOW != 0 {
                if flags & libc::O_PATH == 0 {
                    self.force_error(pid, &mut regs, path_arg, libc::ELOOP)?;
                    return Ok(true);
                }
                if openat2_resolve != 0 {
                    return Err(unsupported(
                        "Constrained openat2 cannot open a virtual link without following it.",
                    ));
                }
                let (logical, link) = self.virtual_link_backing(pid, dirfd, &original)?;
                self.rewrite_path(pid, &mut regs, path_arg, &link)?;
                self.pending.insert(
                    pid,
                    Pending::Open(Translation {
                        logical,
                        physical: link,
                        readonly: true,
                        virtual_link: true,
                    }),
                );
                return Ok(true);
            }
        }
        if call == libc::SYS_inotify_add_watch
            && translation.virtual_link
            && argument(&regs, 2) as u32 & libc::IN_DONT_FOLLOW != 0
        {
            let (_, link) = self.virtual_link_backing(pid, dirfd, &original)?;
            self.rewrite_path(pid, &mut regs, path_arg, &link)?;
            self.pending.insert(pid, Pending::Ordinary);
            return Ok(true);
        }
        if writing && translation.readonly {
            self.force_error(pid, &mut regs, path_arg, libc::EROFS)?;
            return Ok(true);
        }
        let second_translation =
            if let Some(((other_arg, other_fd), other)) = second.zip(second_original) {
                if !other.is_absolute() && other_fd != libc::AT_FDCWD {
                    let descriptor = format!("/proc/{pid}/fd/{other_fd}");
                    let error = match fs::metadata(descriptor) {
                        Ok(metadata) if !metadata.is_dir() => Some(libc::ENOTDIR),
                        Err(_) => Some(libc::EBADF),
                        _ => None,
                    };
                    if let Some(error) = error {
                        self.force_error(pid, &mut regs, path_arg, error)?;
                        return Ok(true);
                    }
                }
                let source = self.source_path(pid, other_fd, &other)?;
                let Some(other_lookup) = resolved_caller_lookup(&source, false, &self.view.graph)
                else {
                    return Ok(false);
                };
                let translated = match self.translate(&other_lookup) {
                    Ok(value) => value,
                    Err(error) if error.code == Code::PnportResolutionFailed => {
                        let errno = self.missing_path_errno(&other_lookup, true)?;
                        self.force_error(pid, &mut regs, path_arg, errno)?;
                        return Ok(true);
                    }
                    Err(error) => return Err(error),
                };
                if translated.readonly {
                    self.force_error(pid, &mut regs, path_arg, libc::EROFS)?;
                    return Ok(true);
                }
                Some((other_arg, other_lookup, translated))
            } else {
                None
            };
        let changed = if openat2_resolve != 0 {
            // openat2's resolve flags apply to the path relative to its real
            // dirfd. Keep that spelling when it already names the translated
            // file; a virtual target outside this root cannot be mediated
            // while preserving the kernel's confinement contract.
            let base = if dirfd == libc::AT_FDCWD {
                fs::read_link(format!("/proc/{pid}/cwd"))
            } else {
                fs::read_link(format!("/proc/{pid}/fd/{dirfd}"))
            }
            .map_err(|_| injection_failed())?;
            let relative = if in_root && original.is_absolute() {
                original.strip_prefix("/").map_err(|_| injection_failed())?
            } else {
                original.as_path()
            };
            if lookup != translation.physical
                && pnport::graph::normalize(&base.join(relative)) != translation.physical
            {
                return Err(unsupported(
                    "Constrained openat2 resolution cannot preserve this virtual path.",
                ));
            }
            false
        } else {
            // View.logical identifies the resolved package and can equal its
            // physical path for an unplugged dependency. Compare the caller's
            // lookup path instead, while leaving native relative spelling in
            // place for the kernel's symlink and trailing-slash semantics.
            lookup != translation.physical
        };
        if changed {
            self.rewrite_path(pid, &mut regs, path_arg, &translation.physical)?;
        }
        if let Some((other_arg, other_lookup, translated)) = second_translation {
            if other_lookup != translated.physical {
                self.rewrite_path_slot(pid, &mut regs, other_arg, &translated.physical, 1)?;
            }
        }
        if is_open {
            self.pending.insert(pid, Pending::Open(translation));
        } else if call == libc::SYS_chdir {
            self.pending.insert(
                pid,
                Pending::ChangeDirectory(
                    (translation.logical != translation.physical).then_some(translation.logical),
                ),
            );
        } else if call == libc::SYS_newfstatat || call == libc::SYS_statx || call == SYS_LSTAT {
            let nofollow = call == libc::SYS_newfstatat
                && argument(&regs, 3) as i32 & libc::AT_SYMLINK_NOFOLLOW != 0
                || call == libc::SYS_statx
                    && argument(&regs, 2) as i32 & libc::AT_SYMLINK_NOFOLLOW != 0
                || call == SYS_LSTAT;
            if nofollow && translation.virtual_link {
                let output = if call == libc::SYS_statx {
                    argument(&regs, 4)
                } else if call == libc::SYS_newfstatat {
                    argument(&regs, 2)
                } else {
                    argument(&regs, 1)
                };
                // View.logical is the resolved PnP target, also exposed by
                // the preload's virtual readlink and lstat implementation.
                let target_len = translation.logical.as_os_str().as_bytes().len();
                self.pending.insert(
                    pid,
                    Pending::LinkMetadata {
                        output,
                        target_len,
                        statx: call == libc::SYS_statx,
                    },
                );
            } else {
                self.pending.insert(pid, Pending::Ordinary);
            }
        } else if call == libc::SYS_readlinkat || call == SYS_READLINK {
            if translation.virtual_link {
                let (output, capacity) = if call == libc::SYS_readlinkat {
                    (argument(&regs, 2), argument(&regs, 3) as usize)
                } else {
                    (argument(&regs, 1), argument(&regs, 2) as usize)
                };
                let target = translation.logical;
                self.pending.insert(
                    pid,
                    Pending::ReadLink {
                        output,
                        capacity,
                        target,
                    },
                );
            } else {
                self.pending.insert(pid, Pending::Ordinary);
            }
        } else {
            self.pending.insert(pid, Pending::Ordinary);
        }
        // A successful exec has no syscall-exit stop. The exec event clears
        // its pending marker; all other calls return through this stop.
        Ok(true)
    }

    fn force_error(
        &mut self,
        pid: i32,
        regs: &mut Registers,
        index: usize,
        errno: i32,
    ) -> Result<()> {
        // Every path operation, including O_CREAT, rename, and symlink, must
        // fail in the kernel before its reported errno is replaced below.
        let denied = Path::new("/dev/null/pnport-denied");
        self.rewrite_path(pid, regs, index, denied)?;
        self.pending.insert(pid, Pending::ForcedError(errno));
        Ok(())
    }

    fn enter(&mut self, pid: i32) -> Result<()> {
        let mut regs = registers(pid)?;
        let call = number(&regs);
        tracing::trace!(
            action = "linux_syscall",
            pid,
            call,
            "Intercepted owned child syscall"
        );
        if matches!(
            call,
            libc::SYS_fanotify_mark
                | libc::SYS_open_by_handle_at
                | libc::SYS_io_uring_setup
                | libc::SYS_io_uring_enter
                | libc::SYS_io_uring_register
        ) {
            deny_syscall(pid, &mut regs)?;
            return Err(unsupported(
                "This Linux filesystem interface cannot be mediated.",
            ));
        }
        if call == libc::SYS_pidfd_getfd {
            // This installs a descriptor without passing through open or dup.
            // Reject it before the kernel can create an untracked cache handle.
            deny_syscall(pid, &mut regs)?;
            return Err(unsupported(
                "Linux pidfd descriptor duplication cannot be mediated.",
            ));
        }
        if call == libc::SYS_setns || call == libc::SYS_chroot || call == libc::SYS_pivot_root {
            // Path classification runs in the supervisor's mount and root
            // context. A tracee with a different view could resolve the
            // same pathname to a different inode after mediation.
            deny_syscall(pid, &mut regs)?;
            return Err(unsupported(
                "Linux mount namespace or filesystem root changes cannot be mediated.",
            ));
        }
        if call == libc::SYS_recvmsg || call == libc::SYS_recvmmsg {
            let messages = argument(&regs, 1);
            let count = if call == libc::SYS_recvmsg {
                1
            } else {
                // Linux caps recvmmsg's vlen at UIO_MAXIOV.
                (argument(&regs, 2) as usize).min(1024)
            };
            let stride = mem::size_of::<libc::mmsghdr>() as u64;
            for index in 0..count {
                let address = messages
                    .checked_add((index as u64) * stride)
                    .ok_or_else(injection_failed)?;
                if receives_ancillary_data(pid, address)? {
                    // An SCM_RIGHTS receive would install a new descriptor
                    // outside the group's ownership map. Stop before the
                    // kernel can create it, including for another thread.
                    deny_syscall(pid, &mut regs)?;
                    return Err(unsupported(
                        "Linux ancillary descriptor reception cannot be mediated.",
                    ));
                }
            }
            return resume(pid, false, 0);
        }
        if call == libc::SYS_clone || call == libc::SYS_clone3 || call == libc::SYS_unshare {
            let flags = if call == libc::SYS_clone3 {
                if argument(&regs, 1) < mem::size_of::<u64>() as u64 {
                    return resume(pid, false, 0);
                }
                let bytes = read_remote(pid, argument(&regs, 0), mem::size_of::<u64>())?;
                u64::from_ne_bytes(bytes[..8].try_into().map_err(|_| injection_failed())?)
            } else {
                argument(&regs, 0)
            };
            if flags & libc::CLONE_NEWNS as u64 != 0 {
                deny_syscall(pid, &mut regs)?;
                return Err(unsupported(
                    "Linux mount namespace changes cannot be mediated.",
                ));
            }
            if call != libc::SYS_unshare
                && flags & libc::CLONE_THREAD as u64 != 0
                && flags & (libc::CLONE_FILES | libc::CLONE_FS) as u64
                    != (libc::CLONE_FILES | libc::CLONE_FS) as u64
            {
                // A thread group has one FD and logical cwd map. A thread
                // with either private kernel context would invalidate it.
                deny_syscall(pid, &mut regs)?;
                return Err(unsupported(
                    "Linux threads with private descriptor or cwd contexts cannot be mediated.",
                ));
            }
            if flags & (libc::CLONE_FILES | libc::CLONE_FS) as u64 != 0
                && (call == libc::SYS_unshare || flags & libc::CLONE_THREAD as u64 == 0)
            {
                // FD and logical cwd state are owned by one thread group.
                // Sharing or splitting either context across that boundary
                // would make its cached identity stale.
                deny_syscall(pid, &mut regs)?;
                return Err(unsupported(
                    "Linux cross-group CLONE_FILES or CLONE_FS cannot be mediated.",
                ));
            }
            if call != libc::SYS_unshare {
                self.spawn_vm
                    .insert(pid, flags & libc::CLONE_VM as u64 != 0);
                // The exit stop clears failed clones as well as successful
                // calls after their fork/clone event consumed these flags.
                self.pending.insert(pid, Pending::Ordinary);
                return resume(pid, true, 0);
            }
            return resume(pid, false, 0);
        }
        if call == libc::SYS_close_range {
            let flags = argument(&regs, 2) as u32;
            if flags & CLOSE_RANGE_UNSHARE != 0 {
                deny_syscall(pid, &mut regs)?;
                return Err(unsupported(
                    "Linux close_range with UNSHARE cannot preserve descriptor ownership.",
                ));
            }
            if flags == 0 {
                self.pending.insert(
                    pid,
                    Pending::CloseRange(argument(&regs, 0) as u32, argument(&regs, 1) as u32),
                );
                return resume(pid, true, 0);
            }
            // CLOEXEC leaves the table intact until the exec event, where
            // we reconcile surviving descriptors against /proc. Invalid
            // flags retain the kernel's EINVAL result.
            return self.resume_fd_sensitive(pid);
        }
        if (call == libc::SYS_utimensat || call == SYS_FUTIMESAT) && argument(&regs, 1) == 0 {
            let fd = argument(&regs, 0) as i32;
            if self.descriptor(pid, fd).is_some_and(|entry| entry.readonly) {
                set_argument(&mut regs, 0, u64::MAX);
                set_registers(pid, &regs)?;
                self.pending.insert(pid, Pending::ForcedError(libc::EROFS));
                return resume(pid, true, 0);
            }
            return self.resume_fd_sensitive(pid);
        }
        if self.path_call(pid, regs).inspect_err(|error| {
            tracing::debug!(
                action = "linux_path_failure",
                pid,
                call,
                code = error.code.as_str(),
                "Owned child path mediation failed"
            );
        })? {
            if self.pending.get(&pid).is_some_and(
                |action| matches!(action, Pending::Open(translation) if translation.readonly),
            ) {
                // Native opens can block (for example, a FIFO whose writer
                // is another thread). Only managed opens need the group FD
                // barrier, after pathname translation identifies ownership.
                let group = Self::group(pid);
                if self.fd_busy.contains_key(&group) {
                    tracing::trace!(
                        action = "linux_fd_barrier_wait",
                        group,
                        pid,
                        call,
                        "Queuing managed open"
                    );
                    self.fd_waiting.push_back((group, pid));
                    return Ok(());
                }
                self.fd_busy.insert(group, pid);
            }
            return resume(pid, true, 0);
        }
        if call == libc::SYS_ioctl {
            if self
                .descriptor(pid, argument(&regs, 0) as i32)
                .is_some_and(|entry| entry.readonly)
            {
                let request = argument(&regs, 1);
                if matches!(request, FS_IOC_GETFLAGS | FS_IOC_FSGETXATTR | FIONREAD) {
                    return self.resume_fd_sensitive(pid);
                }
                set_argument(&mut regs, 0, u64::MAX);
                set_registers(pid, &regs)?;
                self.pending.insert(pid, Pending::ForcedError(libc::EROFS));
                return resume(pid, true, 0);
            }
            return self.resume_fd_sensitive(pid);
        }
        if matches!(
            call,
            libc::SYS_fchmod
                | libc::SYS_fchown
                | libc::SYS_ftruncate
                | libc::SYS_fallocate
                | libc::SYS_fsetxattr
                | libc::SYS_fremovexattr
        ) && self
            .descriptor(pid, argument(&regs, 0) as i32)
            .is_some_and(|entry| entry.readonly)
        {
            set_argument(&mut regs, 0, u64::MAX);
            set_registers(pid, &regs)?;
            self.pending.insert(pid, Pending::ForcedError(libc::EROFS));
            return resume(pid, true, 0);
        }
        let action = if call == libc::SYS_close {
            Pending::Close(argument(&regs, 0) as i32)
        } else if call == libc::SYS_fchdir {
            let fd = argument(&regs, 0) as i32;
            let logical = self
                .descriptor(pid, fd)
                .as_ref()
                .and_then(|entry| (entry.logical != entry.physical).then(|| entry.logical.clone()));
            Pending::ChangeDirectory(logical)
        } else if call == libc::SYS_dup
            || call == libc::SYS_dup3
            || call == SYS_DUP2
            || (call == libc::SYS_fcntl
                && matches!(
                    argument(&regs, 1) as i32,
                    libc::F_DUPFD | libc::F_DUPFD_CLOEXEC
                ))
        {
            let source = self.descriptor(pid, argument(&regs, 0) as i32);
            if call == libc::SYS_dup3 || call == SYS_DUP2 {
                if let Some(entry) = source.as_ref() {
                    self.dup_reservations
                        .insert(pid, (argument(&regs, 1) as i32, entry.clone()));
                }
            }
            Pending::Dup(source)
        } else if call == libc::SYS_getcwd {
            let group = Self::group(pid);
            if let Some(path) = self.cwd.get(&group) {
                Pending::GetCwd {
                    output: argument(&regs, 0),
                    capacity: argument(&regs, 1) as usize,
                    logical: path.clone(),
                }
            } else {
                return resume(pid, false, 0);
            }
        } else {
            return if self.fd_busy.get(&Self::group(pid)) == Some(&pid) {
                self.resume_fd_sensitive(pid)
            } else {
                resume(pid, false, 0)
            };
        };
        self.pending.insert(pid, action);
        resume(pid, true, 0)
    }

    fn exit(&mut self, pid: i32) -> Result<()> {
        if let Some(original) = self.scratch_pending.remove(&pid) {
            return self.finish_scratch(pid, original);
        }
        let Some(action) = self.pending.remove(&pid) else {
            return resume(pid, false, 0);
        };
        self.dup_reservations.remove(&pid);
        self.spawn_vm.remove(&pid);
        let mut regs = registers(pid)?;
        let returned = result(&regs);
        let group = Self::group(pid);
        match action {
            Pending::Open(translation) if returned >= 0 && translation.readonly => {
                self.fds
                    .entry(group)
                    .or_default()
                    .insert(returned as i32, translation);
            }
            Pending::Close(fd) if returned == 0 => {
                self.fds.entry(group).or_default().remove(&fd);
            }
            Pending::CloseRange(first, last) if returned == 0 => {
                self.fds
                    .entry(group)
                    .or_default()
                    .retain(|fd, _| (*fd as u32) < first || (*fd as u32) > last);
            }
            Pending::Dup(translated) if returned >= 0 => {
                let entry = self.fds.entry(group).or_default();
                entry.remove(&(returned as i32));
                if let Some(translated) = translated {
                    entry.insert(returned as i32, translated);
                }
            }
            Pending::ChangeDirectory(logical) if returned == 0 => {
                if let Some(logical) = logical {
                    self.cwd.insert(group, logical);
                } else {
                    self.cwd.remove(&group);
                }
            }
            Pending::LinkMetadata {
                output,
                target_len,
                statx,
            } if returned == 0 => {
                if statx {
                    let bytes = read_remote(pid, output, mem::size_of::<libc::statx>())?;
                    if bytes.len() != mem::size_of::<libc::statx>() {
                        return Err(injection_failed());
                    }
                    let mut info = unsafe { mem::zeroed::<libc::statx>() };
                    unsafe {
                        std::ptr::copy_nonoverlapping(
                            bytes.as_ptr(),
                            (&mut info as *mut libc::statx).cast(),
                            bytes.len(),
                        );
                    }
                    info.stx_mode = (info.stx_mode & !((libc::S_IFMT | 0o7777) as u16))
                        | libc::S_IFLNK as u16
                        | 0o777;
                    info.stx_size = target_len as u64;
                    let raw = unsafe {
                        std::slice::from_raw_parts(
                            (&info as *const libc::statx).cast(),
                            mem::size_of::<libc::statx>(),
                        )
                    };
                    write_remote(pid, output, raw)?;
                } else {
                    let bytes = read_remote(pid, output, mem::size_of::<libc::stat>())?;
                    if bytes.len() != mem::size_of::<libc::stat>() {
                        return Err(injection_failed());
                    }
                    let mut info = unsafe { mem::zeroed::<libc::stat>() };
                    unsafe {
                        std::ptr::copy_nonoverlapping(
                            bytes.as_ptr(),
                            (&mut info as *mut libc::stat).cast(),
                            bytes.len(),
                        );
                    }
                    info.st_mode =
                        (info.st_mode & !(libc::S_IFMT | 0o7777)) | libc::S_IFLNK | 0o777;
                    info.st_size = target_len as i64;
                    let raw = unsafe {
                        std::slice::from_raw_parts(
                            (&info as *const libc::stat).cast(),
                            mem::size_of::<libc::stat>(),
                        )
                    };
                    write_remote(pid, output, raw)?;
                }
            }
            Pending::ReadLink {
                output,
                capacity,
                target,
            } if returned >= 0 || returned == -(libc::EINVAL as i64) => {
                // The materialized target is a real directory, so the
                // kernel's EINVAL means it is not itself a symlink. Preserve
                // other errors such as EFAULT from the caller's output.
                let bytes = target.as_os_str().as_bytes();
                if capacity == 0 {
                    set_result(&mut regs, -(libc::EINVAL as i64));
                } else {
                    let count = bytes.len().min(capacity);
                    if write_remote_or_fault(pid, output, &bytes[..count])? {
                        set_result(&mut regs, count as i64);
                    } else {
                        set_result(&mut regs, -(libc::EFAULT as i64));
                    }
                }
                set_registers(pid, &regs)?;
            }
            Pending::GetCwd {
                output,
                capacity,
                logical,
            } if returned >= 0 => {
                let bytes =
                    CString::new(logical.as_os_str().as_bytes()).map_err(|_| injection_failed())?;
                if bytes.as_bytes_with_nul().len() > capacity {
                    set_result(&mut regs, -(libc::ERANGE as i64));
                } else {
                    write_remote(pid, output, bytes.as_bytes_with_nul())?;
                    set_result(&mut regs, bytes.as_bytes_with_nul().len() as i64);
                }
                set_registers(pid, &regs)?;
            }
            Pending::ForcedError(errno) => {
                set_result(&mut regs, -(errno as i64));
                set_registers(pid, &regs)?;
            }
            _ => {}
        }
        resume(pid, false, 0)
    }

    fn check_inputs(&mut self) -> Result<()> {
        for input in &self.view.graph.snapshot.inputs {
            self.watch.register(input)?;
        }
        self.view.graph.check_conflicts()?;
        if let Some(error) = super::supervisor::runtime_failure(&self.view.session)? {
            return Err(error);
        }
        if let Ok(entries) = fs::read_dir(self.view.session.join("active")) {
            for entry in entries {
                let entry = entry.map_err(|_| injection_failed())?;
                if !self.active_markers.insert(entry.path()) {
                    continue;
                }
                let input: Input = serde_json::from_slice(
                    &fs::read(entry.path()).map_err(|_| injection_failed())?,
                )
                .map_err(|_| injection_failed())?;
                self.watch.register(&input)?;
            }
        }
        self.watch.poll()
    }

    fn stop_tree(&mut self) -> Result<()> {
        // A parked peer is still at its original seccomp entry. Cancel its
        // syscall before cleanup resumes it, and release the group gate so
        // signal handlers can make progress during the grace period.
        for (_, pid) in self.fd_waiting.drain(..) {
            if let Ok(mut regs) = registers(pid) {
                finish_denied_syscall(pid, &mut regs)?;
            }
        }
        self.fd_busy.clear();
        let termination_signal = match super::supervisor::handled_signal() {
            libc::SIGINT | libc::SIGHUP | libc::SIGTERM => super::supervisor::handled_signal(),
            _ => libc::SIGTERM,
        };
        for pid in &self.tasks {
            unsafe {
                if self.early_stops.contains(pid) {
                    // No task state exists yet to mediate a resumed syscall.
                    // SIGKILL wakes this ptrace stop and keeps shutdown closed.
                    libc::kill(*pid, libc::SIGKILL);
                    continue;
                }
                libc::kill(*pid, termination_signal);
                // A traced task can be parked at the stop that caused the
                // failure. SIGTERM is only delivered after it is resumed.
                libc::ptrace(libc::PTRACE_CONT, *pid, 0, termination_signal);
            }
        }
        let deadline = Instant::now() + Duration::from_secs(5);
        while !self.tasks.is_empty() && Instant::now() < deadline {
            let _ = self.event_once();
            std::thread::sleep(Duration::from_millis(10));
        }
        for pid in &self.tasks {
            unsafe {
                libc::kill(*pid, libc::SIGKILL);
                libc::ptrace(libc::PTRACE_CONT, *pid, 0, 0);
            }
        }
        let reap_deadline = Instant::now() + Duration::from_secs(1);
        while !self.tasks.is_empty() && Instant::now() < reap_deadline {
            if self.event_once().is_err() {
                break;
            }
            std::thread::sleep(Duration::from_millis(1));
        }
        tracing::debug!(
            action = "linux_cleanup",
            remaining = self.tasks.len(),
            "Owned children stopped"
        );
        if !self.tasks.is_empty() {
            return Err(Error::new(
                Code::PnportCleanupFailed,
                "Some owned Linux descendants could not be reaped.",
            ));
        }
        Ok(())
    }

    fn event_once(&mut self) -> Result<bool> {
        let mut status = 0;
        let pid = unsafe { libc::waitpid(-1, &mut status, WAIT_ALL | libc::WNOHANG) };
        if pid == 0 {
            return Ok(false);
        }
        if pid < 0 {
            if std::io::Error::last_os_error().raw_os_error() == Some(libc::EINTR) {
                return Ok(false);
            }
            return Err(injection_failed());
        }
        if libc::WIFEXITED(status) || libc::WIFSIGNALED(status) {
            tracing::trace!(action = "linux_exit", pid, status, "Owned child exited");
            self.release_fd_barrier(pid)?;
            self.tasks.remove(&pid);
            self.fd_waiting.retain(|(_, waiting)| *waiting != pid);
            if self.early_stops.remove(&pid) {
                self.reaped_early.insert(pid);
            }
            self.pending.remove(&pid);
            self.dup_reservations.remove(&pid);
            self.release_task_space(pid);
            let group = self.groups.remove(&pid).unwrap_or(pid);
            if pid == self.root {
                self.root_exit_code = Some(if libc::WIFEXITED(status) {
                    libc::WEXITSTATUS(status)
                } else {
                    128 + libc::WTERMSIG(status)
                });
            }
            if !self.groups.values().any(|tracked| *tracked == group) {
                self.fds.remove(&group);
                self.cwd.remove(&group);
                if group == self.root {
                    self.root_result = Some(self.root_exit_code.unwrap_or_else(|| {
                        if libc::WIFEXITED(status) {
                            libc::WEXITSTATUS(status)
                        } else {
                            128 + libc::WTERMSIG(status)
                        }
                    }));
                }
            }
            return Ok(true);
        }
        if !libc::WIFSTOPPED(status) {
            return Ok(true);
        }
        if status >> 16 == libc::PTRACE_EVENT_STOP && !self.task_spaces.contains_key(&pid) {
            // waitpid may report a newly auto-attached child's initial stop
            // before its parent's clone event. Keep it parked until that
            // event installs the child's address-space and descriptor state.
            tracing::debug!(
                action = "linux_early_child_stop",
                pid,
                "Waiting for the parent clone event"
            );
            self.tasks.insert(pid);
            self.early_stops.insert(pid);
            return Ok(true);
        }
        let signal = libc::WSTOPSIG(status);
        if signal == (libc::SIGTRAP | 0x80) {
            self.exit(pid)?;
            self.release_fd_barrier(pid)?;
            return Ok(true);
        }
        if signal == libc::SIGTRAP {
            let event = status >> 16;
            if event == 0 {
                // A trap raised by the tracee is a child signal, not a ptrace
                // protocol event. Forward it to its handler or default action.
                resume(
                    pid,
                    self.pending.contains_key(&pid) || self.scratch_pending.contains_key(&pid),
                    libc::SIGTRAP,
                )?;
                return Ok(true);
            }
            tracing::trace!(
                action = "linux_event",
                pid,
                event,
                "Owned child trace event"
            );
            if event == libc::PTRACE_EVENT_STOP {
                self.startup_stops.remove(&pid);
                resume(
                    pid,
                    self.pending.contains_key(&pid) || self.scratch_pending.contains_key(&pid),
                    0,
                )?;
                return Ok(true);
            }
            if event == libc::PTRACE_EVENT_SECCOMP {
                let handled = if self.scratch.contains_key(&pid) {
                    let call = number(&registers(pid)?);
                    if fd_sensitive_syscall(call) {
                        let group = Self::group(pid);
                        if self.fd_busy.contains_key(&group) {
                            tracing::trace!(
                                action = "linux_fd_barrier_wait",
                                group,
                                pid,
                                call,
                                "Queuing descriptor syscall"
                            );
                            self.fd_waiting.push_back((group, pid));
                            return Ok(true);
                        }
                        self.fd_busy.insert(group, pid);
                    }
                    self.enter(pid)
                } else {
                    self.start_scratch(pid)
                };
                if let Err(error) = handled {
                    // A failed admission can leave the tracee parked before
                    // the kernel executes its original syscall. Cleanup must
                    // never resume that syscall, even when the child handles
                    // the graceful termination signal.
                    let mut regs = registers(pid)?;
                    finish_denied_syscall(pid, &mut regs)?;
                    return Err(error);
                }
                return Ok(true);
            }
            if matches!(event, x if x == libc::PTRACE_EVENT_FORK
                || x == libc::PTRACE_EVENT_VFORK
                || x == libc::PTRACE_EVENT_CLONE)
            {
                let mut created = 0usize;
                if unsafe { libc::ptrace(libc::PTRACE_GETEVENTMSG, pid, 0, &mut created) } != 0 {
                    return Err(injection_failed());
                }
                let child = created as i32;
                if self.reaped_early.remove(&child) {
                    self.spawn_vm.remove(&pid);
                    tracing::debug!(
                        action = "linux_reaped_early_child",
                        child,
                        "Child exited before clone registration"
                    );
                    resume(pid, self.pending.contains_key(&pid), 0)?;
                    return Ok(true);
                }
                self.tasks.insert(child);
                let early_stop = self.early_stops.remove(&child);
                if !early_stop {
                    self.startup_stops.insert(child);
                }
                let parent_group = Self::group(pid);
                let child_group = Self::group(child);
                let spawn_vm = self.spawn_vm.remove(&pid);
                let shares_vm = if event == libc::PTRACE_EVENT_VFORK {
                    true
                } else if event == libc::PTRACE_EVENT_CLONE {
                    spawn_vm.ok_or_else(injection_failed)?
                } else {
                    spawn_vm.unwrap_or(false)
                };
                if child_group == parent_group && !shares_vm {
                    return Err(injection_failed());
                }
                self.register_child_space(
                    pid,
                    child,
                    shares_vm,
                    event == libc::PTRACE_EVENT_VFORK,
                )?;
                self.groups.insert(child, child_group);
                if child_group != parent_group {
                    if let Some(fds) = self.fds.get(&parent_group).cloned() {
                        self.fds.insert(child_group, fds);
                    }
                    if let Some(cwd) = self.cwd.get(&parent_group).cloned() {
                        self.cwd.insert(child_group, cwd);
                    }
                }
                if early_stop {
                    resume(child, false, 0)?;
                }
                resume(pid, self.pending.contains_key(&pid), 0)?;
                return Ok(true);
            }
            if event == libc::PTRACE_EVENT_EXEC {
                self.release_fd_barrier(pid)?;
                // The exec stop precedes the new image's first userspace
                // instruction. Inspect the image the kernel actually loaded,
                // including script interpreters and fd-based execs, before
                // any mixed-ABI syscall can bypass this filter's trace list.
                let image = PathBuf::from(format!("/proc/{pid}/exe"));
                let static_image = is_static(&image)?;
                tracing::debug!(
                    action = "linux_exec_admission",
                    pid,
                    static_image,
                    "Validated owned executable image"
                );
                let mut former = 0usize;
                if unsafe { libc::ptrace(libc::PTRACE_GETEVENTMSG, pid, 0, &mut former) } != 0 {
                    return Err(injection_failed());
                }
                let former = former as i32;
                if former > 0 && former != pid {
                    // Linux changes a non-leader execing thread's TID to its
                    // leader's TID. The former TID will never report an exit.
                    tracing::debug!(
                        action = "linux_exec_tid",
                        pid,
                        former,
                        "Reconciled execing thread identity"
                    );
                    self.tasks.remove(&former);
                    self.pending.remove(&former);
                    self.dup_reservations.remove(&former);
                    self.release_task_space(former);
                    self.groups.remove(&former);
                }
                self.groups.insert(pid, Self::group(pid));
                self.pending.remove(&pid);
                self.dup_reservations.remove(&pid);
                self.release_task_space(pid);
                let space = self.new_space(Vec::new())?;
                self.task_spaces.insert(pid, space);
                if pid == self.root {
                    self.root_exec = true;
                    self.root_exit_code = None;
                }
                let group = Self::group(pid);
                if let Some(fds) = self.fds.get_mut(&group) {
                    fds.retain(|fd, _| Path::new(&format!("/proc/{pid}/fd/{fd}")).exists());
                }
            }
            resume(pid, false, 0)?;
            return Ok(true);
        }
        if status >> 16 == libc::PTRACE_EVENT_STOP {
            if self.startup_stops.remove(&pid) {
                resume(
                    pid,
                    self.pending.contains_key(&pid) || self.scratch_pending.contains_key(&pid),
                    0,
                )?;
            } else {
                // SEIZE + LISTEN preserves a real job-control stop until
                // SIGCONT, including a child-requested SIGSTOP.
                if unsafe { libc::ptrace(libc::PTRACE_LISTEN, pid, 0, 0) } != 0 {
                    return Err(injection_failed());
                }
                // The shell waits for pnport as the job leader. A tracee can
                // stop itself without stopping that leader, leaving `fg`
                // unable to resume the job. Mirror only foreground-group
                // stops; an independently grouped descendant must not stop
                // its supervisor.
                if matches!(
                    signal,
                    libc::SIGSTOP | libc::SIGTSTP | libc::SIGTTIN | libc::SIGTTOU
                ) && unsafe { libc::getpgid(Self::group(pid)) == libc::getpgrp() }
                {
                    tracing::debug!(
                        action = "linux_job_stop",
                        pid,
                        signal,
                        "Stopping supervisor with the foreground tracee"
                    );
                    if unsafe { libc::raise(libc::SIGSTOP) } != 0 {
                        return Err(injection_failed());
                    }
                }
            }
            return Ok(true);
        }
        resume(
            pid,
            self.pending.contains_key(&pid) || self.scratch_pending.contains_key(&pid),
            signal,
        )?;
        Ok(true)
    }
}

pub fn run_traced(view: &mut View, prepared: &Prepared) -> Result<i32> {
    probe()?;
    // Adopt descendants after their direct parent exits so the supervisor
    // can reap detached children instead of leaving zombies with container PID 1.
    if unsafe { libc::prctl(libc::PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) } != 0 {
        return Err(unsupported("Linux child subreaper support is unavailable."));
    }
    let static_image = is_static(&prepared.program)?;
    tracing::debug!(
        action = "linux_admission",
        static_image,
        "Starting owned child syscall tracing"
    );
    let binary = std::env::current_exe().map_err(|_| injection_failed())?;
    let mut command = Command::new(binary);
    // Keep the shell's foreground process group so interactive reads and
    // terminal-generated signals reach the command as they do without pnport.
    command
        .arg(LAUNCH_ARG)
        .arg(&prepared.program)
        .args(&prepared.args)
        .env("PNPORT_SESSION", &view.session);
    let (child, owner) = spawn_owned_helper(&mut command)?;
    let pid = child.id() as i32;
    let mut status = 0;
    if unsafe { libc::waitpid(pid, &mut status, libc::WUNTRACED) } != pid
        || !libc::WIFSTOPPED(status)
        || libc::WSTOPSIG(status) != libc::SIGSTOP
    {
        unsafe {
            libc::kill(pid, libc::SIGKILL);
            libc::waitpid(pid, &mut status, 0);
        }
        return Err(injection_failed());
    }
    drop(owner);
    if unsafe { libc::ptrace(libc::PTRACE_SEIZE, pid, 0, TRACE_OPTIONS) } != 0 {
        unsafe {
            libc::kill(pid, libc::SIGKILL);
            libc::waitpid(pid, &mut status, 0);
        }
        return Err(injection_failed());
    }
    let mut trace = Trace {
        view,
        tasks: HashSet::from([pid]),
        startup_stops: HashSet::from([pid]),
        early_stops: HashSet::new(),
        reaped_early: HashSet::new(),
        groups: HashMap::from([(pid, pid)]),
        pending: HashMap::new(),
        fds: HashMap::new(),
        dup_reservations: HashMap::new(),
        fd_busy: HashMap::new(),
        fd_waiting: VecDeque::new(),
        cwd: HashMap::new(),
        scratch: HashMap::new(),
        scratch_pending: HashMap::new(),
        task_spaces: HashMap::from([(pid, 0)]),
        spaces: HashMap::from([(0, ScratchSpace::default())]),
        next_space: 1,
        spawn_vm: HashMap::new(),
        watch: InputWatch::default(),
        active_markers: HashSet::new(),
        root: pid,
        root_result: None,
        root_exit_code: None,
        root_exec: false,
    };
    let outcome = (|| {
        trace.seed_inherited_descriptors()?;
        if unsafe { libc::kill(pid, libc::SIGCONT) } != 0 {
            return Err(injection_failed());
        }
        let mut next_check = Instant::now();
        loop {
            let signal = super::supervisor::handled_signal();
            if signal != 0 {
                return Ok(128 + signal);
            }
            if Instant::now() >= next_check {
                trace.check_inputs()?;
                next_check = Instant::now() + Duration::from_millis(100);
            }
            if let Some(code) = trace.root_result {
                if let Some(error) = super::supervisor::runtime_failure(&trace.view.session)? {
                    return Err(error);
                }
                if !trace.root_exec {
                    return Err(injection_failed());
                }
                return Ok(code);
            }
            let event = match trace.event_once() {
                Err(_) if super::supervisor::handled_signal() != 0 => {
                    return Ok(128 + super::supervisor::handled_signal());
                }
                result => result?,
            };
            if !event {
                std::thread::sleep(Duration::from_millis(1));
            }
        }
    })();
    trace.stop_tree()?;
    outcome
}

#[cfg(all(test, target_arch = "x86_64"))]
mod tests {
    use super::*;

    #[test]
    fn scratch_replay_restores_x64_syscall_number() {
        let mut regs: Registers = unsafe { mem::zeroed() };
        regs.orig_rax = libc::SYS_execve as u64;
        regs.rax = (-libc::ENOSYS as i64) as u64;
        regs.rip = 0x1002;

        prepare_replayed_syscall(&mut regs).unwrap();

        assert_eq!(regs.rip, 0x1000);
        assert_eq!(regs.orig_rax, libc::SYS_execve as u64);
        assert_eq!(regs.rax, libc::SYS_execve as u64);
    }
}
