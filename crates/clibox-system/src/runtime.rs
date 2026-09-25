use std::{
    io::Read,
    process::{Child, Command, ExitStatus, Stdio},
    sync::{
        atomic::{AtomicI32, AtomicUsize, Ordering},
        mpsc,
    },
    time::{Duration, Instant},
};

use crate::error::{Code, Failure, Result};

static SIGNAL: AtomicI32 = AtomicI32::new(0);
static CANCELLATION_GENERATION: AtomicUsize = AtomicUsize::new(0);
#[cfg(unix)]
static TERMINAL_INTERRUPT_ACK_DESCRIPTOR: AtomicI32 = AtomicI32::new(-1);
pub const POLL: Duration = Duration::from_millis(20);

pub fn install_signals() -> Result<()> {
    #[cfg(unix)]
    for signal in [libc::SIGINT, libc::SIGTERM, libc::SIGHUP, libc::SIGQUIT] {
        // Signal handlers only update atomics and, for a terminal SIGINT,
        // acknowledge the installed launcher through its private pipe.
        // Blocking work stays in the command loop.
        unsafe {
            signal_hook_registry::register_sigaction(signal, move |info| {
                SIGNAL.store(signal, Ordering::SeqCst);
                CANCELLATION_GENERATION.fetch_add(1, Ordering::SeqCst);
                if signal == libc::SIGINT {
                    acknowledge_terminal_interrupt(info);
                }
            })
        }
        .map_err(|e| Failure::io(&e))?;
    }
    #[cfg(windows)]
    unsafe {
        use windows_sys::Win32::System::Console::*;
        unsafe extern "system" fn handler(event: u32) -> i32 {
            match event {
                CTRL_C_EVENT => SIGNAL.store(2, Ordering::SeqCst),
                CTRL_BREAK_EVENT => SIGNAL.store(21, Ordering::SeqCst),
                _ => return 0,
            }
            CANCELLATION_GENERATION.fetch_add(1, Ordering::SeqCst);
            1
        }
        if SetConsoleCtrlHandler(Some(handler), 1) == 0 {
            return Err(Failure::io(&std::io::Error::last_os_error()));
        }
    }
    Ok(())
}

#[cfg(unix)]
pub fn configure_terminal_interrupt_acknowledgement(descriptor: Option<libc::c_int>) -> Result<()> {
    let descriptor = match descriptor.filter(|descriptor| *descriptor >= 3) {
        Some(descriptor) => match close_on_exec(descriptor) {
            Ok(()) => descriptor,
            // A user-controlled environment can name an absent descriptor.
            // Treat it as no launcher acknowledgement instead of changing
            // workload behavior; an installed launcher's pipe is open here.
            Err(error) if error.raw_os_error() == Some(libc::EBADF) => -1,
            Err(error) => return Err(Failure::io(&error)),
        },
        None => -1,
    };
    TERMINAL_INTERRUPT_ACK_DESCRIPTOR.store(descriptor, Ordering::SeqCst);
    Ok(())
}

#[cfg(unix)]
pub fn configure_installed_terminal_interrupt_acknowledgement() -> Result<()> {
    const ACKNOWLEDGEMENT_DESCRIPTOR: &str = "CLIBOX_TERMINAL_INTERRUPT_ACK_FD";

    let value = std::env::var(ACKNOWLEDGEMENT_DESCRIPTOR).ok();
    let descriptor = installed_terminal_interrupt_acknowledgement_descriptor(value.as_deref());
    configure_terminal_interrupt_acknowledgement(descriptor)
}

#[cfg(unix)]
fn installed_terminal_interrupt_acknowledgement_descriptor(
    value: Option<&str>,
) -> Option<libc::c_int> {
    value
        .and_then(|value| value.parse().ok())
        .filter(|descriptor| *descriptor == 3)
}

#[cfg(unix)]
fn close_on_exec(descriptor: libc::c_int) -> std::io::Result<()> {
    let flags = unsafe { libc::fcntl(descriptor, libc::F_GETFD) };
    if flags == -1 {
        return Err(std::io::Error::last_os_error());
    }
    if unsafe { libc::fcntl(descriptor, libc::F_SETFD, flags | libc::FD_CLOEXEC) } == -1 {
        return Err(std::io::Error::last_os_error());
    }
    Ok(())
}

#[cfg(unix)]
fn acknowledge_terminal_interrupt(info: &libc::siginfo_t) {
    let descriptor = TERMINAL_INTERRUPT_ACK_DESCRIPTOR.load(Ordering::SeqCst);
    // Terminal-generated signals have no sending process. An explicit signal
    // to the launcher has a sender PID and must keep its normal forwarding
    // behavior.
    if descriptor >= 3 && unsafe { info.si_pid() } == 0 {
        let acknowledgement = [1u8];
        unsafe {
            let _ = libc::write(
                descriptor,
                acknowledgement.as_ptr().cast(),
                acknowledgement.len(),
            );
        }
    }
}

pub fn cancelled() -> bool {
    SIGNAL.load(Ordering::SeqCst) != 0
}

pub fn cancellation_generation() -> usize {
    CANCELLATION_GENERATION.load(Ordering::SeqCst)
}
pub fn check_cancelled() -> Result<()> {
    if cancelled() {
        Err(Failure::new(
            Code::Cancelled,
            "Operation interrupted; already completed OS effects are not undone.",
        ))
    } else {
        Ok(())
    }
}

pub fn finish(code: i32) -> ! {
    let signal = SIGNAL.load(Ordering::SeqCst);
    // Owned operations return numeric cancellation after their cleanup. Only
    // delegated children reproduce Unix signal termination via exit_child.
    #[cfg(unix)]
    if signal == libc::SIGINT || signal == libc::SIGTERM {
        std::process::exit(128 + signal);
    }
    #[cfg(windows)]
    if signal != 0 {
        std::process::exit(130);
    }
    if signal != 0 {
        finish_signal(signal);
    }
    std::process::exit(code)
}

pub fn finish_signal(signal: i32) -> ! {
    #[cfg(unix)]
    {
        let _ = signal_hook::low_level::emulate_default_handler(signal);
    }
    std::process::exit(128 + signal)
}

pub fn exit_child(status: ExitStatus) -> ! {
    #[cfg(unix)]
    {
        use std::os::unix::process::ExitStatusExt;
        if let Some(signal) = status.signal() {
            finish_signal(signal);
        }
    }
    // A child may handle cancellation and deliberately return its own exit code.
    std::process::exit(status.code().unwrap_or(1))
}

pub fn delegated(mut command: Command) -> Result<ExitStatus> {
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        command.creation_flags(windows_sys::Win32::System::Threading::CREATE_NEW_PROCESS_GROUP);
    }
    let mut child = command.spawn().map_err(|_| {
        Failure::new(
            Code::SpawnFailed,
            "Could not start the child command; check its executable, PATH, permissions and \
             supported argument encoding.",
        )
    })?;
    tracing::debug!(operation = "run-env", pid = child.id(), "Child started");
    loop {
        let signal = SIGNAL.swap(0, Ordering::SeqCst);
        if signal != 0 {
            #[cfg(unix)]
            unsafe {
                libc::kill(child.id() as i32, signal);
            }
            #[cfg(windows)]
            unsafe {
                windows_sys::Win32::System::Console::GenerateConsoleCtrlEvent(
                    windows_sys::Win32::System::Console::CTRL_BREAK_EVENT,
                    child.id(),
                );
            }
        }
        if let Some(status) = child.try_wait().map_err(|e| Failure::io(&e))? {
            return Ok(status);
        }
        std::thread::sleep(POLL);
    }
}

/// Block on cancellable work without introducing a timeout or retaining content
/// on disk.
pub fn interruptible<T: Send + 'static>(
    work: impl FnOnce() -> Result<T> + Send + 'static,
) -> Result<T> {
    interruptible_until(None, work)
}

/// Block on cancellable OS work until an optional monotonic deadline. The
/// worker can outlive the caller when the operating system does not provide a
/// cancellation primitive, so callers must not let its result create side
/// effects after the boundary has elapsed.
pub fn interruptible_until<T: Send + 'static>(
    deadline: Option<Instant>,
    work: impl FnOnce() -> Result<T> + Send + 'static,
) -> Result<T> {
    let (tx, rx) = mpsc::sync_channel(1);
    std::thread::spawn(move || {
        let _ = tx.send(work());
    });
    loop {
        check_cancelled()?;
        let timeout = match deadline {
            Some(deadline) => {
                let remaining = deadline.saturating_duration_since(Instant::now());
                if remaining.is_zero() {
                    return Err(Failure::new(
                        Code::TerminationTimeout,
                        "Execution time limit expired while preparing the child command.",
                    ));
                }
                POLL.min(remaining)
            }
            None => POLL,
        };
        match rx.recv_timeout(timeout) {
            Ok(result) => return result,
            Err(mpsc::RecvTimeoutError::Timeout) => (),
            Err(_) => {
                return Err(Failure::new(
                    Code::IoFailed,
                    "Operating system worker stopped unexpectedly.",
                ))
            }
        }
    }
}

pub fn read_bounded(mut reader: impl Read, limit: usize) -> Result<Vec<u8>> {
    let mut bytes = Vec::new();
    reader
        .by_ref()
        .take((limit + 1) as u64)
        .read_to_end(&mut bytes)
        .map_err(|e| Failure::io(&e))?;
    if bytes.len() > limit {
        return Err(Failure::new(
            Code::TextTooLarge,
            "Text exceeds the 16 MiB UTF-8 limit; no output was written.",
        ));
    }
    Ok(bytes)
}

pub fn detached(command: &mut Command) {
    command
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt;
        // An opened application must not receive the waiting CLI's terminal
        // cancellation.
        unsafe {
            command.pre_exec(|| {
                if libc::setsid() == -1 {
                    Err(std::io::Error::last_os_error())
                } else {
                    Ok(())
                }
            });
        }
    }
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        command.creation_flags(
            windows_sys::Win32::System::Threading::CREATE_NEW_PROCESS_GROUP
                | windows_sys::Win32::System::Threading::DETACHED_PROCESS,
        );
    }
}

pub fn wait_child(child: &mut Child, kill_on_cancel: bool) -> Result<ExitStatus> {
    loop {
        if cancelled() {
            if kill_on_cancel {
                let _ = child.kill();
                let _ = child.wait();
            }
            return check_cancelled().and_then(|()| unreachable!());
        }
        if let Some(status) = child.try_wait().map_err(|_| {
            Failure::new(
                Code::WaitUnavailable,
                "Application tracking failed; the application may already have opened. Do not \
                 retry automatically.",
            )
        })? {
            return Ok(status);
        }
        std::thread::sleep(POLL);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[cfg(unix)]
    #[test]
    fn installed_terminal_acknowledgement_accepts_only_the_launcher_descriptor() {
        assert_eq!(
            installed_terminal_interrupt_acknowledgement_descriptor(Some("3")),
            Some(3)
        );
        for value in [None, Some("2"), Some("4"), Some("not-a-descriptor")] {
            assert_eq!(
                installed_terminal_interrupt_acknowledgement_descriptor(value),
                None
            );
        }
    }

    #[cfg(unix)]
    #[test]
    fn terminal_interrupt_acknowledgement_does_not_cross_exec() {
        let mut pipe = [-1; 2];
        assert_eq!(unsafe { libc::pipe(pipe.as_mut_ptr()) }, 0);

        configure_terminal_interrupt_acknowledgement(Some(pipe[1])).unwrap();

        let flags = unsafe { libc::fcntl(pipe[1], libc::F_GETFD) };
        assert_ne!(flags, -1);
        assert_ne!(flags & libc::FD_CLOEXEC, 0);

        configure_terminal_interrupt_acknowledgement(None).unwrap();
        unsafe {
            libc::close(pipe[0]);
            libc::close(pipe[1]);
        }
    }

    #[test]
    fn interruptible_work_stops_waiting_at_its_deadline() {
        let (release, blocked_work) = mpsc::sync_channel(0);
        let deadline = Instant::now()
            .checked_add(Duration::from_millis(1))
            .unwrap();

        let error = match interruptible_until(Some(deadline), move || {
            let _ = blocked_work.recv();
            Ok(())
        }) {
            Ok(()) => panic!("blocked work must not outlive its deadline"),
            Err(error) => error,
        };

        assert_eq!(error.code, Code::TerminationTimeout);
        release.send(()).unwrap();
    }
}
