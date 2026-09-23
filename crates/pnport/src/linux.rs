//! Linux syscall interception for executables which cannot load the preload.
//!
//! The filter follows fspy's pinned Linux seccomp approach. Virtualization
//! needs to replace pathname arguments, so selected calls use
//! `SECCOMP_RET_TRACE` and a tracer owned by the launching supervisor. The
//! tracee opts in with `PTRACE_TRACEME`; pnport never attaches to other tasks.

use std::{
    collections::{HashMap, HashSet},
    ffi::{CString, OsStr},
    fs::{self, File},
    io::{Read, Seek, SeekFrom},
    mem,
    os::unix::{ffi::OsStrExt, process::CommandExt},
    path::{Component, Path, PathBuf},
    process::Command,
    time::{Duration, Instant},
};

use libc::{self, c_void};
use pnport::{
    diagnostic::{Code, Error, Result},
    executable::Prepared,
    graph::Input,
    view::{Translation, View},
};

use crate::input_watch::InputWatch;

const LAUNCH_ARG: &str = "__pnport_linux_launch";
const PROBE_ARG: &str = "__pnport_linux_probe";
const WAIT_ALL: i32 = 0x4000_0000;
const SECCOMP_RET_TRACE: u32 = 0x7ff0_0000;
const SECCOMP_RET_ALLOW: u32 = 0x7fff_0000;
const SECCOMP_SET_MODE_FILTER: libc::c_uint = 1;
const PATH_LIMIT: usize = 4096;
// The fixed prefix and resolution bits from Linux's openat2 UAPI.
const OPEN_HOW_SIZE: usize = 24;
const RESOLVE_BENEATH: u64 = 0x08;
const RESOLVE_IN_ROOT: u64 = 0x10;
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
const SYS_READLINK: i64 = libc::SYS_readlink;
#[cfg(target_arch = "aarch64")]
const SYS_READLINK: i64 = -1;
#[cfg(target_arch = "x86_64")]
const SYS_DUP2: i64 = libc::SYS_dup2;
#[cfg(target_arch = "aarch64")]
const SYS_DUP2: i64 = -1;

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
        libc::SYS_readlinkat,
        libc::SYS_faccessat,
        libc::SYS_faccessat2,
        libc::SYS_execve,
        libc::SYS_execveat,
        libc::SYS_chdir,
        libc::SYS_fchdir,
        libc::SYS_getcwd,
        libc::SYS_close,
        libc::SYS_dup,
        libc::SYS_dup3,
        libc::SYS_fcntl,
        libc::SYS_unlinkat,
        libc::SYS_mkdirat,
        libc::SYS_renameat,
        libc::SYS_renameat2,
        libc::SYS_linkat,
        libc::SYS_symlinkat,
        libc::SYS_mknodat,
        libc::SYS_fchmodat,
        libc::SYS_fchownat,
        libc::SYS_utimensat,
        libc::SYS_fchmod,
        libc::SYS_fchown,
        libc::SYS_ftruncate,
        libc::SYS_fallocate,
        libc::SYS_truncate,
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
    ]);
    calls
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
        Some(value) if value == OsStr::new(PROBE_ARG) => {
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
        Some(value) if value == OsStr::new(LAUNCH_ARG) => {
            if helper_setup().is_err() {
                record_helper_failure();
                unsafe { libc::_exit(125) }
            }
            let Some(program) = args.next() else {
                unsafe { libc::_exit(125) }
            };
            let error = Command::new(program).args(args).exec();
            let _ = error;
            record_helper_failure();
            unsafe { libc::_exit(125) }
        }
        _ => {}
    }
}

fn record_helper_failure() {
    if let Some(session) = std::env::var_os("PNPORT_SESSION") {
        let _ = fs::write(
            PathBuf::from(session).join("failure"),
            Code::PnportInjectionFailed.as_str(),
        );
    }
}

fn helper_setup() -> std::io::Result<()> {
    unsafe {
        if libc::ptrace(libc::PTRACE_TRACEME, 0, 0, 0) != 0 {
            return Err(std::io::Error::last_os_error());
        }
    }
    install_filter()?;
    unsafe {
        libc::raise(libc::SIGSTOP);
    }
    Ok(())
}

pub fn probe() -> Result<()> {
    let binary = std::env::current_exe().map_err(|_| injection_failed())?;
    let mut child = Command::new(binary)
        .arg(PROBE_ARG)
        .spawn()
        .map_err(|_| injection_failed())?;
    let pid = child.id() as i32;
    let mediated = (|| -> Result<()> {
        let mut status = 0;
        if unsafe { libc::waitpid(pid, &mut status, 0) } != pid || !libc::WIFSTOPPED(status) {
            tracing::debug!(
                action = "linux_probe",
                stage = "initial_stop",
                status,
                "Probe stop unavailable"
            );
            return Err(injection_failed());
        }
        if unsafe { libc::ptrace(libc::PTRACE_SETOPTIONS, pid, 0, TRACE_OPTIONS) } != 0 {
            tracing::debug!(
                action = "linux_probe",
                stage = "options",
                "Probe trace options unavailable"
            );
            return Err(injection_failed());
        }
        resume(pid, false, 0)?;
        if unsafe { libc::waitpid(pid, &mut status, 0) } != pid
            || !libc::WIFSTOPPED(status)
            || libc::WSTOPSIG(status) != libc::SIGTRAP
            || status >> 16 != libc::PTRACE_EVENT_SECCOMP
        {
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
            || read_path(pid, argument(&regs, 1))? != Path::new("/pnport-probe-must-be-rewritten")
        {
            tracing::debug!(
                action = "linux_probe",
                stage = "pathname",
                call = number(&regs),
                "Probe pathname did not match"
            );
            return Err(injection_failed());
        }
        rewrite_path(pid, &mut regs, 1, Path::new("/dev/null"))?;
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
#[cfg(target_arch = "aarch64")]
fn number(regs: &Registers) -> i64 {
    regs.regs[8] as i64
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
fn read_path(pid: i32, address: u64) -> Result<PathBuf> {
    if address == 0 {
        return Err(injection_failed());
    }
    let mut bytes = Vec::new();
    while bytes.len() < PATH_LIMIT {
        let part = read_remote(
            pid,
            address + bytes.len() as u64,
            (PATH_LIMIT - bytes.len()).min(256),
        )?;
        if let Some(end) = part.iter().position(|byte| *byte == 0) {
            bytes.extend_from_slice(&part[..end]);
            return Ok(PathBuf::from(OsStr::from_bytes(&bytes)));
        }
        bytes.extend_from_slice(&part);
    }
    Err(unsupported(
        "A child pathname exceeded the Linux path limit.",
    ))
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
fn rewrite_path(pid: i32, regs: &mut Registers, index: usize, path: &Path) -> Result<()> {
    rewrite_path_slot(pid, regs, index, path, 0)
}
fn rewrite_path_slot(
    pid: i32,
    regs: &mut Registers,
    index: usize,
    path: &Path,
    slot: usize,
) -> Result<()> {
    let bytes = CString::new(path.as_os_str().as_bytes()).map_err(|_| injection_failed())?;
    if bytes.as_bytes_with_nul().len() > PATH_LIMIT {
        return Err(injection_failed());
    }
    // The unused space immediately below the stopped thread's stack pointer
    // lasts until this syscall exits. Keep clear of the x86-64 red zone.
    let address = stack_pointer(regs)
        .checked_sub(bytes.as_bytes_with_nul().len() as u64 + 256 + (slot * PATH_LIMIT) as u64)
        .ok_or_else(injection_failed)?
        & !15;
    write_remote(pid, address, bytes.as_bytes_with_nul())?;
    set_argument(regs, index, address);
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
    Dup(i32),
    ChangeDirectory(PathBuf),
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

struct Trace<'a> {
    view: &'a mut View,
    tasks: HashSet<i32>,
    pending: HashMap<i32, Pending>,
    fds: HashMap<i32, HashMap<i32, Translation>>,
    cwd: HashMap<i32, PathBuf>,
    watch: InputWatch,
    active_markers: HashSet<PathBuf>,
    root: i32,
    root_result: Option<i32>,
    root_exec: bool,
}

impl Trace<'_> {
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
        } else if let Some(entry) = self.fds.get(&group).and_then(|fds| fds.get(&dirfd)) {
            entry.logical.clone()
        } else {
            fs::read_link(format!("/proc/{pid}/fd/{dirfd}")).map_err(|_| injection_failed())?
        };
        if root.as_os_str().is_empty() {
            return Err(injection_failed());
        }
        Ok(root.join(path))
    }

    fn translate(&mut self, pid: i32, dirfd: i32, pointer: u64) -> Result<Translation> {
        let path = read_path(pid, pointer)?;
        let absolute = self.base(pid, dirfd, &path)?;
        self.view.translate(&absolute).inspect_err(|error| {
            if error.code == Code::PnportResolutionFailed {
                tracing::debug!(action = "linux_resolution_miss", path = %absolute.display(),
                    "Owned child path was not present in the PnP view");
            } else {
                let _ = fs::write(self.view.session.join("failure"), error.code.as_str());
            }
        })
    }

    fn proc_descriptor(&self, pid: i32, path: &Path) -> Option<Translation> {
        let text = path.to_str()?;
        let fd = text
            .strip_prefix("/proc/self/fd/")
            .or_else(|| text.strip_prefix("/proc/thread-self/fd/"))
            .or_else(|| text.strip_prefix("/dev/fd/"))
            .or_else(|| text.strip_prefix(&format!("/proc/{pid}/fd/")))?
            .parse::<i32>()
            .ok()?;
        self.fds.get(&Self::group(pid))?.get(&fd).cloned()
    }

    fn path_call(&mut self, pid: i32, mut regs: Registers) -> Result<bool> {
        let call = number(&regs);
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
                let bytes = read_remote(pid, argument(&regs, 2), 8)?;
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
                || n == libc::SYS_fchownat
                || n == libc::SYS_utimensat
                || n == libc::SYS_mknodat
                || n == libc::SYS_linkat =>
            {
                let writing = n == libc::SYS_unlinkat
                    || n == libc::SYS_mkdirat
                    || n == libc::SYS_fchmodat
                    || n == libc::SYS_fchownat
                    || n == libc::SYS_utimensat
                    || n == libc::SYS_mknodat
                    || n == libc::SYS_linkat
                    || ((n == libc::SYS_faccessat || n == libc::SYS_faccessat2)
                        && argument(&regs, 2) as i32 & libc::W_OK != 0);
                (1, argument(&regs, 0) as i32, writing)
            }
            n if n == libc::SYS_symlinkat => (2, argument(&regs, 1) as i32, true),
            n if n == libc::SYS_execve || n == libc::SYS_chdir => (0, libc::AT_FDCWD, false),
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
            if argument(&regs, 3) < OPEN_HOW_SIZE as u64 {
                // The kernel rejects an undersized open_how without opening a
                // path; leave its EINVAL result intact.
                return Ok(false);
            }
            let how = read_remote(pid, argument(&regs, 2), OPEN_HOW_SIZE)?;
            u64::from_ne_bytes(
                how.get(16..OPEN_HOW_SIZE)
                    .ok_or_else(injection_failed)?
                    .try_into()
                    .map_err(|_| injection_failed())?,
            )
        } else {
            0
        };
        let original = read_path(pid, argument(&regs, path_arg))?;
        let is_open = call == libc::SYS_openat || call == libc::SYS_openat2 || call == SYS_OPEN;
        let in_root = openat2_resolve & RESOLVE_IN_ROOT != 0;
        if openat2_resolve & RESOLVE_BENEATH != 0
            && (original.is_absolute() || escapes_beneath(&original))
        {
            // Keep the kernel's EXDEV result for paths that escape dirfd;
            // normalizing these first would erase the attempted traversal.
            return Ok(false);
        }
        if original.as_os_str().is_empty()
            && (call == libc::SYS_newfstatat
                || call == libc::SYS_statx
                || call == libc::SYS_execveat
                    && argument(&regs, 4) as i32 & libc::AT_EMPTY_PATH != 0)
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
        if let Some(descriptor) = (!in_root)
            .then(|| self.proc_descriptor(pid, &original))
            .flatten()
        {
            if call == libc::SYS_readlinkat || call == SYS_READLINK {
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
            if is_open {
                // The kernel follows /proc/self/fd to the materialized file.
                // Retain the logical ownership on the newly opened descriptor.
                self.pending.insert(pid, Pending::Open(descriptor));
                return Ok(true);
            }
            return Ok(false);
        }
        let translation = match if in_root && original.is_absolute() {
            let base = self.base(pid, dirfd, Path::new("."))?;
            self.view
                .translate(&base.join(original.strip_prefix("/").map_err(|_| injection_failed())?))
        } else {
            self.translate(pid, dirfd, argument(&regs, path_arg))
        } {
            Ok(value) => value,
            Err(error) if error.code == Code::PnportResolutionFailed => {
                self.force_error(pid, &mut regs, path_arg, libc::ENOENT)?;
                return Ok(true);
            }
            Err(error) => return Err(error),
        };
        if writing && translation.readonly {
            self.force_error(pid, &mut regs, path_arg, libc::EROFS)?;
            return Ok(true);
        }
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
        let second_translation = if let Some((other_arg, other_fd)) = second {
            let other = read_path(pid, argument(&regs, other_arg))?;
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
            let translated = match self.translate(pid, other_fd, argument(&regs, other_arg)) {
                Ok(value) => value,
                Err(error) if error.code == Code::PnportResolutionFailed => {
                    self.force_error(pid, &mut regs, path_arg, libc::ENOENT)?;
                    return Ok(true);
                }
                Err(error) => return Err(error),
            };
            if translated.readonly {
                self.force_error(pid, &mut regs, path_arg, libc::EROFS)?;
                return Ok(true);
            }
            Some((other_arg, other, translated))
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
            if pnport::graph::normalize(&base.join(relative)) != translation.physical {
                return Err(unsupported(
                    "Constrained openat2 resolution cannot preserve this virtual path.",
                ));
            }
            false
        } else {
            original != translation.physical
        };
        if changed {
            rewrite_path(pid, &mut regs, path_arg, &translation.physical)?;
        }
        if let Some((other_arg, other, translated)) = second_translation {
            if other != translated.physical {
                rewrite_path_slot(pid, &mut regs, other_arg, &translated.physical, 1)?;
            }
        }
        if is_open {
            self.pending.insert(pid, Pending::Open(translation));
        } else if call == libc::SYS_chdir {
            self.pending
                .insert(pid, Pending::ChangeDirectory(translation.logical));
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
        rewrite_path(pid, regs, index, denied)?;
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
            return Err(unsupported(
                "This Linux filesystem interface cannot be mediated.",
            ));
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
            return resume(pid, true, 0);
        }
        let group = Self::group(pid);
        if matches!(
            call,
            libc::SYS_fchmod
                | libc::SYS_fchown
                | libc::SYS_ftruncate
                | libc::SYS_fallocate
                | libc::SYS_fsetxattr
                | libc::SYS_fremovexattr
        ) && self
            .fds
            .get(&group)
            .and_then(|fds| fds.get(&(argument(&regs, 0) as i32)))
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
            let path = self
                .fds
                .get(&group)
                .and_then(|fds| fds.get(&fd))
                .map(|entry| entry.logical.clone())
                .or_else(|| fs::read_link(format!("/proc/{pid}/fd/{fd}")).ok());
            if let Some(path) = path {
                Pending::ChangeDirectory(path)
            } else {
                return resume(pid, false, 0);
            }
        } else if call == libc::SYS_dup
            || call == libc::SYS_dup3
            || call == SYS_DUP2
            || (call == libc::SYS_fcntl
                && matches!(
                    argument(&regs, 1) as i32,
                    libc::F_DUPFD | libc::F_DUPFD_CLOEXEC
                ))
        {
            Pending::Dup(argument(&regs, 0) as i32)
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
            return resume(pid, false, 0);
        };
        self.pending.insert(pid, action);
        resume(pid, true, 0)
    }

    fn exit(&mut self, pid: i32) -> Result<()> {
        let Some(action) = self.pending.remove(&pid) else {
            return resume(pid, false, 0);
        };
        let mut regs = registers(pid)?;
        let returned = result(&regs);
        let group = Self::group(pid);
        match action {
            Pending::Open(translation) if returned >= 0 => {
                self.fds
                    .entry(group)
                    .or_default()
                    .insert(returned as i32, translation);
            }
            Pending::Close(fd) if returned == 0 => {
                self.fds.entry(group).or_default().remove(&fd);
            }
            Pending::Dup(fd) if returned >= 0 => {
                let entry = self.fds.entry(group).or_default();
                let translated = entry.get(&fd).cloned();
                entry.remove(&(returned as i32));
                if let Some(translated) = translated {
                    entry.insert(returned as i32, translated);
                }
            }
            Pending::ChangeDirectory(logical) if returned == 0 => {
                self.cwd.insert(group, logical);
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
                    info.stx_mode = (info.stx_mode & !(libc::S_IFMT as u16)) | libc::S_IFLNK as u16;
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
                    info.st_mode = (info.st_mode & !libc::S_IFMT) | libc::S_IFLNK;
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
            } => {
                let bytes = target.as_os_str().as_bytes();
                if capacity == 0 {
                    set_result(&mut regs, -(libc::EINVAL as i64));
                } else {
                    let count = bytes.len().min(capacity);
                    write_remote(pid, output, &bytes[..count])?;
                    set_result(&mut regs, count as i64);
                }
                set_registers(pid, &regs)?;
            }
            Pending::GetCwd {
                output,
                capacity,
                logical,
            } => {
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
        for pid in &self.tasks {
            unsafe {
                libc::kill(*pid, libc::SIGTERM);
            }
        }
        let deadline = Instant::now() + Duration::from_secs(5);
        while !self.tasks.is_empty() && Instant::now() < deadline {
            let _ = self.event_once();
            std::thread::sleep(Duration::from_millis(10));
        }
        for pid in &self.tasks {
            unsafe {
                libc::ptrace(libc::PTRACE_CONT, *pid, 0, 0);
                libc::kill(*pid, libc::SIGKILL);
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
            self.tasks.remove(&pid);
            self.pending.remove(&pid);
            if pid == self.root {
                self.root_result = Some(if libc::WIFEXITED(status) {
                    libc::WEXITSTATUS(status)
                } else {
                    128 + libc::WTERMSIG(status)
                });
            }
            return Ok(true);
        }
        if !libc::WIFSTOPPED(status) {
            return Ok(true);
        }
        let signal = libc::WSTOPSIG(status);
        if signal == (libc::SIGTRAP | 0x80) {
            self.exit(pid)?;
            return Ok(true);
        }
        if signal == libc::SIGTRAP {
            let event = status >> 16;
            if event == 0 {
                // A trap raised by the tracee is a child signal, not a ptrace
                // protocol event. Forward it to its handler or default action.
                resume(pid, false, libc::SIGTRAP)?;
                return Ok(true);
            }
            tracing::trace!(
                action = "linux_event",
                pid,
                event,
                "Owned child trace event"
            );
            if event == libc::PTRACE_EVENT_SECCOMP {
                self.enter(pid)?;
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
                self.tasks.insert(child);
                let parent_group = Self::group(pid);
                let child_group = Self::group(child);
                if child_group != parent_group {
                    if let Some(fds) = self.fds.get(&parent_group).cloned() {
                        self.fds.insert(child_group, fds);
                    }
                    if let Some(cwd) = self.cwd.get(&parent_group).cloned() {
                        self.cwd.insert(child_group, cwd);
                    }
                }
            }
            if event == libc::PTRACE_EVENT_EXEC {
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
                }
                self.pending.remove(&pid);
                if pid == self.root {
                    self.root_exec = true;
                }
                let group = Self::group(pid);
                if let Some(fds) = self.fds.get_mut(&group) {
                    fds.retain(|fd, _| Path::new(&format!("/proc/{pid}/fd/{fd}")).exists());
                }
            }
            resume(pid, false, 0)?;
            return Ok(true);
        }
        // Initial stops from auto-attached children are ptrace protocol, not
        // signals requested by the child command.
        resume(pid, false, if signal == libc::SIGSTOP { 0 } else { signal })?;
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
    command
        .arg(LAUNCH_ARG)
        .arg(&prepared.program)
        .args(&prepared.args)
        .env("PNPORT_SESSION", &view.session)
        .process_group(0);
    let child = command.spawn().map_err(|_| injection_failed())?;
    let pid = child.id() as i32;
    let mut status = 0;
    if unsafe { libc::waitpid(pid, &mut status, 0) } != pid || !libc::WIFSTOPPED(status) {
        unsafe {
            libc::kill(pid, libc::SIGKILL);
            libc::waitpid(pid, &mut status, 0);
        }
        return Err(injection_failed());
    }
    if unsafe { libc::ptrace(libc::PTRACE_SETOPTIONS, pid, 0, TRACE_OPTIONS) } != 0 {
        unsafe {
            libc::kill(pid, libc::SIGKILL);
            libc::waitpid(pid, &mut status, 0);
        }
        return Err(injection_failed());
    }
    let mut trace = Trace {
        view,
        tasks: HashSet::from([pid]),
        pending: HashMap::new(),
        fds: HashMap::new(),
        cwd: HashMap::new(),
        watch: InputWatch::default(),
        active_markers: HashSet::new(),
        root: pid,
        root_result: None,
        root_exec: false,
    };
    let outcome = (|| {
        resume(pid, false, 0)?;
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
            if !trace.event_once()? {
                std::thread::sleep(Duration::from_millis(1));
            }
        }
    })();
    trace.stop_tree()?;
    outcome
}
